# weaveplatform-agent/sdk

The library layer of the Weave platform agent: what modules build on, and the contract core
implements. A nested Go module of the agent repository, tagged `sdk/vX.Y.Z`, so a module can
pin it independently of core's releases:

```
require github.com/deploymenttheory/weaveplatform-agent/sdk vX.Y.Z
```

Depends only on infrastructure (grpc, protobuf, x/sys, go-winio). `CGO_ENABLED=0`
throughout — this is a Zone A library. It never imports core (`../internal`); CI enforces it.

## Packages

| Package | What |
|---|---|
| `modulesdk` | The module runtime: implement `Module`, call `modulesdk.Serve(m)` — handshake, lifecycle dispatch, health serving and the `Host` client are handled for you |
| `modulesdk/testkit` | `StubCore`, which drives the core side of the handshake against a real module binary; fake host data; the protocol-compat harness every module's CI runs |
| `gen/go/weave/agent/v1` | Protocol 1, generated from [`../proto`](../proto): the handshake vocabulary, `ModuleService` (module-served) and the host services core serves. Committed; regenerate with `buf generate` at the repo root |
| `gen/go/weave/control/v1` | `ControlService` — core's operator control socket, spoken by `weavectl` and the portal. Not a module contract |
| `handshake` | The one stdout line and the environment both sides agree on before any RPC; exit code 78 for a clean protocol refusal. Neither side owns it |
| `ipc` | Local transport seam: unix sockets vs Windows named pipes, listen/dial helpers, per-OS peer credentials. No TLS by design — the OS permissions on the socket are the boundary |
| `hvchannel` | The hypervisor channel's framing and authentication — see below |
| `manifest` | Go types and validation for the module manifest and the signed channel manifest, and the detached-signature *format*. Verification lives in core so a signing CVE is a core patch, not a module rebuild |
| `platform` | The thin OS seam: host info, well-known paths, session helpers. **No bindings re-exports** — modules link `go-bindings-*` directly at protocol-pinned versions |
| `werror` | Error conventions: sentinels + wrapping |
| `wlog` | `slog` construction; module logs stream to core after Init |
| `config` | Handshake-delivered config document > env > defaults; no files, no viper |
| `retry` | Exponential backoff with full jitter; a policy calculator, not a loop runner |

A package earns its place here by having a consumer outside core. One that only core uses
moves to `internal/`.

## The hypervisor channel

`hvchannel` is the one contract that is not a proto: length-prefixed frames carrying a JSON
`Envelope{Module, Kind, Data}` between core inside a guest and the host tooling outside it,
plus the Ed25519 challenge/response the guest uses to authenticate that host.

It lives in the sdk because **both ends must encode identically and nothing on that wire
would catch a mismatch** — there is no negotiation and no version exchange, so a field
renamed on one side simply stops matching on the other, and the symptom is a guest that
never answers. Core's transport (`internal/transport`) and each product's host client
(guestweave's `pkg/guesthost`) both import it.

The format has **no resynchronisation**: a reader that loses its place cannot find the next
boundary. Each end therefore has exactly one reader and serialises its writes. The package
deliberately provides no locking — a mutex there would imply it was safe to hand one
connection to two owners.

It is a different wire from the module protocol, so it does not move the protocol integer.

## Writing a module

Start with [`docs/writing-a-module.md`](docs/writing-a-module.md) — the Module interface,
the Host surface, health semantics, the manifest, and testing with `testkit.StubCore`. Then
copy `sysinfo`, the platform's own module, which exists to be copied.

## Rules

- **Breaking wire changes bump the protocol** — a new `weave/agent/v2` package beside `v1`,
  never an edit in place. `buf breaking` gates this; [`../docs/PROTOCOL.md`](../docs/PROTOCOL.md)
  says what does and does not move the integer.
- **The capability vocabulary lives in `handshake.proto`** so it versions with the protocol.
- **Host is closed by default** (spec §5). A new RPC on a host service is an architecture
  decision, not a pull request. This SDK grows by design review, not accretion.
- Modules never import each other, never open their own sockets, never draw UI.
