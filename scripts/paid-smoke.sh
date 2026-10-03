#!/bin/sh
# Paid smoke test, opt-in: a real round trip through the installed, logged-in agents.
#   1. Claude Code reads a codeword in a scratch project (claude -p);
#   2. hopsesh continues that session in Codex, and Codex answers from the history;
#   3. hopsesh continues the Codex thread back in Claude Code (only the new work is added to
#      the original, whose signed reasoning must stay valid), and Claude answers.
# It makes three short model calls on your own logins, and uses your real agent folders for
# a few minutes: everything it creates is removed at the end (hopsesh undo, then the one
# session Claude Code itself created). hopsesh's own settings go to a scratch folder.
#
#   HOPSESH_PAID_SMOKE=1 BIN=bin/hopsesh scripts/paid-smoke.sh
set -eu

[ "${HOPSESH_PAID_SMOKE:-}" = 1 ] || { echo "This calls the models on your logins; set HOPSESH_PAID_SMOKE=1 to run it." >&2; exit 2; }
BIN=${BIN:-$PWD/bin/hopsesh}
case "$BIN" in /*) ;; *) BIN=$PWD/$BIN ;; esac
command -v claude >/dev/null || { echo "claude is not installed" >&2; exit 2; }
command -v codex >/dev/null || { echo "codex is not installed" >&2; exit 2; }
command -v python3 >/dev/null || { echo "python3 is needed to read JSON" >&2; exit 2; }

WORK=$(mktemp -d)
PROJ="$WORK/smoke-$(date +%s)"
export HOPSESH_CONFIG_DIR="$WORK/config" HOPSESH_STATE_DIR="$WORK/state"
fail() { echo "FAIL: $*" >&2; exit 1; }
json() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); exec("for k in sys.argv[2].split(\".\"): d=d[k]"); print(d)' "$1" "$2"; }

CLAUDE_FILE=""
JOURNALS=""
cleanup() {
  for j in $JOURNALS; do "$BIN" undo "$j" --yes >/dev/null 2>&1 || echo "note: could not undo $j" >&2; done
  if [ -n "$CLAUDE_FILE" ]; then
    rm -f "$CLAUDE_FILE" "${CLAUDE_FILE%.jsonl}.hopsesh.json"
    rmdir "$(dirname "$CLAUDE_FILE")/memory" "$(dirname "$CLAUDE_FILE")" 2>/dev/null || true # only if empty
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

mkdir -p "$PROJ" && cd "$PROJ"
git init -q && echo "The codeword is PLUM-7." > notes.txt && git add . && git -c user.email=s@example.com -c user.name=smoke commit -qm notes

echo "== 1. Claude Code reads the codeword"
claude -p "Read notes.txt and remember the codeword. Reply with just OK." --output-format json --allowedTools Read > "$WORK/c1.json" \
  || { cat "$WORK/c1.json"; fail "claude -p"; }
SID=$(json "$WORK/c1.json" session_id)
SLUG=$(pwd -P | sed 's/[^A-Za-z0-9]/-/g')
CLAUDE_FILE="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/$SLUG/$SID.jsonl"
[ -f "$CLAUDE_FILE" ] || fail "no Claude transcript at $CLAUDE_FILE"

echo "== 2. continue it in Codex"
"$BIN" pull "claude/$SID" --in codex --to "$PROJ" --yes --json > "$WORK/p1.json" || { cat "$WORK/p1.json"; fail "pull --in codex"; }
JOURNALS="$(json "$WORK/p1.json" result.journal) $JOURNALS"
TID=$(json "$WORK/p1.json" plan.placement.key.session)
codex exec resume "$TID" "What is the codeword? Reply with just the codeword." > "$WORK/x1.txt" 2>&1 \
  || { cat "$WORK/x1.txt"; fail "codex exec resume"; }
grep -q "PLUM-7" "$WORK/x1.txt" || { cat "$WORK/x1.txt"; fail "Codex did not answer from the carried history"; }

echo "== 3. back to Claude Code"
"$BIN" pull "codex/$TID" --in claude --to "$PROJ" --yes --json > "$WORK/p2.json" || { cat "$WORK/p2.json"; fail "pull --in claude"; }
JOURNALS="$(json "$WORK/p2.json" result.journal) $JOURNALS"
[ "$(json "$WORK/p2.json" plan.continue.relation)" = append ] || fail "the return should add only the new work to the original"
claude -p --resume "$SID" "What codeword did Codex just give? Reply with it in lowercase." > "$WORK/c2.txt" 2>&1 \
  || { cat "$WORK/c2.txt"; fail "claude --resume after the round trip (signature errors show here)"; }
grep -q "plum-7" "$WORK/c2.txt" || { cat "$WORK/c2.txt"; fail "Claude Code did not see Codex's answer"; }
grep -qi "signature" "$WORK/c2.txt" && fail "a reasoning signature was rejected"

echo
echo "paid smoke test passed"
