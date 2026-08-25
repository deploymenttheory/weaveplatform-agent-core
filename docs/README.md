# Documentation

- [`../spec.md`](../spec.md) — the governing architecture specification: core/module split,
  process model, protocol, handshake, lifecycle, install footprint.
- [`architecture.md`](architecture.md) — what was built and how the pieces relate: repo
  graph, process model, handshake sequence, supervision state machine, install flow
  (with diagrams).
- [`development.md`](development.md) — local setup, running the agent, testing, CI and the
  release pipeline.
- [`PROTOCOL.md`](PROTOCOL.md) — what the protocol integer is and what bumps it; the
  handshake; the N-2 window.
- [`services.md`](services.md) — the gRPC service map: who serves what, on which socket.
- [`../sdk/docs/writing-a-module.md`](../sdk/docs/writing-a-module.md) — the module author's
  guide.
- [`linux-package.md`](linux-package.md) — the deb, the apt repository and the cloud-init seed.
