# weaveplatform-agent

Architecture specification. Core and modules.

---

## 1. Purpose

One agent, any device, physical or virtual. It presents a single install footprint, a single
enrolment and a single identity to an administrator, and behind that runs one module per Weave
product that has a device-side half.

The design exists to resolve a specific tension. Administrators cannot maintain N agents, so the
install surface must be one thing. But the products behind it ship on independent schedules and
have distinct needs, so the code behind that surface cannot be one thing. Core is the surface.
Modules are the products.

---

## 2. The distinction

**Core is infrastructure with no opinion about what the platform does.** It knows how to be one
authenticated, policied, supervised presence on a machine. It knows nothing about VMs, telemetry
schemas, MDM payloads or session brokering.

**Modules are products.** Each is owned by a product team, versioned with its product, and shipped
on its product's release train.

Concretely: core knows how to deliver a policy document to a module. It does not know what is in
one.

| | Core | Module |
|---|---|---|
| Owns | the device's presence on the platform | one product's device-side behaviour |
| Ships with | `weaveplatform-agent` | its product repo |
| Versioned by | itself | its product |
| Lives in | the install package | fetched, staged and promoted at runtime |
| Changes | rarely | continuously |
| Count | one | one per product |

Core is the single point of failure for the whole product line. That is the argument for keeping it
aggressively boring.

---

## 3. Process model

Modules are separate binaries. This is not preference; Go cannot run in-process code built at a
different time from its host. `plugin` requires a byte-identical toolchain and does not work on
Windows or macOS. Independent module versioning therefore forces a wire boundary.

The model is Terraform's: the host launches a signed binary, performs a handshake, and speaks gRPC
over a local socket for the process lifetime.

Process count is driven by build constraints, not by the product catalogue:

| Zone | Build | Contents |
|---|---|---|
| A | `CGO_ENABLED=0`, static | `weaveboot`, core, CLI, most modules |
| B | pure Go, UI-runtime-coupled | portal — AppKit via purego, WinUI 3 via WASDK |
| C | cgo, clang, entitlement | ESF, xpc, anything requiring a System Extension |

Zone B is separate because an uncaught `NSException` in the purego framework packages terminates
the process and cannot be recovered. The portal must not be able to kill the agent.

Zone C is separate because the OS says so. A System Extension is owned by the operating system,
approved by the user or MDM, and cannot be `exec`'d by core.

Three processes core does not parent:

- `weaveboot` — supervises core, so it can replace it. The relationship inverts.
- the portal — launchd owns per-session lifecycle on macOS. Core requests, it does not spawn.
- any System Extension — the OS owns it.

Everything else is core's child.

---

## 4. Core scope

The test for inclusion: **would two of these on one machine be wrong?** If yes, core. If no, it is a
library or a module.

### In

- **Identity** — device identity, keypair, hardware-backed storage (Secure Enclave, TPM), enrolment,
  attestation, token acquisition and refresh, per-module credential scoping. Supports an ephemeral
  session-scoped variant for non-persistent VMs.
- **Transport** — the authenticated channel to GateWeave. Reconnect, backoff, offline queue, TLS
  configuration, proxy handling. When running as a guest, the hypervisor channel (vsock, HvSocket)
  is available as a second peer and is the attested one. Modules never open their own sockets.
- **Policy** — fetch, verify, cache, evaluate, notify on change. Host-delivered policy is
  authoritative over anything local.
- **Store** — namespaced key/value, one namespace per module, encrypted at rest, one owner of the
  file. Modules cannot read each other's namespace.
- **Supervision** — spawn, signature verification before `exec`, privilege drop, handshake, health
  checks, crash-loop backoff, restart, hot swap, drain, orphan cleanup.
- **Capability probe** — one host inventory at startup. OS version, entitlements, symbol
  availability, hardware, architecture. Gates which modules launch at all.
- **Module lifecycle** — manifest resolution, fetch, staged install, health-gated promotion, N-1
  retention, rollback.
- **Control socket** — the endpoint for the CLI and the portal.
- **UI brokerage** — receives declared surfaces from modules as data and hands them to the portal.
  Core never draws.
- **Event bus** — modules do not import each other; they publish and subscribe through core.
- **Crypto and certificate validation** — deliberately in core rather than the SDK, so a CVE is a
  core patch rather than a rebuild of every module.

### Out

Everything product-shaped, including anything only one product needs however tempting the
generalisation.

---

## 5. Module contract

Two interfaces. This is substantially the whole architecture.

```go
// What every product's device-side half implements.
type Module interface {
    ID() string
    Requires() []Capability
    Init(context.Context, Host) error
    Start(context.Context) error
    Stop(context.Context) error
    Health() Health
}

// What core offers. Growing this is a design review.
type Host interface {
    Identity() Identity
    Transport() Transport
    Policy() PolicyReader
    Store(ns string) Store
    UI() UIBroker
    Schedule(Job)
    Log() *slog.Logger
}
```

### Rules

**`Host` is closed by default.** A module needing a new method is an architecture decision, not a
pull request. This is the only durable defence against core absorbing product logic one
reasonable-sounding request at a time.

**Modules never import each other.** SightWeave wanting DeviceWeave state goes through the event bus
or does not happen.

**`Requires()` is how the agent survives a mixed fleet.** Core probes once at startup and does not
launch modules whose requirements are unmet. No module calls a capability probe at its own call
site. This turns a panic on an unbound symbol from a field incident into a startup log line.

**Modules declare UI surfaces as data.** They never draw. The portal renders. Otherwise AppKit and
WinUI leak into every module and Zone B stops being a boundary.

**`Platform()` is absent from `Host` deliberately.** OS bindings are syscall and purego surfaces and
cannot cross a wire. Each module links `weaveplatform-sdk/platform` itself.

---

## 6. Versioning

Three numbers, not two. Versioning core against modules directly produces a support matrix;
inserting a protocol collapses it to a handshake.

| | Cadence | Form |
|---|---|---|
| Core | its own | semver |
| Protocol | rare, deliberate | monotonic integer |
| Module | its product's | its product's semver |

Core advertises `{min, max}` protocol. Each module declares the protocol it speaks. Compatibility is
negotiated, not tabulated. An N-2 window is the default.

**Binding versions are pinned by protocol, not by core.** Protocol N declares the `go-bindings-*`
versions its participants build against. A bindings bump is a protocol event. This prevents a
two-year-old module linking bindings whose behaviour core no longer expects.

**Self-hosted pinning uses the channel manifest** — a signed document mapping a service version to a
known-good set of module versions and the protocol they assume. SaaS follows a rolling manifest;
self-hosted pins one. Same mechanism, different policy.

---

## 7. Handshake

```
core → module   protocol version, capability set, module config,
                socket path, credentials, privilege level
module → core   protocol spoken, declared requirements,
                health endpoint, surfaces offered
```

Capability probing lives here rather than in module code, so the capability vocabulary versions with
the protocol.

---

## 8. Module lifecycle

- **Verify before `exec`.** Modules are fetched after install, so Gatekeeper is not protecting you.
  Core verifies signature and Team ID itself, every launch. Non-negotiable.
- **Stage, then promote.** Fetch, verify, stage alongside, launch, health check, promote. Retain N-1.
- **Swap atomically.** The new process is up and healthy before the old one drains.
- **Fail closed.** Exponential backoff on crash loop, then a circuit breaker that pins to
  last-known-good rather than restarting forever.
- **Contain.** Job Objects on Windows so orphans die with core. launchd owns core only; core owns
  modules.
- **Drop privilege at spawn.** The manifest declares the level. Not every module is root.

---

## 9. Install footprint

The promise is that the installed footprint never changes: package receipt, launchd plists, service
registration, TCC/PPPC grants, MDM profiles, firewall and EDR allowlists, signing identity, any
System Extension. That is what triggers reprompts and breaks fleets.

The binaries behind it change freely. `weaveboot` supervises core and can replace it in place;
staged, health-gated, with rollback. Image-baked core is the VM special case, not the mechanism.

"Never update" is not the requirement. **Invisible and reversible** is.

---

## 10. Layout

```
weaveplatform-api            contracts: proto, openapi, events, shared schemas, generated Go
weaveplatform-sdk            errors, log, config, retry, platform seam, module scaffolding
weaveplatform-agent          weaveboot, core, cli, portal, template, packaging
weaveplatform-manifest       signed channel manifests, signing chain

weaveplatform-agent-modules  the modules monorepo — one Go module per directory, each
                             versioned and released on its product's train (<module>/vX.Y.Z
                             tags, one go.mod per module)
```

Dependency direction is one-way and enforced by repository (and go.mod) boundary:

```
weaveplatform-api  ←  weaveplatform-sdk  ←  weaveplatform-agent
                                        ←  weaveplatform-agent-modules/<module>
go-bindings-*      ←  weaveplatform-sdk/platform   (the platform seam wraps the bindings)
go-bindings-*      ←  weaveplatform-agent-modules/<module>   (product-specific APIs, directly)
```

Either way the `go-bindings-*` versions are pinned by the protocol (§6), recorded in the
channel manifest's `bindings` block.

Core and modules never import each other. Products never import each other. Anything two products
share moves up.

---

## 11. Build order

Protocol, then core, then one module. Prove the shape before the second module exists.

The test that matters is not that a module runs — it is that core at protocol N accepts a module
built against N-1 and cleanly refuses one built against N-3. If that is not solid before the second
module lands, independent versioning is a liability rather than the property it was meant to be.

`sysinfo` is the first module: Zone A, read-only, no UI, unprivileged. It is deliberately not
a product — it is the module template, and it exercises every host surface exactly once
(capability gate, policy watch, store, events, scheduled work, health). The first *product*
module lands only after the N-1/N-3 test above is a required CI check.

---

## 12. Open

- ~~**Privilege levels.**~~ **Resolved:** privilege is manifest-declared from day one —
  `privilege: system | service | user` plus a session placement
  (`system | per-user-console | per-user-all`), and the supervisor drops privilege at spawn.
  Not every module is root; `sysinfo` runs unprivileged to prove the drop path, and the
  Windows clipboard case (session 0 owns a clipboard no interactive user sees) is why session
  placement is declared, not inferred.
- **Zone C.** Is ESF in scope for SightWeave? If yes, the immutable-footprint promise acquires a
  qualification and that component should be designed first rather than bolted on.
- **Module binary size.** Each links its own copy of the generated bindings. Measure early; if a
  module lands at 80MB the staged-update story changes shape.
