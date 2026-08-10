# Windows / platform security — validation handoff

This document hands off the platform-specific security work (Phase 2 part 2
of the combative-review improvement plan) to an engineer or agent working
on a **Windows host** (and, where noted, a macOS host with a login keychain
or a Linux host with a TPM). The code is written and cross-compiles; what
remains is **runtime validation** on the target OS and a few clearly-marked
follow-ups that need infrastructure this repo can't stand up in CI on a
non-Windows dev box.

Everything below builds today with:

```sh
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
```

## What is implemented (needs Windows runtime validation)

### S9 — DPAPI master-key protection with machine-bound entropy
`internal/store/keyprotect/dpapi_windows.go`. DPAPI machine scope alone lets
any local process `CryptUnprotectData` the blob; this binds secondary
entropy derived from `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid`
(machine-bound, not stored beside `store.key`).

**Run on Windows:** `go test ./internal/store/keyprotect/`
- `TestDPAPIRoundTrip` — seal/unseal survives; sealed blob ≠ plaintext.
- `TestDPAPIEntropyBinding` — unseal with wrong entropy fails (entropy is
  actually applied). If this passes on a machine with no MachineGuid, check
  the registry read.

### S11 — named-pipe SDDL
`weaveplatform-sdk/ipc/ipc_windows.go`. Pipes are now created with
`D:(A;;GA;;;SY)(A;;GA;;;BA)` (SYSTEM + Administrators only), replacing
go-winio's broad default that let any local user open the control/host pipe.
`ListenPipeSDDL` is provided for privilege-dropped modules that need a wider
descriptor.

**Validate on Windows:**
- As a non-admin user, attempt to open `\\.\pipe\weave-control` — must be
  denied. As admin/SYSTEM — allowed.
- Confirm a running module (SYSTEM today) can still reach its host pipe.
- **Follow-up:** when per-module Windows privilege lands, set a per-module
  SDDL granting the module's SID via `ListenPipeSDDL` in
  `internal/supervise/launch.go` (host socket) instead of the default.

### S12 — Authenticode revocation + thumbprint pin
`internal/verify/authenticode_windows.go`. Revocation is on
(`WTD_REVOKE_WHOLECHAIN`, cache-only + exclude-root so offline installs
don't fail-closed on a network CRL fetch), and the leaf is pinned by SHA-1
thumbprint (`AuthenticodeThumbprint` in the manifest) when provided, falling
back to the subject display name.

**Run on Windows:** `go test ./internal/verify/` — see
`authenticode_windows_test.go`. It needs a self-signed cert; the file header
has the PowerShell to mint one. The tests must show:
1. unsigned binary refused;
2. self-signed (untrusted-chain) binary refused — the Authenticode analogue
   of the macOS "anchor apple" fix;
3. thumbprint mismatch refused when a thumbprint is pinned.

## Follow-ups that need target-OS infrastructure

### S5 — Windows per-peer identity on the host/control pipes
`weaveplatform-sdk/ipc/peercred_windows.go` currently returns no uid, so the
authorizers fall through to the SDDL (S11) as the gate — which is correct and
sufficient today. To add per-peer PID/identity checks:
- Get the pipe HANDLE (go-winio does not expose it on `net.Conn`; either
  vendor a small pipe type that exposes `Fd()`, or use the win32 bindings'
  `GetNamedPipeClientProcessId`), then map PID→SID and compare.
- Wire it into `authorizeModulePeer` (`internal/supervise/spawn_windows.go`)
  and `controlAuthorizer` (`internal/controlsock/authorize_windows.go`),
  which are allow-all placeholders today.

The **unix** side of S5 is done and validated on macOS: `SO_PEERCRED` /
`LOCAL_PEERCRED` in `peercred_{linux,darwin}.go`, enforced by
`authorizeModulePeer` (host socket) and `controlAuthorizer` (control socket).

### S5 — per-module service accounts (installer)
`internal/supervise/spawn_unix.go` still drops all `service`-privilege
modules to one shared account (`serviceAccount()`), so the peer-uid check
distinguishes modules from non-modules but not modules from each other. True
per-module isolation needs the installer to create a per-module account (or
the supervisor to create one lazily) and `dropCreds` to return it. This is
installer/packaging work (`pkg/`), tracked, not code-complete.

### S9 — macOS Keychain / Secure Enclave and Linux TPM
`internal/store/keyprotect/file_unix.go` is still a plaintext passthrough on
unix (the master key sits in `store.key`). Targets:
- **macOS:** back the `Protector` with the login/System keychain via
  `go-bindings-macosplatform/opinionated/tools/keychain` (store the master
  key as a generic password; `store.key` holds only a marker). Validate that
  a daemon context can read it without a UI prompt (needs the right
  entitlement/keychain-access-group).
- **Linux:** seal the key to the TPM (`go-tpm/tpm2` Seal/Unseal) or
  `systemd-creds`. Needs a TPM (or swtpm) to validate.

Until these land, `keyprotect.New()` on unix returns the file protector; the
startup directory-permission tightening (`internal/layout/tighten_unix.go`,
`Ensure()` chmod 0700) is the interim gate.

### S10 — enrolment server key provisioning
`internal/identity/identity.go` implements the signed-challenge +
pinned-server-assignment handshake and requires https + a pinned `ServerPub`
in release (dev bypasses via `AllowInsecureEnroll`). Wiring the real
`ServerPub` (embedded like the manifest root key, or delivered by the
installer) is the remaining provisioning step; until then release enrolment
is refused (fail closed).

## Summary for the Windows-host validator

1. `go test ./internal/store/keyprotect/` — DPAPI round-trip + entropy.
2. `go test ./internal/verify/` — Authenticode (mint a self-signed cert per
   the file header).
3. Manual: non-admin cannot open `\\.\pipe\weave-control`; a module still
   reaches its host pipe.
4. Report back so the allow-all Windows authorizers (S5) can be tightened to
   real per-peer checks.
