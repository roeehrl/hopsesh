#!/bin/sh
# Maintainer's check of the real Claude Code cloud, opt-in and by hand (CI never runs it:
# it would need a subscription login stored as a secret). hopsesh brings a cloud session you
# name here with the real `claude --teleport`, in a new worktree of its repository, then
# checks the message count the teleport reports against the copy it wrote, and the branch it
# brought. Run it on a session whose repository is checked out here.
#
# The teleport is Claude Code's own interactive session: once it shows the conversation,
# leave it (/exit) and the script goes on. It starts no model turn unless you type one.
# Everything hopsesh makes is undone at the end (the worktree, its branches, and the copy,
# which goes into the scratch folder and is removed with it); hopsesh's own settings go to a
# scratch folder.
#
#   HOPSESH_PAID_CLOUD=1 BIN=bin/hopsesh scripts/cloud-smoke.sh <session id or its link> [<checkout>]
set -eu

[ "${HOPSESH_PAID_CLOUD:-}" = 1 ] || { echo "This teleports a real cloud session on your login; set HOPSESH_PAID_CLOUD=1 to run it." >&2; exit 2; }
[ $# -ge 1 ] || { echo "usage: HOPSESH_PAID_CLOUD=1 $0 <session id or its link> [<checkout>]" >&2; exit 2; }
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
cleanup() {
  if [ -n "$JOURNAL" ]; then "$BIN" undo "$JOURNAL" --yes --force >/dev/null 2>&1 || echo "note: could not undo $JOURNAL" >&2; fi
  rm -rf "$WORK"
}
trap cleanup EXIT

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
