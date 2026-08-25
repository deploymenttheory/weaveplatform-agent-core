# sdk — what module authors read

The conventions in the repository root's `CLAUDE.md` apply here unchanged. Two things are
stricter in this directory:

- **Comments here are documentation for people outside this codebase.** An unclear one
  costs more than an unclear one in core.
- **Never import `github.com/deploymenttheory/weaveplatform-agent/internal/...` or `/cmd`.**
  The compiler would allow it — Go's `internal/` rule is path-based and `sdk/` sits inside
  the agent's module path — and CI (`go-test.yml`, job `boundary`) fails the build if it
  happens. Modules build against this module alone; anything they need from core crosses
  the wire as a host service, which is an architecture decision (spec §5).
