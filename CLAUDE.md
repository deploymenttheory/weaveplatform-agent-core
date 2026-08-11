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
