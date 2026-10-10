#!/bin/sh
# End-to-end test over a real SSH server, with "another machine" played by a second local
# user reached through sshd: list its Claude Code session and pull it here; continue it in
# Codex here; and push it back with hopsesh on that machine receiving it, then undo both
# sides at once. Run by CI on Linux and macOS; needs sudo.
set -eu
# shellcheck source=lib/testhost.sh
. "$(dirname "$0")/lib/testhost.sh"

BIN=${BIN:-$PWD/bin/hopsesh}
REMOTE_USER=hsremote
ID=0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a00
WORK=$(mktemp -d /tmp/hopsesh-it.XXXXXX)
export HOPSESH_CONFIG_DIR="$WORK/config" HOPSESH_STATE_DIR="$WORK/state" CLAUDE_CONFIG_DIR="$WORK/claude" CODEX_HOME="$WORK/codex"
export HOPSESH_MACHINE=here # the other "machine" is this host too; it keeps the host name
mkdir -p "$CODEX_HOME/sessions" "$CLAUDE_CONFIG_DIR" # both agents have run here once

fail() { echo "FAIL: $*" >&2; exit 1; }

# The "remote" machine: a user with one Claude Code session in ~/proj.
th_add_user "$REMOTE_USER"
th_clean_login_env
RHOME=$(th_home "$REMOTE_USER")
SLUG=$(printf '%s' "$RHOME/proj" | sed 's/[^A-Za-z0-9]/-/g')
sudo -u "$REMOTE_USER" -H sh -c "mkdir -p ~/proj ~/.claude/projects/$SLUG ~/.ssh && chmod 700 ~/.ssh"
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
th_start_sshd

"$BIN" hosts add box "$REMOTE_USER@127.0.0.1"
"$BIN" trust box --yes
"$BIN" ls --host box --no-local --json > "$WORK/ls.json"
grep -q "\"session\": *\"$ID\"" "$WORK/ls.json" || { cat "$WORK/ls.json"; fail "ls did not list the session"; }
grep -q '"title": *"integration test"' "$WORK/ls.json" || fail "ls did not read the title"

TARGET="$WORK/here/proj"
mkdir -p "$TARGET"
"$BIN" pull "box:$ID" --to "$TARGET" --yes --json > "$WORK/pull.json" || { cat "$WORK/pull.json"; fail "pull failed"; }
TSLUG=$(cd "$TARGET" && pwd -P | sed 's/[^A-Za-z0-9]/-/g')
LOCAL_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["plan"]["placement"]["key"]["session"])' "$WORK/pull.json")
if [ -z "$LOCAL_ID" ] || [ "$LOCAL_ID" = "$ID" ]; then
  fail "unverified account transfer did not create a fresh session"
fi
GOT="$CLAUDE_CONFIG_DIR/projects/$TSLUG/$LOCAL_ID.jsonl"
[ -f "$GOT" ] || { ls -R "$CLAUDE_CONFIG_DIR" >&2; fail "transcript not installed at $GOT"; }
grep -q "$TARGET/main.go" "$GOT" || fail "paths were not rewritten"
grep -q "$RHOME/proj" "$GOT" && fail "old paths remain"
grep -q '"kind": *"continue"' "$WORK/pull.json" || fail "unverified account transfer did not use portable history"

"$BIN" undo "$LOCAL_ID" --yes
[ -f "$GOT" ] && fail "undo left the transcript"

# Continue it in Codex here.
"$BIN" pull "box:$ID" --in codex --to "$TARGET" --yes --json > "$WORK/codex.json" || { cat "$WORK/codex.json"; fail "continue in Codex failed"; }
grep -q '"kind": *"continue"' "$WORK/codex.json" || fail "not a continuation"
ROLLOUT=$(find "$CODEX_HOME/sessions" -name 'rollout-*.jsonl' | head -n 1)
[ -n "$ROLLOUT" ] || fail "no Codex rollout written"
grep -q "fix the build in $TARGET/main.go" "$ROLLOUT" || fail "the conversation did not arrive in Codex with this machine's paths"
"$BIN" undo --yes
[ -f "$ROLLOUT" ] && fail "undo left the Codex rollout"

# Push it back: hopsesh on box receives sessions.
sudo mkdir -p /usr/local/bin && sudo install -m 0755 "$BIN" /usr/local/bin/hopsesh
# The remote user's own environment: the caller's XDG_* paths must not leak in.
as_remote() { sudo -u "$REMOTE_USER" -H env -u XDG_CONFIG_HOME -u XDG_STATE_HOME -u XDG_CACHE_HOME -u XDG_DATA_HOME HOME="$RHOME" "$@"; }
as_remote /usr/local/bin/hopsesh receive on >/dev/null
"$BIN" pull "box:$ID" --to "$TARGET" --yes --json > "$WORK/pull2.json" || { cat "$WORK/pull2.json"; fail "second pull failed"; }
LOCAL_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["plan"]["placement"]["key"]["session"])' "$WORK/pull2.json")
GOT="$CLAUDE_CONFIG_DIR/projects/$TSLUG/$LOCAL_ID.jsonl"
[ -f "$GOT" ] || fail "second pull did not install its reported destination"
RFILE="$RHOME/.claude/projects/$SLUG/$ID.jsonl"
sudo grep -q '↪' "$RFILE" && fail "box's copy was relabelled by the pull: titles must stay as they were"
# Continue the native chain before returning. A workless visit only syncs receipts.
python3 - "$GOT" "$LOCAL_ID" <<'PYTHON'
import json, sys
path, session = sys.argv[1:]
with open(path) as f:
    records = [json.loads(line) for line in f if line.strip()]
parent = next(r["uuid"] for r in reversed(records) if r.get("type") in ("user", "assistant") and r.get("uuid"))
with open(path, "a") as f:
    for record in [
        {"type": "user", "uuid": "return1", "parentUuid": parent, "sessionId": session, "timestamp": "2026-10-01T10:01:00Z", "message": {"role": "user", "content": "SSH return sentinel"}},
        {"type": "last-prompt", "leafUuid": "return1", "sessionId": session},
    ]:
        f.write(json.dumps(record) + "\n")
PYTHON
"$BIN" push "$LOCAL_ID" box --to "$RHOME/proj" --yes --json > "$WORK/push.json" || {
  cat "$WORK/push.json"
  # shellcheck disable=SC2016 # expands on box
  ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "$REMOTE_USER@127.0.0.1" 'echo "box HOME=$HOME"; env | grep "^XDG_" || true' >&2
  fail "push failed"
}
JOURNAL=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["result"]["journal"])' "$WORK/push.json")
[ -n "$JOURNAL" ] || { cat "$WORK/push.json"; fail "push printed no journal"; }
RETURN_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["plan"]["placement"]["key"]["session"])' "$WORK/push.json")
if [ -z "$RETURN_ID" ] || [ "$RETURN_ID" = "$ID" ]; then
  fail "unverified return did not preserve the original session"
fi
RETURN_FILE="$RHOME/.claude/projects/$SLUG/$RETURN_ID.jsonl"
sudo test -f "$RETURN_FILE" || fail "box did not install the reported return destination"
sudo cat "$RETURN_FILE" | grep '"type":"custom-title"' | tail -n 1 | grep -q '↪' && fail "the returned copy carries a title label"
sudo grep -q 'SSH return sentinel' "$RETURN_FILE" || fail "box did not receive this machine's new work"
sudo grep -q 'SSH return sentinel' "$RFILE" && fail "unverified return changed the original conversation"
grep -q '↪' "$GOT" && fail "the copy here was relabelled by the push"
"$BIN" undo "$JOURNAL" --yes
sudo test -f "$RETURN_FILE" && fail "undo left the returned portable copy"
grep -q '↪' "$GOT" && fail "the copy here carries a title label after undo"
sudo grep -q '↪' "$RFILE" && fail "box's own copy carries a title label after undo"

# A machine that does not receive refuses.
as_remote /usr/local/bin/hopsesh receive off >/dev/null
if "$BIN" push "$LOCAL_ID" box --to "$RHOME/proj" --yes --json > "$WORK/refused.json" 2>&1; then fail "push to a machine that does not receive"; fi
grep -q 'does not receive sessions' "$WORK/refused.json" || { cat "$WORK/refused.json"; fail "the refusal does not say why"; }
echo "integration test passed"
