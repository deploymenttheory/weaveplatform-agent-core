# weaveplatform-api

Contracts for the Weave platform agent: protobuf definitions, JSON schemas, and the generated
Go they produce. This repo is the root of the dependency graph — everything imports it, it
imports nothing.

```
weaveplatform-api  ←  weaveplatform-sdk  ←  weaveplatform-agent
                                        ←  weaveplatform-agent-modules
```

## Contents

| Path | What |
|---|---|
| `proto/weave/agent/v1/` | Protocol 1: the core↔module contract — handshake vocabulary, `ModuleService`, and the host services (store, policy, events, identity, transport, log, ui) |
| `proto/weave/control/v1/` | `ControlService` — core's control socket, spoken by `weavectl` and the portal |
| `schema/` | JSON Schemas for the module manifest and the signed channel manifest |
| `gen/go/` | Generated Go, committed — consumers never run protoc |
| `PROTOCOL.md` | What the protocol integer is, what bumps it, the handshake, the registry |
| `docs/services.md` | The service map: who serves what, on which socket, and why |

## Regenerating

```sh
buf generate
```

Requires `buf`, `protoc-gen-go`, `protoc-gen-go-grpc`. Generated output under `gen/go` is
committed; CI fails if it drifts from the proto sources.

## Rules

- **Breaking wire changes bump the protocol** — a new `weave/agent/v2` package beside `v1`,
  never an edit in place. `buf breaking` gates this.
- **The capability vocabulary lives in `handshake.proto`** so it versions with the protocol.
- **Host services are closed by default.** A new RPC on a host service is an architecture
  decision, not a pull request.
