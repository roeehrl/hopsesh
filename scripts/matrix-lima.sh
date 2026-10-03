#!/bin/sh
# The scenario matrix between macOS and Linux on one Intel Mac, both ways: an Ubuntu VM
# (Lima, QEMU with hardware virtualization) is the Linux machine (user hsremote), and a
# throwaway macOS user (hsremote) over this Mac's sshd the macOS one. macOS → Linux runs
# here; Linux → macOS runs inside the VM. Used by CI on macos-15-intel; needs sudo.
#
#   scripts/matrix-lima.sh <macOS bin dir> <Linux bin dir> <out dir>
set -eu
# shellcheck source=lib/testhost.sh
. "$(dirname "$0")/lib/testhost.sh"
BIN=$(cd "$1" && pwd); LBIN=$(cd "$2" && pwd); mkdir -p "$3"; OUT=$(cd "$3" && pwd)
U=hsremote
fail() { echo "FAIL: $*" >&2; exit 1; }

# This Mac as "there" for Linux → macOS.
th_add_user "$U"
th_clean_login_env
UHOME=$(th_home "$U")
sudo mkdir -p /usr/local/bin
sudo install -m 0755 "$BIN/hopsesh" /usr/local/bin/hopsesh
sudo install -m 0755 "$BIN/hsmatrix" /usr/local/bin/hsmatrix
for a in claude codex; do sudo install -m 0755 "$BIN/fakeagent" "/usr/local/bin/$a"; done
mkdir -p ~/.ssh && chmod 700 ~/.ssh
[ -f ~/.ssh/id_ed25519 ] || ssh-keygen -q -t ed25519 -N '' -f ~/.ssh/id_ed25519
sudo -u "$U" -H sh -c 'umask 077; mkdir -p ~/.ssh; touch ~/.ssh/authorized_keys'
sudo -u "$U" -H sh -c 'for f in ~/.zshenv ~/.bashrc; do echo "export PATH=/usr/local/bin:\$PATH" >> "$f"; done'
th_start_sshd

# The Linux VM.
command -v limactl >/dev/null || brew install lima qemu
limactl start --name=hs --tty=false --vm-type=qemu --cpus=2 --memory=4 template:ubuntu-24.04
G() { limactl shell hs -- "$@"; }
G sudo sh -c 'DEBIAN_FRONTEND=noninteractive apt-get update -qq && apt-get install -y -qq git >/dev/null'
for p in hopsesh hsmatrix fakeagent; do limactl copy "$LBIN/$p" "hs:/tmp/$p"; done
limactl copy ~/.ssh/id_ed25519.pub hs:/tmp/mac.pub
G sudo sh -c '
  id hsremote >/dev/null 2>&1 || useradd -m -s /bin/bash hsremote
  install -m 755 /tmp/hopsesh /usr/local/bin/hopsesh
  install -m 755 /tmp/hsmatrix /usr/local/bin/hsmatrix
  install -m 755 /tmp/fakeagent /usr/local/bin/claude
  install -m 755 /tmp/fakeagent /usr/local/bin/codex
  install -d -o hsremote -m 700 /home/hsremote/.ssh
  install -o hsremote -m 600 /tmp/mac.pub /home/hsremote/.ssh/authorized_keys
  su hsremote -c "ssh-keygen -q -t ed25519 -N \"\" -f /home/hsremote/.ssh/id_ed25519"'
# The guest user's key may log in to the Mac's hsremote.
G sudo cat /home/hsremote/.ssh/id_ed25519.pub | sudo tee -a "$UHOME/.ssh/authorized_keys" >/dev/null
sudo chown "$U" "$UHOME/.ssh/authorized_keys"; sudo chmod 600 "$UHOME/.ssh/authorized_keys"

# The VM is reached through Lima's forwarded ssh port on this Mac (so by alias only).
PORT=$(limactl list hs --format '{{.SSHLocalPort}}')
[ -n "$PORT" ] || fail "no ssh port for the VM"
printf 'Host hsm-lima\n  HostName 127.0.0.1\n  Port %s\n  User hsremote\n' "$PORT" >> ~/.ssh/config
chmod 600 ~/.ssh/config

failed=0
echo "== macOS → Linux"
(cd / && "$BIN/hsmatrix" run -hopsesh "$BIN/hopsesh" -there hsm-lima -alias hsm-lima -label 'macos→linux' -out "$OUT/m2l") || failed=$((failed + 1))

echo "== Linux → macOS"
G sudo -u hsremote -H sh -c '
  printf "Host hsm-mac\n  HostName host.lima.internal\n  User hsremote\n" >> ~/.ssh/config
  cd ~ && hsmatrix run -hopsesh /usr/local/bin/hopsesh -there hsremote@host.lima.internal -alias hsm-mac -label "linux→macos" -out /tmp/l2m' || failed=$((failed + 1))
G sudo chmod -R a+rX /tmp/l2m 2>/dev/null || true
limactl copy -r hs:/tmp/l2m "$OUT/" 2>/dev/null || true
if [ -f "$OUT/l2m/summary.md" ] && [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then cat "$OUT/l2m/summary.md" >> "$GITHUB_STEP_SUMMARY"; fi
exit "$failed"
