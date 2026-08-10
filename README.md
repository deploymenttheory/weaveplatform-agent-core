# weaveplatform-agent

One agent, any device, physical or virtual. It presents a single install footprint, a single
enrolment and a single identity to an administrator, and behind that runs one module per Weave
product that has a device-side half.

**Core is infrastructure with no opinion about what the platform does.** It knows how to be one
authenticated, policied, supervised presence on a machine. **Modules are products** — separate
signed binaries, owned by product teams, shipped on their products' release trains, fetched and
promoted at runtime. Core launches them Terraform-style: verify signature, spawn, handshake,
speak gRPC over a local socket for the process lifetime.

The full architecture lives in [`spec.md`](spec.md). Read it before changing anything here.

## Repos

```
weaveplatform-api      ←  weaveplatform-sdk  ←  weaveplatform-agent          (this repo)
                                             ←  weaveplatform-agent-modules  (the modules)
weaveplatform-manifest    signed channel manifests, signing chain
```

## This repo

| Path | What |
|---|---|
| `cmd/weave-agent` | Core: the supervised, enrolled presence on the machine |
| `cmd/weaveboot` | Supervises core so core can be replaced in place — staged, health-gated, with rollback |
| `cmd/weavectl` | Operator CLI over the control socket |
| `internal/supervise` | Module supervision: verify-before-exec, privilege drop, handshake, health, crash-loop breaker |
| `internal/lifecycle` | Module install: fetch, stage, health-gated promote, N-1 retention, rollback |
| `test/protocompat` | The test that matters: core at protocol N accepts an N-1 module and cleanly refuses N-3 |

## Building

```sh
CGO_ENABLED=0 go build ./...   # Zone A: static, no cgo, macOS + Windows + Linux
```

Core is the single point of failure for the whole product line. That is the argument for
keeping it aggressively boring.
