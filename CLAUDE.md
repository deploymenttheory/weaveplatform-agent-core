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
is used. Windows (`go-bindings-win32`/`-wmi`) is pure Go and builds under
`CGO_ENABLED=0`, so always use it. macOS (`go-bindings-macosplatform`)
frameworks transitively pull `runtime/cgo` (NSException handling) and need
`CGO_ENABLED=1`; in a `CGO_ENABLED=0` build they are not viable and `x/sys`
is the sanctioned fallback for that call, noted in a comment.

### Only sanctioned fallbacks (no house binding exists)

- **Linux** has no house bindings SDK — use `golang.org/x/sys/unix` there.
- OS-neutral plumbing shared with Linux in a `//go:build unix` file (e.g.
  raw-tty via `golang.org/x/term` in `internal/transport`) may stay on `x/*`
  because it must also compile for Linux.
- A bare POSIX syscall with no framework/service equivalent (e.g. `O_NOCTTY`)
  is not an "OS API" the bindings cover; plain `syscall`/`x/sys` is fine.
