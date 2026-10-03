#!/bin/sh
# End-to-end test of password login: a machine whose sshd accepts only passwords (a
# separate sshd on 127.0.0.1:2222 with its own configuration; the system sshd is not
# touched), reached with --password-stdin; a wrong password is reported as one; then
# `hosts setup-key` moves it to key login. Run by CI on Linux and macOS; needs sudo.
set -eu
# shellcheck source=lib/testhost.sh
. "$(dirname "$0")/lib/testhost.sh"

BIN=${BIN:-$PWD/bin/hopsesh}
PW_USER=hspass
PORT=2222
ID=2c1d0e9f-3a4b-4c5d-8e6f-7a8b9c0d1e2f
WORK=$(mktemp -d /tmp/hopsesh-pw.XXXXXX)
export HOPSESH_CONFIG_DIR="$WORK/config" HOPSESH_STATE_DIR="$WORK/state" CLAUDE_CONFIG_DIR="$WORK/claude"
mkdir -p "$CLAUDE_CONFIG_DIR" # Claude Code has run here once

fail() { echo "FAIL: $*" >&2; exit 1; }
cleanup() {
  if [ -f "$WORK/sshd.pid" ]; then sudo kill "$(cat "$WORK/sshd.pid")" 2>/dev/null || true; fi
  rm -f "$WORK/password"
}
trap cleanup EXIT
cd /

# The machine: a user with a password, no keys, and one session.
PW="pw-$(od -An -N9 -tx1 /dev/urandom | tr -d ' \n')"
( umask 077; printf '%s\n' "$PW" > "$WORK/password" )
th_add_user "$PW_USER" "$PW"
th_clean_login_env
RHOME=$(th_home "$PW_USER")
SLUG=$(printf '%s' "$RHOME/proj" | sed 's/[^A-Za-z0-9]/-/g')
sudo -u "$PW_USER" -H sh -c "mkdir -p ~/proj ~/.claude/projects/$SLUG && rm -rf ~/.ssh"
cat > "$WORK/session.jsonl" <<JSONL
{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"$ID","cwd":"$RHOME/proj","version":"2.1.284","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"hello from a password machine"}}
{"type":"custom-title","customTitle":"password login test","sessionId":"$ID"}
JSONL
sudo install -o "$PW_USER" -m 0600 "$WORK/session.jsonl" "$RHOME/.claude/projects/$SLUG/$ID.jsonl"

# Its own sshd: passwords and keys allowed, only this user, only on loopback.
th_privsep_dir
sudo ssh-keygen -q -t ed25519 -N '' -f "$WORK/host_ed25519"
cat > "$WORK/sshd_config" <<CONF
Port $PORT
ListenAddress 127.0.0.1
HostKey $WORK/host_ed25519
PidFile $WORK/sshd.pid
PasswordAuthentication yes
KbdInteractiveAuthentication no
PubkeyAuthentication yes
AuthorizedKeysFile .ssh/authorized_keys
UsePAM yes
AllowUsers $PW_USER
Subsystem sftp internal-sftp
CONF
sudo /usr/sbin/sshd -t -f "$WORK/sshd_config"
sudo /usr/sbin/sshd -f "$WORK/sshd_config"
th_wait_port "$PORT"

# This user reaches it through an ssh alias (as people do).
mkdir -p ~/.ssh && chmod 700 ~/.ssh
[ -f ~/.ssh/id_ed25519 ] || ssh-keygen -q -t ed25519 -N '' -f ~/.ssh/id_ed25519
touch ~/.ssh/config && chmod 600 ~/.ssh/config
printf '\nHost pwbox\n  HostName 127.0.0.1\n  Port %s\n  User %s\n' "$PORT" "$PW_USER" >> ~/.ssh/config

"$BIN" hosts add pwbox pwbox --password
"$BIN" hosts --json | grep -q '"auth": *"password"' || fail "hosts does not show password login"
"$BIN" trust pwbox --yes

# Without a password source, a non-interactive run says what is missing.
"$BIN" ls --host pwbox --no-local --json < /dev/null > "$WORK/none.json" || true
grep -q '"status": *"auth"' "$WORK/none.json" || { cat "$WORK/none.json"; fail "no password: not reported as a login problem"; }

# A wrong password is reported as one (after ssh refuses it).
printf 'not-the-password\n' | "$BIN" ls --host pwbox --no-local --json --password-stdin > "$WORK/wrong.json" || true
grep -q 'did not accept the password' "$WORK/wrong.json" || { cat "$WORK/wrong.json"; fail "wrong password not reported"; }

# The right one lists the session and pulls it.
"$BIN" ls --host pwbox --no-local --json --password-stdin < "$WORK/password" > "$WORK/ls.json"
grep -q "\"session\": *\"$ID\"" "$WORK/ls.json" || { cat "$WORK/ls.json"; fail "ls did not list the session"; }
TARGET="$WORK/here/proj"
mkdir -p "$TARGET"
"$BIN" pull "pwbox:$ID" --to "$TARGET" --yes --json --password-stdin < "$WORK/password" > "$WORK/pull.json" \
  || { cat "$WORK/pull.json"; fail "pull failed"; }
TSLUG=$(cd "$TARGET" && pwd -P | sed 's/[^A-Za-z0-9]/-/g')
[ -f "$CLAUDE_CONFIG_DIR/projects/$TSLUG/$ID.jsonl" ] || fail "transcript not installed"
grep -rq "$PW" "$HOPSESH_CONFIG_DIR" "$HOPSESH_STATE_DIR" && fail "the password was written to disk"

# Switch to key login: one password login adds the key, then no password is needed.
"$BIN" hosts setup-key pwbox --password-stdin < "$WORK/password"
"$BIN" hosts --json | grep -q '"auth": *"key"' || fail "setup-key did not switch to key login"
sudo grep -qF "$(cut -d' ' -f1-2 ~/.ssh/id_ed25519.pub)" "$RHOME/.ssh/authorized_keys" || fail "key not installed"
"$BIN" ls --host pwbox --no-local --json < /dev/null > "$WORK/key.json"
grep -q "\"session\": *\"$ID\"" "$WORK/key.json" || { cat "$WORK/key.json"; fail "key login did not work"; }
echo "password test passed"
