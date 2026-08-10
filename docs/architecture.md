# Architecture

How the Weave platform agent is put together. [`spec.md`](../spec.md) is the governing
document — this explains what was built and how the pieces relate. The one-line summary: one
agent, any device; **core is the surface, modules are the products**.

## The repositories

Five repositories, one dependency direction. Nothing imports downward, core and modules never
import each other, and anything two products share moves up.

```mermaid
flowchart TD
    api["<b>weaveplatform-api</b><br/>protobuf contracts, generated Go,<br/>manifest types + JSON Schemas, PROTOCOL.md"]
    sdk["<b>weaveplatform-sdk</b><br/>modulesdk runtime, ipc, handshake,<br/>werror/wlog/config/retry, platform seam, testkit"]
    agent["<b>weaveplatform-agent</b><br/>weaveboot · core · weavectl<br/>(this repo)"]
    modules["<b>weaveplatform-agent-modules</b><br/>one module per directory:<br/>sysinfo, guestweave-*, …"]
    manifest["<b>weaveplatform-manifest</b><br/>signed channel manifests,<br/>signing chain, weavemanifest tool"]
    bindings["go-bindings-*<br/>macosplatform · win32 · wmi"]

    api --> sdk
    sdk --> agent
    sdk --> modules
    bindings --> sdk
    bindings --> agent
    bindings --> modules
    manifest -. "signed channel manifests<br/>(consumed at runtime, never imported)" .-> agent

    style agent fill:#1f6feb,color:#fff
```

`weaveplatform-manifest` is deliberately not a Go dependency of anything: core consumes its
*documents* over HTTP and verifies them against a root key baked into core
(`internal/manifestverify`). A signing CVE is a core patch, not an SDK rebuild.

## Process model

Modules are separate binaries — Go cannot run in-process code built at a different time from
its host, so independent module versioning forces a wire boundary. The model is Terraform's:
launch a verified binary, handshake, speak gRPC over a local socket for the process lifetime.

```mermaid
flowchart TD
    init["launchd / SCM"] --> boot["<b>weaveboot</b><br/>versioned core tree, staged replace,<br/>crash-loop revert"]
    boot -->|"exec, supervise"| core["<b>weave-agent</b> (core)<br/>identity · transport · policy · store<br/>supervision · lifecycle · event bus"]
    core -->|"verify sig → spawn →<br/>handshake → health-poll"| m1["module: sysinfo"]
    core -->|"same"| m2["module: guestweave-*<br/>(consolidation phase)"]
    ctl["weavectl"] -->|"ControlService<br/>unix socket / named pipe"| core
    portal["portal (Zone B, future)"] -.->|"reads declared surfaces<br/>via ControlService"| core
    gw["GateWeave (stub today)"] <-->|"policy · enrolment ·<br/>messages"| core

    style boot fill:#238636,color:#fff
    style core fill:#1f6feb,color:#fff
```

Three processes core does not parent: **weaveboot** (supervises core so it can replace it —
the relationship inverts), the **portal** (session lifecycle belongs to launchd), and any
**System Extension** (the OS owns it). Everything else is core's child; on Windows a Job
Object with kill-on-close guarantees module trees die with core.

## The handshake

Protocol compatibility is negotiated, never tabulated. Core advertises a `{min,max}` window;
each module speaks exactly one protocol integer. An out-of-window module exits with code 78
before listening — clean refusal, never a crash loop.

```mermaid
sequenceDiagram
    participant S as core supervisor
    participant M as module process
    participant H as core host services

    S->>S: verify signature + Team ID / Authenticode subject
    S->>M: spawn with env: WEAVE_PROTOCOL_MIN/MAX,<br/>WEAVE_HANDSHAKE_TOKEN, WEAVE_HOST_ADDR, WEAVE_SOCKET_DIR
    alt protocol outside window
        M-->>S: exit 78 — recorded as unsupported, no restart
    else protocol in window
        M->>M: listen on module socket
        M->>S: stdout, one line: WEAVE|1|proto|network|addr
        S->>M: gRPC ModuleService.Init(capabilities, config, privilege)
        M->>H: dial WEAVE_HOST_ADDR presenting one-time token
        H->>H: bind connection to module identity —<br/>store namespace, policy scope, event origin
        M-->>S: InitResponse: requires, surfaces, health interval
        S->>M: Start
        loop every health interval
            S->>M: Health — degraded is tolerated, unhealthy strikes restart
        end
    end
```

All host-service auth is per-connection: the token rides every RPC as metadata, and the
socket itself lives in a 0700 directory (SDDL-ACL'd pipe on Windows). A module cannot name,
let alone reach, another module's namespace.

## Supervision

Each module runs under a per-module state machine (`internal/supervise`). It fails closed: a
module that cannot run safely does not run.

```mermaid
stateDiagram-v2
    [*] --> pending
    pending --> requirements_unmet: manifest needs capabilities<br/>this host lacks — never launched
    pending --> starting
    starting --> unsupported_protocol: exit 78 — no restart
    starting --> running: handshake + Init + Start
    running --> backoff: process exit / health strikes
    backoff --> starting: jittered exponential delay
    backoff --> breaker: ≥5 crashes in 10 min
    running --> stopped: core shutdown (drain → Shutdown → kill after grace)
    breaker --> [*]
    stopped --> [*]
```

The breaker pins the module down rather than restarting forever; a rollback or operator
action resets it. Crash counts only reset after the module proves stable (60s up).

## Module install: stage → promote → rollback

The lifecycle manager (`internal/lifecycle`) owns install. Verify-before-exec is
non-negotiable: modules are fetched after install, so Gatekeeper and SmartScreen protect
nobody here.

```mermaid
flowchart LR
    fetch["fetch artifact<br/>(digest + size enforced<br/>against signed channel manifest)"]
    stage["stage under staging/id/ver<br/><b>verify signature there</b>"]
    promote["promote → modules/id/versions/ver<br/>flip <code>current</code>"]
    swap["supervisor hot swap<br/>(drain old, start new)"]
    gate{"health gate:<br/>running + stable?"}
    ok["retain N-1 in <code>previous</code>,<br/>prune older"]
    rb["auto-rollback:<br/>flip current back,<br/>restart old version"]

    fetch --> stage --> promote --> swap --> gate
    gate -->|pass| ok
    gate -->|fail| rb

    style rb fill:#da3633,color:#fff
    style ok fill:#238636,color:#fff
```

The same shape applies one level up: **weaveboot** replaces core itself. An updater leaves a
new core under `core/staging/`; weaveboot promotes it at loop start and reverts a version
that cannot stay up. The installed footprint (launchd plist, service registration, signing
identity — see [`../pkg/README.md`](../pkg/README.md)) never changes; only the binaries
behind it do.

```mermaid
stateDiagram-v2
    [*] --> promote: staged version found →<br/>becomes current, old becomes previous
    promote --> run
    [*] --> run: nothing staged
    run --> stable: up ≥ 60s — crash count resets
    stable --> run: exit → restart current
    run --> crashed: exits early
    crashed --> run: backoff, retry
    crashed --> revert: 3 early exits —<br/>flip current back to previous
    revert --> run
```

## Core's internal layout

| Package | Owns |
|---|---|
| `internal/supervise` | spawn, verify-before-exec, handshake, health, backoff, breaker, Job Objects |
| `internal/hostserv` | per-module gRPC host services, token-gated: store, policy, events, identity, transport |
| `internal/store` | one bbolt file, bucket per namespace, AES-256-GCM per value (namespace+key as AAD), master key sealed by `keyprotect` (DPAPI / keyfile → Secure Enclave/TPM later) |
| `internal/policy` | fetch from GateWeave, cache in store for offline restarts, wake Watch streams on change |
| `internal/identity` | Ed25519 device identity behind a Provider seam, enrolment, per-module scoped credentials |
| `internal/transport` | peer mux (GateWeave HTTP peer; hypervisor channel reserved), durable offline queue |
| `internal/lifecycle` | staged install, health-gated promote, N-1 retention, rollback |
| `internal/manifestverify` | two-tier Ed25519 chain verification for channel manifests |
| `internal/capability` | the one host probe at startup that gates module launch |
| `internal/eventbus` | at-most-once in-core pub/sub — the only lateral channel between modules |
| `internal/weaveboot` | versioned core tree, staged replace, crash-loop revert |
| `internal/controlsock` | ControlService for weavectl and (later) the portal |

## The test that matters

`test/protocompat/v1` is a module frozen at protocol 1 — pinned go.mod, built with
`GOWORK=off` so the pins decide what it links. Every CI run proves current core accepts it
in-window and refuses it cleanly once the window moves past it. When protocol 2 ships, `v2/`
is added beside it; `v1/` is never deleted. That property — negotiated compatibility that is
continuously proven — is what makes independent module versioning an asset instead of a
liability.

## What is deliberately not here yet

- **Portal (Zone B)** — UI surfaces are already declared as data and brokered through
  ControlService; nothing draws yet.
- **Zone C** (System Extensions, cgo) — the capability probe and manifest zone field are the
  seams.
- **GateWeave** — `test/stubgateweave` defines the contract core programs against.
- **Hypervisor channel** — `transport.Peer` is the seam; virtio-serial/HvSocket arrive with
  the guestweave consolidation, along with per-user session supervision.
