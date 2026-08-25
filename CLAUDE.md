# weaveplatform-agent — engineering conventions

## OS APIs: always use the house Go bindings when technically viable

When core needs an operating-system API (verify, keyprotect, supervise,
capability, transport, layout, …), use the house bindings — not
`golang.org/x/sys/*`, cgo, or shelling out — whenever it is technically viable:

- **macOS** → `github.com/deploymenttheory/go-bindings-macosplatform`
- **Windows** → `github.com/deploymenttheory/go-bindings-win32` and
  `github.com/deploymenttheory/go-bindings-wmi`

Prefer the binding even when an `x/sys` or stdlib path looks shorter. If a
binding is missing a call you need, the default is to add it to the binding SDK
rather than bypass it; if that is genuinely out of scope, note the gap and use
the narrowest fallback.

"Technically viable" means the binding exposes the call **and** builds where it
is used. Both SDKs are pure Go and build under `CGO_ENABLED=0`, so both are
always viable: Windows (`go-bindings-win32`/`-wmi`) is generated Go over
`syscall`, and macOS (`go-bindings-macosplatform`, **v0.19.0+**) runs its whole
`bindings/` surface — Objective-C frameworks and Apple C libraries alike — on
`github.com/ebitengine/purego`, `dlopen`ing the dylib at runtime.

> An earlier version of this rule said the macOS frameworks needed
> `CGO_ENABLED=1` for NSException handling. That described the pre-purego
> binding and is no longer true (the binding repo's own README is stale on this
> point too). What purego does change: an uncaught `NSException` is not
> converted to a Go panic — it terminates the process. Validate before calling.

Coverage, not cgo, is the remaining macOS limit: `bindings/libraries/bsd` is
essentially empty, so bare POSIX calls (`sysctl`, `settimeofday`, `fcntl`) have
no house wrapper and take the fallback below.

### Only sanctioned fallbacks (no house binding exists)

- **Linux** has no house bindings SDK — use `golang.org/x/sys/unix` there.
- OS-neutral plumbing shared with Linux in a `//go:build unix` file (e.g.
  raw-tty via `golang.org/x/term` in `internal/transport`) may stay on `x/*`
  because it must also compile for Linux.
- A bare POSIX syscall with no framework/service equivalent (e.g. `O_NOCTTY`)
  is not an "OS API" the bindings cover; plain `syscall`/`x/sys` is fine.

## Comments: what and why, sparse and deep

Comment the **reasoning a reader cannot recover from the code**. Delete anything
that restates it.

```go
// BAD — the signature already says this
// SetTime sets the time. t is the time to set.

// GOOD — says what the code cannot
// A step, not a slew: a guest resuming from a snapshot can be days out, and
// adjtime would take longer to converge than the guest is likely to run.
```

**Sparse, not uniform.** Most code carries no comment at all. Effort concentrates
at the few places where a decision was made and the alternative was plausible —
an ordering that must not be reversed, a fallback that must not fail open, a
number that must match something elsewhere. A file where every function has a
paragraph is not thorough, it is undifferentiated: the reader has no way to tell
the load-bearing note from the throat-clearing.

**Length is not the metric.** A comment earns its lines by carrying content, and
some genuinely need twenty. Never pad one to look thorough, and never cut one
that is doing real work to hit a length. If it took an afternoon to learn, write
it down in full.

What is worth the space:

- **Why this and not the obvious alternative.** "PDH means opening a query,
  adding counters by localised path string, and collecting twice before the
  first number appears — three FILETIME counters do it here."
- **What a failure would look like**, especially the silent ones. "A truncated
  count silently gives an available-memory reading of zero, which reads as a
  quiet guest rather than a broken call."
- **Orderings and invariants** whose violation is not a compile error. "The
  acknowledgement must be on the wire before the command that kills the OS."
- **What was deliberately NOT done**, when a reader would otherwise add it.
  "Sizes the ABI passes by reference stay skipped rather than being passed
  truncated for the callee to read as garbage."
- **Facts learned the hard way** — an API that reports success while doing
  nothing, a field the kernel validates strictly, a service that silently
  reverses your change.

What is not:

- Restating a name, signature, or the line below it.
- Narrating structure: `// loop over the items`, `// error handling`.
- Marking sections: `// --- helpers ---`.
- Changelog or attribution. That is what git is for.
- Apologising for code. Fix it, or write down why it stays.

**Exported identifiers** get the standard Go doc comment, starting with the
name, and usually one sentence. Add paragraphs below it only when the caller
needs the reasoning to use the thing correctly.

**Prefer one home for a rationale.** When the same "why" applies in several
places, write it once where the decision lives and point at it from the others,
rather than restating it in each.

## Documentation

Docs describe what the code does **now**. A feature that changes behaviour,
adds a wire operation, or moves a boundary is not finished until the docs that
describe that area say so — in the same change, not a follow-up.

- **README** is the entry point: what this repo is, what it is for, how to build
  and test it. Not a feature list.
- **`docs/`** covers the things a reader cannot get from the code: architecture,
  protocols and their compatibility rules, trust chains, release pipelines.
- **Handoff documents** (work another person or machine must finish) state what
  to run, what a pass looks like, and **what each failure would mean** — the
  last one matters most where a call can fail by returning a plausible zero.
- Say plainly what is unverified. "Compiles but has never run on Windows" is
  more useful than silence, and far more useful than implied confidence.

## Repository layout: two Go modules, one direction

`.` is core (`github.com/deploymenttheory/weaveplatform-agent-core`, tags `vX.Y.Z`); `sdk/` is
the module SDK (`…/weaveplatform-agent/sdk`, tags `sdk/vX.Y.Z`). Core `replace`s the sdk to
`./sdk` and builds the tree it ships with; modules pin the sdk by tag. The sdk **never**
imports core — CI enforces what the old repository boundary used to. `proto/` and `schema/`
are sources; `sdk/gen` is generated from them and committed (`buf generate`).
`test/protocompat/v1` deliberately pins the *old* module paths (`weaveplatform-sdk v0.2.1`,
`weaveplatform-api v0.2.0`): it is the frozen protocol-1 fixture and must not be rewritten,
tidied, or bumped by dependabot.

A package belongs in `sdk/` only if something other than core imports it. Otherwise it is
`internal/`.
