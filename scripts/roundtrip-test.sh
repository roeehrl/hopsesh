#!/bin/sh
# Round trip over real SSH between two throwaway users of this machine, each playing a
# machine:
#   "box"  = user hsremote (the session starts there)
#   "back" = user hsback   (runs hopsesh first)
# box → back: code comes along straight from box (never pushed), box's copy gets marked.
# back → box: the marked copy on box is replaced by the newer one, back's copy gets marked.
# Then both copies change and a move is refused until --keep-both.
# Run by CI on Linux; needs sudo, sshd and git. The account running it is not changed.
set -eu

BIN=${BIN:-$PWD/bin/hopsesh}
case "$BIN" in /*) ;; *) BIN=$PWD/$BIN ;; esac
cd / # the test users cannot read the caller's working directory (git checks it)
B=hsremote # box
A=hsback   # back
ID=7c1d2e3f-4a5b-4c6d-8e7f-90a1b2c3d4e5
WORK=$(mktemp -d)
chmod 755 "$WORK"
fail() { echo "FAIL: $*" >&2; exit 1; }
say() { printf '\n== %s\n' "$*"; }

sudo install -m 0755 "$BIN" /usr/local/bin/hopsesh-rt
HS=/usr/local/bin/hopsesh-rt
for u in "$A" "$B"; do sudo useradd -m -s /bin/sh "$u" 2>/dev/null || true; done
AHOME=$(getent passwd "$A" | cut -d: -f6)
BHOME=$(getent passwd "$B" | cut -d: -f6)
# Run as a test user with a clean environment (the caller's XDG_* paths must not leak).
user_env() {
  u=$1 h=$2
  shift 2
  sudo -u "$u" -H env -u XDG_CONFIG_HOME -u XDG_STATE_HOME -u XDG_CACHE_HOME -u XDG_DATA_HOME HOME="$h" "$@"
}
as_a() { user_env "$A" "$AHOME" HOPSESH_CONFIG_DIR="$AHOME/.hscfg" HOPSESH_STATE_DIR="$AHOME/.hsstate" "$@"; }
as_b() { user_env "$B" "$BHOME" HOPSESH_CONFIG_DIR="$BHOME/.hscfg" HOPSESH_STATE_DIR="$BHOME/.hsstate" "$@"; }
sh_a() { user_env "$A" "$AHOME" sh -c "$1"; }
sh_b() { user_env "$B" "$BHOME" sh -c "$1"; }

# Two-way SSH between the users.
for u in "$A" "$B"; do
  sudo -u "$u" -H sh -c 'umask 077; mkdir -p ~/.ssh && chmod 700 ~/.ssh && { [ -f ~/.ssh/id_ed25519 ] || ssh-keygen -q -t ed25519 -N "" -f ~/.ssh/id_ed25519; } && chmod 600 ~/.ssh/id_ed25519'
done
sudo cat "$AHOME/.ssh/id_ed25519.pub" | sudo -u "$B" -H sh -c 'cat >> ~/.ssh/authorized_keys; chmod 600 ~/.ssh/authorized_keys'
sudo cat "$BHOME/.ssh/id_ed25519.pub" | sudo -u "$A" -H sh -c 'cat >> ~/.ssh/authorized_keys; chmod 600 ~/.ssh/authorized_keys'
sudo systemctl start ssh 2>/dev/null || sudo service ssh start 2>/dev/null || {
  sudo mkdir -p /run/sshd && sudo ssh-keygen -A >/dev/null && sudo /usr/sbin/sshd
}

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

say "box → back"
as_a "$HS" hosts add box "$B@127.0.0.1" >/dev/null
as_a "$HS" trust box --yes >/dev/null
as_a "$HS" pull "box:$ID" --yes --json > "$WORK/pull1.json" || { cat "$WORK/pull1.json"; fail "first move"; }
grep -q '"fromSource": *true' "$WORK/pull1.json" || { cat "$WORK/pull1.json"; fail "the unpushed commit should come straight from box"; }
grep -q '"state": *"fast-forwarded"' "$WORK/pull1.json" || fail "back's checkout should be fast-forwarded"
[ "$(sh_a 'git -C ~/git/rt rev-parse HEAD')" = "$TWO" ] || fail "back's checkout is not at box's commit"
grep -q '"mark": *"done"' "$WORK/pull1.json" || fail "box's copy should be marked"
sudo tail -n 1 "$BFILE" | grep -q 'moved to' || fail "box's transcript has no moved mark"
SLUG_A=$(sh_a 'cd ~/git/rt && pwd -P' | sed 's/[^A-Za-z0-9]/-/g')
AFILE="$AHOME/.claude/projects/$SLUG_A/$ID.jsonl"
sudo test -f "$AFILE" || fail "no copy on back at $AFILE"
as_a "$HS" ls --json --no-git > "$WORK/ls.json"
grep -q '"movedTo": *"' "$WORK/ls.json" || fail "the listing should show box's copy as moved"

say "work continues on back"
sh_a "echo '{\"type\":\"user\",\"uuid\":\"u2\",\"parentUuid\":\"u1\",\"sessionId\":\"$ID\",\"cwd\":\"$AHOME/git/rt\",\"timestamp\":\"2026-10-02T12:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"continued on back\"}}' >> '$AFILE'"
sh_a 'cd ~/git/rt && echo three > a.txt && git commit -qam three'
THREE=$(sh_a 'git -C ~/git/rt rev-parse HEAD')

say "back → box (hop back)"
as_b "$HS" hosts add back "$A@127.0.0.1" >/dev/null
as_b "$HS" trust back --yes >/dev/null
as_b "$HS" pull "back:$ID" --yes --json > "$WORK/pull2.json" 2>&1 || { cat "$WORK/pull2.json"; fail "hop back"; }
grep -q '"setAside"' "$WORK/pull2.json" || fail "box's stale copy should be set aside"
sudo grep -q "continued on back" "$BFILE" || fail "box should now have the newer copy"
if sudo grep -q 'moved to' "$BFILE"; then fail "the copy that came home must not carry a moved mark"; fi
[ "$(sh_b 'git -C ~/rt rev-parse HEAD')" = "$THREE" ] || fail "box's checkout should be at back's commit"
sudo tail -n 1 "$AFILE" | grep -q 'moved to' || fail "back's copy should now be marked moved"

say "both change: a conflict"
sh_a "echo '{\"type\":\"user\",\"uuid\":\"u3\",\"parentUuid\":\"u2\",\"sessionId\":\"$ID\",\"cwd\":\"$AHOME/git/rt\",\"timestamp\":\"2026-10-02T13:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"kept going on back after the move\"}}' >> '$AFILE'"
sh_b "echo '{\"type\":\"user\",\"uuid\":\"u4\",\"parentUuid\":\"u2\",\"sessionId\":\"$ID\",\"cwd\":\"$BHOME/rt\",\"timestamp\":\"2026-10-02T14:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"and on box\"}}' >> '$BFILE'"
if as_a "$HS" pull "box:$ID" --yes --json > "$WORK/pull3.json" 2>&1; then fail "a move over a changed copy must be refused"; fi
grep -q "keep-both" "$WORK/pull3.json" || { cat "$WORK/pull3.json"; fail "the refusal should offer --keep-both"; }
as_a "$HS" pull "box:$ID" --yes --json --keep-both > "$WORK/pull4.json" || { cat "$WORK/pull4.json"; fail "keep both"; }
sudo grep -q "kept going on back after the move" "$AFILE" || fail "keep-both must not touch the copy here"
grep -q '"originalId": *"'"$ID"'"' "$WORK/pull4.json" || fail "the incoming copy should get a new id"

echo
echo "round trip test passed"
