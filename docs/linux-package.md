# The Linux package

How core gets onto a Linux machine, and why it arrives this way.

## Why a package at all

macOS has `codesign` and Windows has Authenticode: a signature the OS itself
checks at exec time. Linux has no such construct, so it uses the mechanism it
does have — **a signed package from a GPG-signed repository**, with dpkg
verifying provenance at install. That is what every Linux agent in the field
relies on, and it is why the Linux verifier
(`internal/verify/release_linux.go`) does not re-check a signature it has no key
for. Its job is to confirm that what dpkg vouched for has not since become
writable by anyone who is not root.

That is also why the module binary and the directory holding it must be
root-owned and not group- or world-writable. A root-owned `0755` binary can still
be swapped wholesale by anyone able to write the directory around it, so checking
only the file would be a check in name. The packages below set both.

## Two packages

| Package | Built by | Contents |
|---|---|---|
| `weave-agent` | `.goreleaser.yaml` (`nfpms:`) in this repo | `/usr/lib/weave/{weave-agent,weaveboot,weavectl}`, `/usr/bin/weavectl` → symlink, `/lib/systemd/system/weave-agent.service`, `/usr/lib/weave/modules/` |
| `weave-guestweave` | `guestweave-linux/packaging/` in weaveplatform-agent-modules | `/usr/lib/weave/modules/guestweave/{guestweave,module.manifest.json}`, `Depends: weave-agent` |

Two rather than one because core and the module live in different repositories
with independent release cadences, and a module that can only ship when core ships
is a module the pipeline cannot promote on its own. `Depends:` keeps the ordering
honest without merging them.

Details that are load-bearing rather than stylistic:

- **All three binaries in one directory.** `weaveboot.currentBinary` resolves core
  as "the binary beside me" when no staged version layout exists — the fresh
  install case — so they must be siblings.
- **The unit runs `weaveboot`, not core.** systemd supervises weaveboot, weaveboot
  supervises core, core supervises modules. A unit pointing at core directly takes
  away the in-place core replace (stage, health-gate, roll back).
- **`KillMode=mixed`.** Core runs its own module shutdown; the default
  (`control-group`) signals the modules directly at the same moment, so core finds
  their sockets gone and reports a failed shutdown for what is really a race.
- **The module binary is named for the module id.** `core.discoverModules` looks
  for `<dir>/<id>`; a differently-named binary is simply never found — no error,
  no log line.
- **No sandboxing directives in the unit.** The guestweave module's job is to
  execute what the host asks inside this guest; `ProtectSystem` and friends would
  break the feature rather than harden it. The isolation boundary is the VM.

## The repository

`packaging/apt/aptrepo` builds a flat apt repository from a directory of `.deb`s
and signs it. `packaging/apt/build-repo.sh` drives both packages and the
repository in one command.

What apt actually verifies, and therefore what the signature has to cover: it
fetches `InRelease`, checks it against the key named in the source's `signed-by=`,
checks `Packages` against the digests inside that signed file, then checks each
`.deb` against the digest inside `Packages`. **The signature over `Release` is what
makes every byte downstream of it trusted.** A repository of individually signed
`.deb`s with an unsigned `Release` would give apt nothing it checks by default —
which is why the repository signature is the gate here, not a `_gpgorigin` member
inside the package. (nfpm can add one when `WEAVE_DEB_SIGN_KEY` is set; it is
belt-and-braces.)

```sh
# Build both packages and a signed repository.
GNUPGHOME=~/.weave/bringup/gnupg ./packaging/apt/build-repo.sh <gpg-key-id>

# Re-check what was built, the way apt will.
go run ./packaging/apt/aptrepo -verify ~/.weave/bringup/repo
```

`aptrepo` reads the `.deb` control data itself (an `ar` archive containing
`control.tar.gz`) rather than shelling out to `dpkg-deb`, because the machines
that build and test this package are macOS hosts with no dpkg. It supports gzip
control archives — what these configs produce — and refuses others by name rather
than guessing.

## Installing into a guest

`packaging/cloudinit/` builds a NoCloud seed ISO that installs the agent at first
boot, with nobody touching the guest:

```sh
./packaging/cloudinit/make-seed.sh http://192.168.64.1:8000/ \
    ~/.weave/bringup/repo/weave-archive-keyring.asc \
    ~/.weave/vms/agent-test/channel.pub \
    ~/.weave/bringup/seed.iso

weave run agent-test --mount ~/.weave/bringup/seed.iso --no-graphics
```

Two keys, two jobs: the **archive key** is what apt checks to trust the packages;
the **channel key** is what the guest checks to decide whether the host may command
it afterwards (see `architecture.md`). Neither substitutes for the other, and the
seed refuses to build without both.

### Things that will waste an afternoon

- **A repeated `instance-id`** makes cloud-init treat the boot as resumed and skip
  every module. The guest boots clean with nothing installed and nothing in the
  log to say why. `make-seed.sh` stamps a fresh one every build — so rebuild the
  seed for every boot that should reinstall.
- **A snapshot version never changes.** `0.1.2~next` is `0.1.2~next` however many
  times its binaries change, so a guest that has it already correctly decides
  there is nothing to do, and runs the previous binaries while every log line says
  the install succeeded. The seed uses `apt-get install --reinstall` for exactly
  this; released versions would not need it.
- **`/dev/console` in a Debian arm64 cloud guest under Virtualization.framework
  goes nowhere the host can read.** The image's kernel cmdline names a serial
  console this hypervisor does not provide, so `weave run --serial-path` produces
  an empty file — not even boot messages. Evidence has to go to a runcmd's stdout,
  which cloud-init records in `/var/log/cloud-init-output.log`.

### Reading evidence out of a stopped guest

When the guest is down and the question is what happened inside it, mount its disk
read-only. The root partition starts at sector 262144 on the Debian arm64 cloud
image:

```sh
docker run --rm --privileged -v ~/.weave/vms/agent-test:/vm debian:12 sh -c '
  mkdir -p /mnt/g && mount -o ro,loop,offset=134217728 /vm/disk.img /mnt/g
  journalctl -D /mnt/g/var/log/journal -u weave-agent --no-pager | tail -40'
```

## What a healthy first boot looks like

```
Started weave-agent.service - Weave platform agent.
{"msg":"core starting","modules_dir":"/usr/lib/weave/modules","capabilities":2}
{"msg":"hypervisor channel connected","device":"/dev/virtio-ports/org.weave.agent.0"}
{"msg":"module running","module":"guestweave","pid":661,"protocol":1}
```

And on the host, from the run process:

```
guest channel: guestweave guestweave/1 answered (linux/arm64)
guest channel: authenticated
```

Failure meanings worth knowing before you meet them:

| Symptom | What it means |
|---|---|
| `module not launched: requirements unmet` | The channel device is missing, so the module was gated. The host published no **named** port — see the capability probe. |
| A refusal from `release_linux.go` | dpkg installed the module with the wrong ownership or mode; check the directory as well as the file. |
| `guest channel: the guest refused this host's key` | The guest's `/etc/weave/channel.pub` is a different key, or absent. The guest is healthy; provisioning is not. |
| No cloud-init output at all | The `instance-id` was not fresh. |
