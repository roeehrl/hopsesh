#!/bin/sh
# End-to-end test over a real SSH server: list a session on "another machine" (a second
# local user reached through sshd) and pull it here. Run by CI on Linux; needs sudo.
set -eu

BIN=${BIN:-$PWD/bin/hopsesh}
REMOTE_USER=hsremote
ID=0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a00
WORK=$(mktemp -d)
export HOPSESH_CONFIG_DIR="$WORK/config" HOPSESH_STATE_DIR="$WORK/state" CLAUDE_CONFIG_DIR="$WORK/claude"

fail() { echo "FAIL: $*" >&2; exit 1; }

# The "remote" machine: a user with one Claude Code session in ~/proj.
sudo useradd -m -s /bin/sh "$REMOTE_USER" 2>/dev/null || true
RHOME=$(getent passwd "$REMOTE_USER" | cut -d: -f6)
SLUG=$(printf '%s' "$RHOME/proj" | sed 's/[^A-Za-z0-9]/-/g')
sudo -u "$REMOTE_USER" sh -c "mkdir -p ~/proj ~/.claude/projects/$SLUG ~/.ssh && chmod 700 ~/.ssh"
cat > "$WORK/session.jsonl" <<JSONL
{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"$ID","cwd":"$RHOME/proj","version":"2.1.284","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"fix the build in $RHOME/proj/main.go"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"$ID","cwd":"$RHOME/proj","timestamp":"2026-10-01T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Edited $RHOME/proj/main.go"}]}}
{"type":"custom-title","customTitle":"integration test","sessionId":"$ID"}
JSONL
sudo install -o "$REMOTE_USER" -m 0600 "$WORK/session.jsonl" "$RHOME/.claude/projects/$SLUG/$ID.jsonl"

# SSH from this user to the remote user with a fresh key.
mkdir -p ~/.ssh && chmod 700 ~/.ssh
[ -f ~/.ssh/id_ed25519 ] || ssh-keygen -q -t ed25519 -N '' -f ~/.ssh/id_ed25519
sudo install -o "$REMOTE_USER" -m 0600 ~/.ssh/id_ed25519.pub "$RHOME/.ssh/authorized_keys"
sudo systemctl start ssh 2>/dev/null || sudo service ssh start

"$BIN" hosts add box "$REMOTE_USER@127.0.0.1"
"$BIN" trust box --yes
"$BIN" ls --host box --no-local --json > "$WORK/ls.json"
grep -q "\"id\": *\"$ID\"" "$WORK/ls.json" || { cat "$WORK/ls.json"; fail "ls did not list the session"; }
grep -q '"title": *"integration test"' "$WORK/ls.json" || fail "ls did not read the title"

TARGET="$WORK/here/proj"
mkdir -p "$TARGET"
"$BIN" pull "box:$ID" --to "$TARGET" --yes --json > "$WORK/pull.json" || { cat "$WORK/pull.json"; fail "pull failed"; }
TSLUG=$(cd "$TARGET" && pwd -P | sed 's/[^A-Za-z0-9]/-/g')
GOT="$CLAUDE_CONFIG_DIR/projects/$TSLUG/$ID.jsonl"
[ -f "$GOT" ] || { ls -R "$CLAUDE_CONFIG_DIR" >&2; fail "transcript not installed at $GOT"; }
grep -q "$TARGET/main.go" "$GOT" || fail "paths were not rewritten"
grep -q "$RHOME/proj" "$GOT" && fail "old paths remain"
grep -q '"type":"relocated"' "$GOT" || fail "no relocated record"

"$BIN" undo "$ID" --yes
[ -f "$GOT" ] && fail "undo left the transcript"
echo "integration test passed"
