#!/bin/sh
# Build a NoCloud seed ISO that installs the agent at first boot.
#
#   ./packaging/cloudinit/make-seed.sh <repo-url> <archive-key> <channel-pub> [out.iso]
#
# e.g. ./packaging/cloudinit/make-seed.sh http://192.168.64.1:8000/ \
#          ~/.weave/bringup/repo/weave-archive-keyring.asc \
#          ~/.weave/vms/agent-test/channel.pub ~/.weave/bringup/seed.iso
#
# Two different keys, doing two different jobs. The archive key is what apt checks
# to trust the packages; the channel key is what the guest checks to decide whether
# the host may command it afterwards. Neither substitutes for the other.
#
# Attach it with: weave run <vm> --mount seed.iso
#
# Declarative, not screen automation: the guest is configured by a file it reads
# at boot, so the install is reproducible and reviewable rather than a sequence of
# keystrokes that has to hit the same pixels twice.
set -eu

REPO_URL="${1:-}"
KEY_FILE="${2:-}"
CHANNEL_PUB="${3:-}"
OUT="${4:-$HOME/.weave/bringup/seed.iso}"
HERE="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

if [ -z "$REPO_URL" ] || [ -z "$KEY_FILE" ] || [ -z "$CHANNEL_PUB" ]; then
	echo "usage: $0 <repo-url> <archive-key> <channel-pub> [out.iso]" >&2
	exit 2
fi
[ -f "$KEY_FILE" ] || { echo "no such archive key: $KEY_FILE" >&2; exit 1; }
# Required, not optional. A seed that quietly omitted it would build a guest that
# looks healthy and refuses every command, which is a much worse afternoon than a
# seed that refuses to build.
[ -f "$CHANNEL_PUB" ] || { echo "no such channel public key: $CHANNEL_PUB" >&2; exit 1; }

SEED="$(dirname "$OUT")/seed"
rm -rf "$SEED"
mkdir -p "$SEED"

# A FRESH instance-id every build. cloud-init records the id it last ran for and
# treats a repeat as a resumed boot, skipping every module — the guest then boots
# clean with nothing installed and nothing in the log to say why. This one line
# is the difference between a seed that works twice and a seed that works once.
INSTANCE_ID="weave-bringup-$(uuidgen)"

cat > "$SEED/meta-data" <<EOF
instance-id: $INSTANCE_ID
local-hostname: weave-guest
EOF

# Splice the armored key in at the @@KEY@@ line, indented to sit under
# write_files' `content: |`. Split-and-cat rather than a substitution: an armored
# key is multi-line, and both sed's `s` and awk's -v choke on a replacement
# containing newlines — quietly, in awk's case, which would produce a seed that
# builds and then trusts nothing.
sed -n '1,/^@@KEY@@$/p' "$HERE/user-data.tmpl" | sed '$d' > "$SEED/user-data"
sed 's/^/      /' "$KEY_FILE" >> "$SEED/user-data"
sed -n '/^@@KEY@@$/,/^@@CHANNELKEY@@$/p' "$HERE/user-data.tmpl" | sed '1d;$d' >> "$SEED/user-data"
sed 's/^/      /' "$CHANNEL_PUB" >> "$SEED/user-data"
sed -n '/^@@CHANNELKEY@@$/,$p' "$HERE/user-data.tmpl" | sed '1d' >> "$SEED/user-data"

# The URL is single-line, so a substitution is fine; | as the delimiter because
# the value contains slashes.
sed -i '' "s|@@REPO_URL@@|$REPO_URL|g" "$SEED/user-data"

rm -f "$OUT"
# The volume label MUST be CIDATA: the NoCloud datasource finds its seed by
# filesystem label, not by device, so a mislabelled ISO is simply never read.
hdiutil makehybrid -iso -joliet -default-volume-name CIDATA -o "$OUT" "$SEED" >/dev/null

echo "$OUT"
echo "  instance-id: $INSTANCE_ID"
echo "  repo:        $REPO_URL"
echo "  channel key: $CHANNEL_PUB"
