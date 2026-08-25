# Development

## Local setup

This repository holds two Go modules — core at the root and the sdk under `sdk/` — plus the
frozen protocol fixture under `test/protocompat/v1`. Core's `go.mod` `replace`s the sdk to
`./sdk`, so editing the sdk and building core needs no workspace and no tag. Every Go
module here is public; no `GOPRIVATE` and no token is needed to fetch them.

Product repositories (guestweave, …) pin the sdk by tag. To develop one against an untagged
sdk, use an untracked workspace in the parent directory (`go.work` is gitignored here):

```sh
weaveplatform/
├── go.work                 # untracked; go work init ./weaveplatform-agent/sdk ./guestweave
├── weaveplatform-agent/
└── guestweave/
```

**Trap:** a dependency bump inside `sdk/` must be followed by `go mod tidy` at the root — the
root builds the sdk through its `replace`, so its `go.sum` needs the new hashes too, and a
`-mod=readonly` build (CI, goreleaser) fails without them. Dependabot only edits `sdk/go.mod`;
the go-test tidy check catches the gap on the PR.

**Trap:** a workspace masks stale `go.mod` pins — a product can build locally against the
sdk's HEAD while its recorded requirement is releases behind. CI and goreleaser build from
the real pins. Before releasing anything, validate in each module with:

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

# Lay out a module (sysinfo — the platform's own module and the template)
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

Conventional-commit PR titles (enforced), golangci-lint and go-test run per Go module
(`.` and `sdk`) on macOS + Windows + Linux runners; `buf` lints the protos, checks for
breaking changes against `main`, and fails if `sdk/gen` drifts from `proto/`. A `boundary`
job fails if `sdk/` ever imports `weaveplatform-agent/internal` — Go's `internal/` rule is
path-based, so the compiler would allow it now that the sdk lives in this tree.

One secret drives releasing: `RELEASE_PLEASE_PAT`, because release-please's tags must
trigger the release workflows and the default `GITHUB_TOKEN` cannot.

Releasing is automatic and per component. Conventional commits accumulate; release-please
opens one PR per component that changed — `release X.Y.Z` for core, `release sdk X.Y.Z` for
the sdk — and merging tags `vX.Y.Z` or `sdk/vX.Y.Z`. A core tag builds signed archives and
the deb (goreleaser + cosign keyless); an sdk tag builds nothing, it is a Go module version
for modules to pin. `feat:` bumps minor, `fix:` bumps patch, `ci:`/`docs:`/`chore:` don't
release. Commits are assigned to a component by the paths they touch, so keep sdk and core
changes in separate commits when both move.

**Merge the sdk release PR first, then close and let release-please regenerate the core one.**
Both PRs edit `.release-please-manifest.json`. Once one merges, the other's branch is stale
and conflicts — and release-please does not rebase a release PR whose version did not change
("PR remained the same"), so the conflict never clears on its own. Closing it makes the next
push to `main` open a fresh one.
