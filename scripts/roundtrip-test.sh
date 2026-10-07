#!/bin/sh
# Round trip over real SSH between two throwaway users of this machine, each playing a
# machine:
#   "box"  = user hsremote (the session starts there)
#   "back" = user hsback   (runs hopsesh first)
# box → back: code comes along straight from box (never pushed), box's copy gets marked.
# back → box: portable history returns as a fresh copy, back is marked.
# Without verified login identity each account boundary uses a fresh native ID; originals remain.
# Then both copies change and a move is refused until --keep-both.
# Run by CI on Linux and macOS; needs sudo, sshd and git. The account running it is not changed.
set -eu
# shellcheck source=lib/testhost.sh
. "$(dirname "$0")/lib/testhost.sh"

BIN=${BIN:-$PWD/bin/hopsesh}
case "$BIN" in /*) ;; *) BIN=$PWD/$BIN ;; esac
cd / # the test users cannot read the caller's working directory (git checks it)
B=hsremote # box
A=hsback   # back
ID=7c1d2e3f-4a5b-4c6d-8e7f-90a1b2c3d4e5
WORK=$(mktemp -d /tmp/hopsesh-rt.XXXXXX)
chmod 755 "$WORK"
fail() { echo "FAIL: $*" >&2; exit 1; }
say() { printf '\n== %s\n' "$*"; }

sudo mkdir -p /usr/local/bin && sudo install -m 0755 "$BIN" /usr/local/bin/hopsesh-rt
HS=/usr/local/bin/hopsesh-rt
for u in "$A" "$B"; do th_add_user "$u"; done
th_clean_login_env
AHOME=$(th_home "$A")
BHOME=$(th_home "$B")
# Run as a test user with a clean environment (the caller's XDG_* paths must not leak).
user_env() {
  u=$1 h=$2
  shift 2
  sudo -u "$u" -H env -u HOPSESH_CONFIG_DIR -u XDG_CONFIG_HOME -u XDG_STATE_HOME -u XDG_CACHE_HOME -u XDG_DATA_HOME HOME="$h" "$@"
}
as_a() { user_env "$A" "$AHOME" HOPSESH_MACHINE=back HOPSESH_STATE_DIR="$AHOME/.hsstate" "$@"; }
as_b() { user_env "$B" "$BHOME" HOPSESH_MACHINE=box HOPSESH_STATE_DIR="$BHOME/.hsstate" "$@"; }
sh_a() { user_env "$A" "$AHOME" sh -c "$1"; }
sh_b() { user_env "$B" "$BHOME" sh -c "$1"; }

# Two-way SSH between the users.
for u in "$A" "$B"; do
  sudo -u "$u" -H sh -c 'umask 077; mkdir -p ~/.ssh && chmod 700 ~/.ssh && { [ -f ~/.ssh/id_ed25519 ] || ssh-keygen -q -t ed25519 -N "" -f ~/.ssh/id_ed25519; } && chmod 600 ~/.ssh/id_ed25519'
done
sudo cat "$AHOME/.ssh/id_ed25519.pub" | sudo -u "$B" -H sh -c 'cat >> ~/.ssh/authorized_keys; chmod 600 ~/.ssh/authorized_keys'
sudo cat "$BHOME/.ssh/id_ed25519.pub" | sudo -u "$A" -H sh -c 'cat >> ~/.ssh/authorized_keys; chmod 600 ~/.ssh/authorized_keys'
th_start_sshd

# One repository, an unreachable "origin" (so commits can only come from the other machine).
say "setting up the repository on both machines"
ORIGIN=https://example.invalid/acme/rt.git
NOKH="ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes"
sh_b "git config --global user.email b@example.com && git config --global user.name b &&
  git init -q -b main ~/rt && cd ~/rt && echo one > a.txt && git add . && git commit -qm one && git remote add origin $ORIGIN"
sh_a "git config --global user.email a@example.com && git config --global user.name a && mkdir -p ~/git &&
  GIT_SSH_COMMAND='$NOKH' git clone -q $B@127.0.0.1:rt ~/git/rt && git -C ~/git/rt remote set-url origin $ORIGIN"
# Work on box that was never pushed.
sh_b 'cd ~/rt && echo two > a.txt && git commit -qam two'
TWO=$(sh_b 'git -C ~/rt rev-parse HEAD')

# The session on box.
SLUG_B=$(printf '%s' "$BHOME/rt" | sed 's/[^A-Za-z0-9]/-/g')
cat > "$WORK/s.jsonl" <<JSONL
{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"$ID","cwd":"$BHOME/rt","version":"2.1.284","timestamp":"2026-10-02T08:00:00Z","message":{"role":"user","content":"edit $BHOME/rt/a.txt"}}
{"type":"custom-title","customTitle":"round trip","sessionId":"$ID"}
JSONL
sh_b "mkdir -p ~/.claude/projects/$SLUG_B"
sudo install -o "$B" -m 0600 "$WORK/s.jsonl" "$BHOME/.claude/projects/$SLUG_B/$ID.jsonl"
BFILE="$BHOME/.claude/projects/$SLUG_B/$ID.jsonl"
sh_a "mkdir -p ~/.claude" # Claude Code has run on back once

say "box → back"
as_a "$HS" hosts add box "$B@127.0.0.1" >/dev/null
as_a "$HS" trust box --yes >/dev/null
as_a "$HS" pull "box:$ID" --yes --json > "$WORK/pull1.json" || { cat "$WORK/pull1.json"; fail "first move"; }
grep -q '"fromSource": *true' "$WORK/pull1.json" || { cat "$WORK/pull1.json"; fail "the unpushed commit should come straight from box"; }
grep -q '"state": *"fast-forwarded"' "$WORK/pull1.json" || fail "back's checkout should be fast-forwarded"
[ "$(sh_a 'git -C ~/git/rt rev-parse HEAD')" = "$TWO" ] || fail "back's checkout is not at box's commit"
grep -q '"mark": *"done"' "$WORK/pull1.json" || fail "box's copy should be marked"
sudo tail -n 1 "$BFILE" | grep -q 'prepared in Claude Code on back' || fail "box's transcript has no prepared mark"
SLUG_A=$(sh_a 'cd ~/git/rt && pwd -P' | sed 's/[^A-Za-z0-9]/-/g')
AID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["plan"]["placement"]["key"]["session"])' "$WORK/pull1.json")
if [ -z "$AID" ] || [ "$AID" = "$ID" ]; then
  fail "unverified account transfer did not create a fresh session"
fi
AFILE="$AHOME/.claude/projects/$SLUG_A/$AID.jsonl"
sudo test -f "$AFILE" || fail "no copy on back at $AFILE"
as_a "$HS" ls --json --no-git > "$WORK/ls.json"
grep -q '"kind": *"prepared"' "$WORK/ls.json" || fail "the listing should show box's copy as prepared"

say "work continues on back"
ALEAF=$(sudo cat "$AFILE" | python3 -c 'import json,sys; records=[json.loads(l) for l in sys.stdin]; print([r["uuid"] for r in records if r.get("type") in ("user","assistant")][-1])')
sh_a "echo '{\"type\":\"user\",\"uuid\":\"u2\",\"parentUuid\":\"$ALEAF\",\"sessionId\":\"$AID\",\"cwd\":\"$AHOME/git/rt\",\"timestamp\":\"2026-10-02T12:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"continued on back\"}}' >> '$AFILE'"
sh_a "echo '{\"type\":\"last-prompt\",\"leafUuid\":\"u2\",\"sessionId\":\"$AID\"}' >> '$AFILE'"
sh_a 'cd ~/git/rt && echo three > a.txt && git commit -qam three'
THREE=$(sh_a 'git -C ~/git/rt rev-parse HEAD')

say "back → box (hop back)"
as_b "$HS" hosts add back "$A@127.0.0.1" >/dev/null
as_b "$HS" trust back --yes >/dev/null
as_b "$HS" pull "back:$AID" --yes --json > "$WORK/pull2.json" 2>&1 || { cat "$WORK/pull2.json"; fail "hop back"; }
RETURN_ID=$(python3 -c 'import json,sys; p=json.load(open(sys.argv[1]))["plan"]; assert p["kind"]=="continue" and not p.get("conflict"); print(p["placement"]["key"]["session"])' "$WORK/pull2.json")
if [ -z "$RETURN_ID" ] || [ "$RETURN_ID" = "$ID" ]; then
  fail "unverified return did not use a fresh ID"
fi
sudo grep -q "continued on back" "$BFILE" && fail "return changed the preserved original"
BFILE="$BHOME/.claude/projects/$SLUG_B/$RETURN_ID.jsonl"
sudo test -f "$BFILE" || fail "no reported return copy on box"
sudo grep -q "continued on back" "$BFILE" || fail "box should now have the newer copy"
if sudo cat "$BFILE" | grep '"type":"custom-title"' | tail -n 1 | grep -Eq 'moved to|continued in|prepared in'; then fail "the copy that came home must not carry a moved mark"; fi
[ "$(sh_b 'git -C ~/rt rev-parse HEAD')" = "$THREE" ] || fail "box's checkout should be at back's commit"
sudo tail -n 1 "$AFILE" | grep -q 'prepared in Claude Code on box' || fail "back's copy should now be marked prepared"

say "both change: a conflict"
# Follow the active native leaf on the returned copy.
BLEAF=$(sudo cat "$BFILE" | python3 -c 'import json,sys; records=[json.loads(l) for l in sys.stdin]; leaves=[r["leafUuid"] for r in records if r.get("type")=="last-prompt"]; print(leaves[-1] if leaves else [r["uuid"] for r in records if r.get("type") in ("user","assistant")][-1])')
sh_a "echo '{\"type\":\"user\",\"uuid\":\"u3\",\"parentUuid\":\"u2\",\"sessionId\":\"$AID\",\"cwd\":\"$AHOME/git/rt\",\"timestamp\":\"2026-10-02T13:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"kept going on back after the move\"}}' >> '$AFILE'"
sh_a "echo '{\"type\":\"last-prompt\",\"leafUuid\":\"u3\",\"sessionId\":\"$AID\"}' >> '$AFILE'"
sh_b "echo '{\"type\":\"user\",\"uuid\":\"u4\",\"parentUuid\":\"$BLEAF\",\"sessionId\":\"$RETURN_ID\",\"cwd\":\"$BHOME/rt\",\"timestamp\":\"2026-10-02T14:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"and on box\"}}' >> '$BFILE'"
sh_b "echo '{\"type\":\"last-prompt\",\"leafUuid\":\"u4\",\"sessionId\":\"$RETURN_ID\"}' >> '$BFILE'"
if as_a "$HS" pull "box:$RETURN_ID" --yes --json > "$WORK/pull3.json" 2>&1; then fail "a move over a changed copy must be refused"; fi
grep -q "keep-both" "$WORK/pull3.json" || { cat "$WORK/pull3.json"; fail "the refusal should offer --keep-both"; }
as_a "$HS" pull "box:$RETURN_ID" --yes --json --keep-both > "$WORK/pull4.json" || { cat "$WORK/pull4.json"; fail "keep both"; }
sudo grep -q "kept going on back after the move" "$AFILE" || fail "keep-both must not touch the copy here"
grep -q '"sourceId": *"'"$RETURN_ID"'"' "$WORK/pull4.json" || fail "the incoming copy should record where it came from"
python3 -c 'import json,sys; sys.exit(json.load(open(sys.argv[1]))["plan"]["placement"]["key"]["session"] == sys.argv[2])' "$WORK/pull4.json" "$AID" \
  || fail "the incoming copy should get a new id"

echo
echo "round trip test passed"
