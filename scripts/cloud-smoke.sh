#!/bin/sh
# Maintainer's checks of the real Claude Code cloud, opt-in and by hand (CI never runs them:
# they would need a subscription login stored as a secret). Two checks:
#
# bring <session id or link> [<checkout>] (the default): hopsesh brings a cloud session you
#   name here with the real `claude --teleport`, in a new worktree of its repository, then
#   checks the message count the teleport reports against the copy it wrote, and the branch
#   it brought. The teleport is Claude Code's own interactive session: once it shows the
#   conversation, leave it (/exit) and the script goes on. It starts no model turn unless
#   you type one.
#
# handoff <local session> [<checkout>]: hopsesh hands a local Claude Code session off to the
#   cloud with a codeword in its briefing (claude -p … --cloud … --output-format json, no
#   terminal needed), asks the cloud session to recall the codeword without reading files
#   (claude -p … --cloud <id>, no terminal needed), waits, brings it back with the teleport
#   (this one step needs your terminal: /exit once the conversation shows), and checks the
#   message count and that the recall is in the copy. It starts two model turns in the
#   cloud on your plan. Use a throwaway session in a checkout of a GitHub repository the
#   Claude GitHub App can reach; its work in progress goes up on a hopsesh/handoff/ branch.
#   HANDOFF_WAIT (seconds, default 240) is how long it waits for the cloud's turns.
#
# Everything hopsesh makes is undone at the end (worktrees, branches, the copy, the mark);
# a cloud session stays in Claude Code on the web (the script prints its link to archive
# it). hopsesh's own settings go to a scratch folder.
#
#   HOPSESH_PAID_CLOUD=1 BIN=bin/hopsesh scripts/cloud-smoke.sh [bring] <session id or its link> [<checkout>]
#   HOPSESH_PAID_CLOUD=1 BIN=bin/hopsesh scripts/cloud-smoke.sh handoff <local session id> [<checkout>]
set -eu

[ "${HOPSESH_PAID_CLOUD:-}" = 1 ] || { echo "This uses a real cloud session on your login; set HOPSESH_PAID_CLOUD=1 to run it." >&2; exit 2; }
MODE=bring
case "${1:-}" in bring|handoff) MODE=$1; shift ;; esac
[ $# -ge 1 ] || { echo "usage: HOPSESH_PAID_CLOUD=1 $0 [bring|handoff] <session> [<checkout>]" >&2; exit 2; }
SESSION=$1
CHECKOUT=${2:-$PWD}
BIN=${BIN:-$PWD/bin/hopsesh}
case "$BIN" in /*) ;; *) BIN=$PWD/$BIN ;; esac
command -v claude >/dev/null || { echo "claude is not installed" >&2; exit 2; }
command -v python3 >/dev/null || { echo "python3 is needed to read JSON" >&2; exit 2; }
git -C "$CHECKOUT" rev-parse --git-dir >/dev/null 2>&1 || { echo "$CHECKOUT is not a git checkout" >&2; exit 2; }

WORK=$(mktemp -d)
export HOPSESH_CONFIG_DIR="$WORK/config" HOPSESH_STATE_DIR="$WORK/state"
fail() { echo "FAIL: $*" >&2; exit 1; }
json() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); exec("for k in sys.argv[2].split(\".\"): d=d.get(k, \"\")"); print(d)' "$1" "$2"; }

JOURNAL=""
HANDOFF=""
CLOUDURL=""
cleanup() {
  if [ -n "$JOURNAL" ]; then "$BIN" undo "$JOURNAL" --yes --force >/dev/null 2>&1 || echo "note: could not undo $JOURNAL" >&2; fi
  if [ -n "$HANDOFF" ]; then "$BIN" undo "$HANDOFF" --yes --force >/dev/null 2>&1 || echo "note: could not undo the hand-off $HANDOFF" >&2; fi
  if [ -n "$CLOUDURL" ]; then echo "The cloud session stays in Claude Code on the web; archive it there: $CLOUDURL"; fi
  rm -rf "$WORK"
}
trap cleanup EXIT

if [ "$MODE" = handoff ]; then
  CODEWORD="hsm-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
  cat > "$WORK/note.md" <<NOTE
This is a connectivity check of hopsesh's hand-off: there is no open work, so do not change, commit or push anything.
The codeword for this check is $CODEWORD. Remember it; you will be asked for it.
NOTE

  echo "== 1. the login and the flags, read-only"
  "$BIN" clouds allow claude-cloud >/dev/null
  "$BIN" clouds test claude-cloud || fail "Claude Code cloud cannot be used with this login"

  echo "== 2. the plan (read-only)"
  "$BIN" plan "claude/$SESSION" --to claude-cloud --note-file "$WORK/note.md" --json > "$WORK/plan.json" || { cat "$WORK/plan.json"; fail "the plan has a blocker"; }
  grep -q "$CODEWORD" "$WORK/plan.json" || fail "the codeword is not in the briefing"

  echo "== 3. the hand-off (claude -p … --cloud … --output-format json; no terminal needed)"
  "$BIN" handoff "claude/$SESSION" --to claude-cloud --note-file "$WORK/note.md" --yes --json > "$WORK/handoff.json" || { cat "$WORK/handoff.json"; fail "handoff"; }
  HANDOFF=$(json "$WORK/handoff.json" result.journal)
  CLOUDID=$(json "$WORK/handoff.json" handoff.session)
  CLOUDURL=$(json "$WORK/handoff.json" handoff.url)
  echo "session: $CLOUDID ($CLOUDURL); branch: $(json "$WORK/handoff.json" handoff.branch)"
  case "$CLOUDID" in session_*) ;; *) fail "claude printed no session id hopsesh could read; update the module's notes on the reply's shape" ;; esac

  echo "== 4. the recall (claude -p … --cloud <id>; no terminal needed)"
  "$BIN" followup "claude-cloud:$CLOUDID" "Reply with only the codeword from the hopsesh note at the start of this session. Do not read, run or change anything." --yes \
    || fail "the follow-up was refused"
  WAIT=${HANDOFF_WAIT:-240}
  echo "waiting ${WAIT}s for the cloud's turns (HANDOFF_WAIT)…"
  sleep "$WAIT"

  echo "== 5. back with the teleport (THIS STEP NEEDS YOUR TERMINAL: /exit once the conversation shows)"
  "$BIN" pull "claude-cloud:$CLOUDID" --to "$CHECKOUT" --yes --run --json > "$WORK/pull.json" || { cat "$WORK/pull.json"; fail "pull"; }
  JOURNAL=$(json "$WORK/pull.json" result.journal)
  OUTCOME=$(json "$WORK/pull.json" brought.outcome)
  RESTORED=$(json "$WORK/pull.json" brought.restored)
  EXPECTED=$(json "$WORK/pull.json" brought.expected)
  KEY=$(json "$WORK/pull.json" brought.key)
  echo "outcome: $OUTCOME; restored $RESTORED of $EXPECTED"
  [ "$OUTCOME" = complete ] || fail "the copy is $OUTCOME: $(json "$WORK/pull.json" brought.message)"
  "$BIN" show "$KEY" --json > "$WORK/show.json" || fail "the copy is not listed"
  COPY=$(json "$WORK/show.json" session.path)
  python3 - "$COPY" "$CODEWORD" <<'PY' || fail "the cloud session did not recall the codeword (or the copy lacks its reply)"
import json, sys
path, word = sys.argv[1], sys.argv[2]
for line in open(path, encoding="utf-8"):
    try:
        r = json.loads(line)
    except ValueError:
        continue
    if r.get("type") != "assistant":
        continue
    for c in (r.get("message") or {}).get("content") or []:
        if isinstance(c, dict) and c.get("type") == "text" and word in c.get("text", ""):
            sys.exit(0)
sys.exit(1)
PY
  echo
  echo "hand-off smoke test passed: the codeword came back from the cloud. Record the Claude Code version (claude --version) in the module's Tested list"
  exit 0
fi

echo "== 1. the login and the flags, read-only"
"$BIN" clouds allow claude-cloud >/dev/null
"$BIN" clouds test claude-cloud || fail "Claude Code cloud cannot be used with this login"

echo "== 2. the plan"
"$BIN" plan "$SESSION" --to "$CHECKOUT" || fail "the plan has a blocker"

echo "== 3. the teleport (leave Claude Code with /exit once the conversation shows)"
"$BIN" pull "$SESSION" --to "$CHECKOUT" --yes --run --json > "$WORK/pull.json" || { cat "$WORK/pull.json"; fail "pull"; }
JOURNAL=$(json "$WORK/pull.json" result.journal)
OUTCOME=$(json "$WORK/pull.json" brought.outcome)
RESTORED=$(json "$WORK/pull.json" brought.restored)
EXPECTED=$(json "$WORK/pull.json" brought.expected)
STATED=$(json "$WORK/pull.json" brought.stated)
BRANCH=$(json "$WORK/pull.json" brought.branch)
echo "outcome: $OUTCOME; restored $RESTORED of $EXPECTED (stated by Claude Code: $STATED); branch: ${BRANCH:-none}"

[ "$OUTCOME" != waiting ] || fail "Claude Code wrote no copy hopsesh could find (did the teleport fail?)"
[ "$STATED" = True ] || echo "note: the copy has no teleported-from record, so the count is unchecked; update the module's notes"
[ "$OUTCOME" = complete ] || fail "the copy is $OUTCOME: $(json "$WORK/pull.json" brought.message)"
case "$BRANCH" in hopsesh/from/claude-cloud/*|"") ;; *) fail "the cloud's branch was not renamed: $BRANCH" ;; esac

echo
echo "cloud smoke test passed: record the Claude Code version (claude --version) in the module's Tested list"
