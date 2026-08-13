#!/bin/sh
# Build both Debian packages and the signed apt repository that serves them.
#
#   ./packaging/apt/build-repo.sh <gpg-key-id> [outdir]
#
# This is the local bring-up path: one command from a working tree to something a
# guest can `apt-get install` from. The release pipeline builds the weave-agent
# package through goreleaser and the module package through its own repository —
# this script drives both, because a host testing an install needs them together
# and they live apart.
#
# The module's package comes from the sibling checkout. Point MODULES_REPO
# somewhere else if your layout differs; a missing checkout is an error rather
# than a repository quietly built with core alone, because the resulting guest
# would boot, look healthy, and serve nothing.
set -eu

KEY="${1:-}"
OUT="${2:-$HOME/.weave/bringup/repo}"
HERE="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
MODULES_REPO="${MODULES_REPO:-$HERE/../weaveplatform-agent-modules}"
ARCH="${ARCH:-arm64}"
STAGE="$(dirname "$OUT")/debs"

if [ -z "$KEY" ]; then
	echo "usage: $0 <gpg-key-id> [outdir]" >&2
	echo "  the key signs Release, which is the only thing apt actually verifies" >&2
	exit 2
fi
if [ ! -d "$MODULES_REPO/guestweave-linux" ]; then
	echo "no guestweave-linux in $MODULES_REPO — set MODULES_REPO" >&2
	exit 1
fi

# GOWORK=off throughout: the workspace masks stale go.mod pins, and a package
# built against workspace-resolved dependencies is not the package CI would build.
export GOWORK=off

echo "==> weave-agent"
cd "$HERE"
goreleaser release --snapshot --clean --skip=sign,archive,publish --timeout 15m >/dev/null

echo "==> weave-guestweave"
cd "$MODULES_REPO/guestweave-linux"
./packaging/build-deb.sh "$ARCH" >/dev/null

echo "==> repository"
rm -rf "$STAGE"
mkdir -p "$STAGE"
cp "$HERE/dist/weave-agent_"*"_$ARCH.deb" "$STAGE/"
cp "$MODULES_REPO/guestweave-linux/dist/weave-guestweave_"*"_$ARCH.deb" "$STAGE/"

cd "$HERE"
go run ./packaging/apt/aptrepo -in "$STAGE" -out "$OUT" -key "$KEY"
go run ./packaging/apt/aptrepo -verify "$OUT"
