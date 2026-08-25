# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.1](https://github.com/deploymenttheory/weaveplatform-agent-core/compare/v0.5.0...v0.5.1) (2026-08-25)


### Bug Fixes

* **module-release:** create the release when attaching artifacts to a tag that has none ([918d3cd](https://github.com/deploymenttheory/weaveplatform-agent-core/commit/918d3cd68f7555a1b1c8bc527c74a77a7055b7a0))
* **module-release:** the debs land in dist/, and upload to the ref that was published ([9bad51d](https://github.com/deploymenttheory/weaveplatform-agent-core/commit/9bad51d92b8e2a31bf5933e3a9de630f28e54f9b))

## [0.5.0](https://github.com/deploymenttheory/weaveplatform-agent-core/compare/v0.4.0...v0.5.0) (2026-08-25)


### Features

* sysinfo moves in as modules/sysinfo, weavemanifest ships with core ([9507270](https://github.com/deploymenttheory/weaveplatform-agent-core/commit/9507270a9dc6a257bb88f562cc5377a9e06c86aa))
* sysinfo moves in as modules/sysinfo, weavemanifest ships with core ([2943532](https://github.com/deploymenttheory/weaveplatform-agent-core/commit/2943532541e13626ac2434e687f9bd55b6ec67c2))

## [0.4.0](https://github.com/deploymenttheory/weaveplatform-agent-core/compare/v0.3.1...v0.4.0) (2026-08-25)


### Features

* module paths follow the repository name (weaveplatform-agent-core) ([043b870](https://github.com/deploymenttheory/weaveplatform-agent-core/commit/043b870ebfb182ad38b8b43d111acd0f190c54a2))
* module paths follow the repository name (weaveplatform-agent-core) ([b3b12f4](https://github.com/deploymenttheory/weaveplatform-agent-core/commit/b3b12f482cb177c2bcae5dac038ac145c21e1648))

## [0.3.1](https://github.com/deploymenttheory/weaveplatform-agent-core/compare/v0.3.0...v0.3.1) (2026-08-25)


### Bug Fixes

* **build:** tidy the root go.sum after the sdk dependency bumps, and check it in CI ([38223c0](https://github.com/deploymenttheory/weaveplatform-agent-core/commit/38223c0f5673e6998526647a521d66b1c8a1e46b))

## [0.3.0](https://github.com/deploymenttheory/weaveplatform-agent-core/compare/v0.2.2...v0.3.0) (2026-08-25)


### Features

* absorb weaveplatform-sdk and weaveplatform-api as the nested sdk/ module ([5f2439d](https://github.com/deploymenttheory/weaveplatform-agent-core/commit/5f2439dac5ade885acd760e5da30e3091a5460a0))
* absorb weaveplatform-sdk and weaveplatform-api as the nested sdk/ module ([b7e21cf](https://github.com/deploymenttheory/weaveplatform-agent-core/commit/b7e21cf72771c649f37ca66eb829159d33d5f46e))

## [0.2.2](https://github.com/deploymenttheory/weaveplatform-agent-core/compare/v0.2.1...v0.2.2) (2026-08-25)


### Bug Fixes

* **cloudinit:** the seed builder takes a VM name, not a channel key path ([8c88838](https://github.com/deploymenttheory/weaveplatform-agent-core/commit/8c888385117f04b62abe410921dfb35935422f6a))
* **cloudinit:** the seed builder takes a VM name, not a channel key path ([e63aa17](https://github.com/deploymenttheory/weaveplatform-agent-core/commit/e63aa170874d0b0f50bfce67bacab7417060b405))

## [0.2.1](https://github.com/deploymenttheory/weaveplatform-agent/compare/v0.2.0...v0.2.1) (2026-08-14)


### Bug Fixes

* **test:** give hostserv a Windows address, and pin the handshake fix ([9f1e31d](https://github.com/deploymenttheory/weaveplatform-agent/commit/9f1e31d3866a06f3c20519f33e170d51e11c5641))
* **test:** give hostserv a Windows address, and pin the handshake fix ([6f3f427](https://github.com/deploymenttheory/weaveplatform-agent/commit/6f3f4273df68c33991417bcaaef6d21aae81540f))

## [0.2.0](https://github.com/deploymenttheory/weaveplatform-agent/compare/v0.1.1...v0.2.0) (2026-08-13)

Core now installs into a Linux guest from a signed apt repository, starts under
systemd, finds its hypervisor channel, verifies and launches the guestweave
module, and refuses to be driven by a host that cannot prove it holds the VM's
key. Verified on a real Debian 12 arm64 guest, with nobody touching it — the
first time any of this has run in a VM rather than in a loopback test.


### Features

* **packaging:** ship core as a signed .deb from a signed apt repository, with a systemd unit, an apt repository builder and a cloud-init seed ([9de57fb](https://github.com/deploymenttheory/weaveplatform-agent/commit/9de57fbf01cec6bde382e5e2c691e83deaf3643a))
* **transport:** fail closed until the host proves it holds the VM's key, in both directions ([7a8bb5e](https://github.com/deploymenttheory/weaveplatform-agent/commit/7a8bb5e25704a3ea5a5ae6d2c2a5b4ff0cca4552))


### Bug Fixes

* **capability:** identify the hypervisor channel by port name, not by node. The probe claimed the capability from a bare /dev/vsock, which every weave guest has — core would open a device that never delivers a frame while every log line read healthy ([eeb10c3](https://github.com/deploymenttheory/weaveplatform-agent/commit/eeb10c302998feefeebfc40838c2e53f65b69e74))
* **weaveboot:** create the core state directory before launching core, so a fresh package install can write its readiness marker. Without it, crash-loop revert silently degraded to judging a core by uptime alone ([e91fe71](https://github.com/deploymenttheory/weaveplatform-agent/commit/e91fe71b6350a56b72e2e31ac1d2fc8994e6924e))


### Note on this release

The tag was cut by hand rather than by release-please, which could not run at the
time. The artifacts are real: goreleaser built the archives, both `.deb`
packages and the cosign-signed checksums once CI was available again, and the
Debian package was verified to install and start core in a guest.

`go-test` fails on windows-latest in `internal/hostserv` and `test/protocompat`
— unix socket paths and an unknown network "winpipe". Both predate this release
and are in packages it does not touch; it is the first time those tests have
executed on Windows at all. macOS and Linux pass.

## [0.1.1](https://github.com/deploymenttheory/weaveplatform-agent/compare/v0.1.0...v0.1.1) (2026-08-10)


### Bug Fixes

* pin released api/sdk versions and Windows test exe suffix ([9df40db](https://github.com/deploymenttheory/weaveplatform-agent/commit/9df40db00514056d04c236b2024f5539d27732b4))

## 0.1.0 (2026-08-10)


### Features

* adopt architecture spec, strip template scaffolding ([17db931](https://github.com/deploymenttheory/weaveplatform-agent/commit/17db9316fe60e49e4807b6f77be51dbaae0f5b81))
* core skeleton — supervision, host services, control socket, weavectl ([55d35d2](https://github.com/deploymenttheory/weaveplatform-agent/commit/55d35d22126f1c235d50c5e9c7e84db12a5a3088))
* device identity with enrolment, transport mux with offline queue ([eb6a457](https://github.com/deploymenttheory/weaveplatform-agent/commit/eb6a457122ab63816dd20bfe13de325889ee269f))
* encrypted store and policy pipeline ([402b837](https://github.com/deploymenttheory/weaveplatform-agent/commit/402b837cf2fd6fc9b7529981e157c9e9fc665def))
* module lifecycle — staged install, health-gated promote, rollback ([016bc15](https://github.com/deploymenttheory/weaveplatform-agent/commit/016bc15294c27fdabe4fd199382342652667df12))
* verify-before-exec and privilege drop ([7c7e2e2](https://github.com/deploymenttheory/weaveplatform-agent/commit/7c7e2e23e4d3229acf9669777dc1251b8d3b5cba))
* weaveboot staged core replacement with crash-loop revert ([1fdbf2f](https://github.com/deploymenttheory/weaveplatform-agent/commit/1fdbf2f7366b76e9381cf0c90de2073f4ac3fb7e))


### Miscellaneous Chores

* set initial release version ([4603b82](https://github.com/deploymenttheory/weaveplatform-agent/commit/4603b82817fee468fdedb0cf7888801093757cd4))

## [Unreleased]

### Added

- Added xyz [@your_username](https://github.com/your_username)

### Fixed

- Fixed zyx [@your_username](https://github.com/your_username)

## [1.1.0] - 2021-06-23

### Added

- Added x [@your_username](https://github.com/your_username)

### Changed

- Changed y [@your_username](https://github.com/your_username)

## [1.0.0] - 2021-06-20

### Added

- Inititated y [@your_username](https://github.com/your_username)
- Inititated z [@your_username](https://github.com/your_username)
