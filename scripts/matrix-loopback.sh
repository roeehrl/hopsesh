#!/bin/sh
# The scenario matrix over real SSH on one Linux or macOS machine: "there" is a throwaway
# user (hsremote) with hopsesh, the matrix helper and the stand-in agents on its PATH;
# "here" is this user with hopsesh's own folders kept apart. Needs sudo, sshd and git.
#
#   scripts/matrix-loopback.sh <bin dir with hopsesh, hsmatrix, fakeagent> <label> <out dir> [hsmatrix flags]
set -eu
# shellcheck source=lib/testhost.sh
. "$(dirname "$0")/lib/testhost.sh"
BIN=$(cd "$1" && pwd); LABEL=$2; OUT=$3; shift 3
U=hsremote
th_add_user "$U"
th_clean_login_env
UHOME=$(th_home "$U")
sudo mkdir -p /usr/local/bin
sudo install -m 0755 "$BIN/hopsesh" /usr/local/bin/hopsesh
sudo install -m 0755 "$BIN/hsmatrix" /usr/local/bin/hsmatrix
for a in claude codex; do sudo install -m 0755 "$BIN/fakeagent" "/usr/local/bin/$a"; done

mkdir -p ~/.ssh && chmod 700 ~/.ssh
[ -f ~/.ssh/id_ed25519 ] || ssh-keygen -q -t ed25519 -N '' -f ~/.ssh/id_ed25519
sudo -u "$U" -H sh -c 'umask 077; mkdir -p ~/.ssh'
sudo install -o "$U" -m 0600 ~/.ssh/id_ed25519.pub "$UHOME/.ssh/authorized_keys"
# The same machine by an ssh alias (naming=alias rows).
printf 'Host hsm-box\n  HostName 127.0.0.1\n  User %s\n' "$U" >> ~/.ssh/config
chmod 600 ~/.ssh/config
th_start_sshd

mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
cd / # the test user cannot read the caller's working directory
exec "$BIN/hsmatrix" run -hopsesh "$BIN/hopsesh" -there "$U@127.0.0.1" -alias hsm-box -label "$LABEL" -out "$OUT" "$@"
