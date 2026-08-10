# Development

## Local setup

The five repos are developed side by side under one parent directory with an untracked
`go.work` so cross-repo changes are visible without tagging:

```sh
weave/
├── go.work                    # untracked; `go work use` each repo
├── weaveplatform-api/
├── weaveplatform-sdk/
├── weaveplatform-agent/
├── weaveplatform-agent-modules/
└── weaveplatform-manifest/
```

The repos are private Go modules:

```sh
go env -w GOPRIVATE='github.com/deploymenttheory/*'
```

**Trap:** the workspace masks stale `go.mod` pins — code can build locally against a
sibling's HEAD while the recorded requirement is releases behind. CI and goreleaser build
from the real pins. Before releasing, validate with:

```sh
GOWORK=off CGO_ENABLED=0 go build ./...
```

## Running the agent locally

Release builds fail closed on module signatures. Dev builds (`-tags dev`) accept unsigned
binaries and log the bypass loudly; the escape hatch does not exist in release binaries.

```sh
# Build
CGO_ENABLED=0 go build -tags dev -o /tmp/wv/weave-agent ./cmd/weave-agent
CGO_ENABLED=0 go build -o /tmp/wv/weavectl ./cmd/weavectl

# Lay out a module (built from weaveplatform-agent-modules/sysinfo)
mkdir -p /tmp/wv/state/modules/sysinfo
cp sysinfo-binary /tmp/wv/state/modules/sysinfo/sysinfo
cp module.manifest.json /tmp/wv/state/modules/sysinfo/

# Run against a stub GateWeave (optional; omit --gateweave-url to run offline)
/tmp/wv/weave-agent --state-dir /tmp/wv/state \
  --gateweave-url http://127.0.0.1:18099 --policy-interval 10s

# Inspect
/tmp/wv/weavectl -socket /tmp/wv/state/run/control.sock status
/tmp/wv/weavectl -socket /tmp/wv/state/run/control.sock modules
/tmp/wv/weavectl -socket /tmp/wv/state/run/control.sock surfaces

# Install / hot-swap / roll back
/tmp/wv/weavectl -socket ... install -local ./path-to-module-dir
/tmp/wv/weavectl -socket ... rollback sysinfo
```

`WEAVE_STATE_DIR` redirects the entire filesystem layout; without it the platform paths
apply (`/Library/Application Support/Weave`, `%ProgramData%\Weave`, `/var/lib/weave`).
`WEAVE_LOG_LEVEL=debug` raises verbosity everywhere.

## Testing

```sh
go test ./...          # unit + integration; spawns real module processes
```

Integration tests build real module binaries and drive the full handshake — supervision
(kill → backoff → start limit), lifecycle (install → hot-swap → auto-rollback), policy
(stub change → module Watch stream), weaveboot (staged core → crash-loop → revert), and the
protocol-compat fixture. Two conventions the tests rely on:

- Socket dirs come from short `os.MkdirTemp` paths, not `t.TempDir()` — long test names push
  unix socket paths past the 104-byte `sun_path` limit on macOS.
- Test binaries are built with an `.exe` suffix on Windows — Windows cannot exec a binary
  without its extension.

## CI and releasing

Every repo: conventional-commit PR titles (enforced), golangci-lint, go-test on macOS +
Windows + Linux runners. Two org secrets drive everything:

| Secret | Purpose | Where |
|---|---|---|
| `RELEASE_PLEASE_PAT` | release-please tags must trigger downstream workflows; cross-repo promotion dispatch | all five repos |
| `GOMODULES_TOKEN` | read-only fetch of the private api/sdk modules | agent, sdk, modules — in **both** the Actions and Dependabot secret stores (dependabot-triggered runs read a separate store) |

Releasing is automatic: conventional commits accumulate → release-please opens the version
PR → merging tags → the tag builds signed artifacts (goreleaser + cosign keyless here and in
weaveplatform-manifest; ORAS→GHCR in the modules repo). `feat:` bumps minor, `fix:` bumps
patch, `ci:`/`docs:`/`chore:` don't release.
