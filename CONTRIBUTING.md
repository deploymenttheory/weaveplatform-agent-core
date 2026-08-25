# Contributing

Thanks for taking the time to contribute.

Before opening a pull request, read [`spec.md`](spec.md) — it is the governing architecture
document, and two of its rules gate every change here:

- **Core stays boring.** Anything product-shaped belongs in a module, however tempting the
  generalisation. If only one product needs it, it is not core.
- **The `Host` surface is closed by default.** A module needing a new host method is an
  architecture decision raised as an issue, not a pull request.

Bugs and proposals: [issues](https://github.com/deploymenttheory/weaveplatform-agent/issues).
PR titles follow Conventional Commits (enforced by CI). Wire-visible changes to the protocol
happen in `proto/` and are gated by `buf breaking` and [`docs/PROTOCOL.md`](docs/PROTOCOL.md);
the generated Go under `sdk/gen` is committed and CI fails if it drifts.
