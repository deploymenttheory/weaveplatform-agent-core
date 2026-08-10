# The protocol

Three numbers version this platform: core's semver, each module's product semver, and **the
protocol integer** between them. This document owns the integer.

## What the integer is

The protocol is the complete contract a module builds against: the proto package
`weave/agent/v1`, the handshake environment and stdout line, the capability vocabulary, and the
`go-bindings-*` versions its participants build against. Proto package `v<N>` **is** protocol N.

Core advertises `{min, max}` at spawn (`WEAVE_PROTOCOL_MIN`, `WEAVE_PROTOCOL_MAX`). Each module
declares the single protocol it speaks. Compatibility is negotiated at handshake, never
tabulated. **An N-2 window is the default**: core at protocol N accepts modules built against
N-1 and N-2 and cleanly refuses anything older.

## What bumps the integer

A protocol bump is rare and deliberate — a design review, not a side effect. Bump when:

- a wire-visible message or RPC changes incompatibly (buf breaking-change checks gate this);
- the handshake environment or stdout line changes shape;
- the capability vocabulary changes meaning (adding a new capability name is *not* a bump);
- a pinned `go-bindings-*` version moves to a release with changed behaviour.

A bump means a new proto package (`weave/agent/v2`) beside the old one. Core serves both for
the length of the window. The old package is deleted only when it falls out of the window.

## What does not bump the integer

- Adding an optional field to an existing message.
- Adding a new capability name (modules that require it simply don't launch on hosts without it).
- Core releases. Module releases. Anything semver'd.

## Bindings pinning

Protocol N declares the `go-bindings-*` versions its participants build against, recorded in
the channel manifest's `bindings` block. A bindings bump that changes behaviour is a protocol
event. This prevents a two-year-old module linking bindings whose behaviour core no longer
expects.

## The handshake

```
core → module    env: WEAVE_PROTOCOL_MIN, WEAVE_PROTOCOL_MAX, WEAVE_HANDSHAKE_TOKEN,
                      WEAVE_HOST_ADDR, WEAVE_SOCKET_DIR, WEAVE_CONFIG (path, 0600)
module → core    stdout, one line:  WEAVE|1|<protocol>|<network>|<addr>
core → module    gRPC ModuleService.Init on <addr>
module → core    gRPC dial WEAVE_HOST_ADDR presenting the one-time token
```

The leading `1` in the stdout line is the *handshake format* version, distinct from the
protocol integer that follows it. A module whose protocol falls outside the advertised window
must exit with code 78 (EX_CONFIG) before listening; core records "protocol unsupported" and
does not restart it — refusal is clean, never a crash loop.

## Registry

| Protocol | Proto package | Status | Notes |
|---|---|---|---|
| 1 | `weave/agent/v1` | current | initial protocol |
