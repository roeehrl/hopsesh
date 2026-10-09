#!/bin/sh
# Maintainer's checks of the real Claude Code and Codex clouds, opt-in and by hand (CI never
# runs them: they would need a subscription login stored as a secret). Three checks:
#
# bring <session id or link> [<checkout>] (the default): hopsesh brings a cloud session you
#   name here with the real `claude --teleport`, in a new worktree of its repository, then
#   checks the copy it wrote and the branch it brought. The teleport is Claude Code's own
#   interactive session, and Claude Code 2.1.289 saves its copy only once you send a message
#   in it: send one (even "ok"), then leave it (/exit) and the script goes on. That message
#   starts one model turn, on this machine.
#
# handoff <local session> [<checkout>]: hopsesh hands a local Claude Code session off to the
#   cloud with a codeword in its briefing, which asks the cloud session to reply with the
#   codeword only. Claude Code starts a cloud session only in a terminal, so hopsesh runs
#   `claude --cloud "<briefing>"` in this one, in its hand-off folder for the repository
#   (here in the script's scratch folder, so Claude Code asks every run whether you trust
#   that folder; hopsesh's own folder is asked about once per repository). Answer it
#   yourself (hopsesh never does); the script goes on once claude --cloud exits, and
#   checks the session id hopsesh read from what it printed. It waits, brings the session
#   back with the teleport (in this terminal too: once the conversation shows, send a message
#   so Claude Code saves its copy, then leave it with /exit and the script goes on), and
#   checks that the copy begins with the briefing hopsesh sent and holds the codeword reply. It starts one model turn in the cloud on your plan (there is no
#   follow-up: hopsesh has not integrated and qualified that command). Use a throwaway
#   session in a checkout of a GitHub repository the Claude GitHub App can reach; its work
#   in progress goes up on a hopsesh/handoff/ branch. HANDOFF_WAIT (seconds, default 240) is
#   how long it waits for the cloud's turn.
#
# codex <environment> [<checkout>]: starts a tiny Codex cloud task in the environment you
#   name (its id or name, as `codex cloud` shows it) on the checkout's current branch, which
#   must be on GitHub and in that environment's repository (codex cloud exec --env --branch;
#   the task writes one file, hopsesh-smoke-<codeword>.txt), waits for it (codex cloud
#   status, every 15 seconds, up to CODEX_WAIT seconds, default 900), then brings it back
#   with hopsesh: the diff committed on hopsesh/from/codex-cloud/<id> in a new worktree and
#   the task written as a Codex session; it checks the file and the session, and undoes
#   them. It starts one task on your plan. It also saves what codex printed (list --json,
#   status, diff) under the scratch folder and prints it, to check the shapes the module
#   reads from source against the real ones.
#
# Everything hopsesh makes is undone at the end (worktrees, branches, the copy, the mark, and
# the hand-off folder, which lives in the scratch folder);
# a cloud session or task stays in the vendor's list (the script prints its link to archive
# it). hopsesh's own settings go to a scratch folder.
#
#   HOPSESH_PAID_CLOUD=1 BIN=bin/hopsesh scripts/cloud-smoke.sh [bring] <session id or its link> [<checkout>]
#   HOPSESH_PAID_CLOUD=1 BIN=bin/hopsesh scripts/cloud-smoke.sh handoff <local session id> [<checkout>]
#   HOPSESH_PAID_CLOUD=1 BIN=bin/hopsesh scripts/cloud-smoke.sh codex <environment> [<checkout>]
set -eu

[ "${HOPSESH_PAID_CLOUD:-}" = 1 ] || { echo "This uses a real cloud session on your login; set HOPSESH_PAID_CLOUD=1 to run it." >&2; exit 2; }
MODE=bring
case "${1:-}" in bring|handoff|codex) MODE=$1; shift ;; esac
[ $# -ge 1 ] || { echo "usage: HOPSESH_PAID_CLOUD=1 $0 [bring|handoff] <session> [<checkout>] | codex <environment> [<checkout>]" >&2; exit 2; }
SESSION=$1
CHECKOUT=${2:-$PWD}
BIN=${BIN:-$PWD/bin/hopsesh}
case "$BIN" in /*) ;; *) BIN=$PWD/$BIN ;; esac
if [ "$MODE" = codex ]; then
  command -v codex >/dev/null || { echo "codex is not installed" >&2; exit 2; }
else
  command -v claude >/dev/null || { echo "claude is not installed" >&2; exit 2; }
fi
command -v python3 >/dev/null || { echo "python3 is needed to read JSON" >&2; exit 2; }
git -C "$CHECKOUT" rev-parse --git-dir >/dev/null 2>&1 || { echo "$CHECKOUT is not a git checkout" >&2; exit 2; }

WORK=$(mktemp -d)
export HOPSESH_CONFIG_DIR="$WORK/config" HOPSESH_STATE_DIR="$WORK/state"
fail() { echo "FAIL: $*" >&2; exit 1; }
# json prints a field of a JSON file ("" when the file or the field is missing).
json() { python3 -c 'import json,sys
try:
    d=json.load(open(sys.argv[1]))
except (OSError, ValueError):
    d={}
for k in sys.argv[2].split("."):
    d=d.get(k, "") if isinstance(d, dict) else ""
print(d)' "$1" "$2"; }

JOURNAL=""
HANDOFF=""
CLOUDURL=""
cleanup() {
  if [ -n "$JOURNAL" ]; then "$BIN" undo "$JOURNAL" --yes --force >/dev/null 2>&1 || echo "note: could not undo $JOURNAL" >&2; fi
  if [ -n "$HANDOFF" ]; then "$BIN" undo "$HANDOFF" --yes --force >/dev/null 2>&1 || echo "note: could not undo the hand-off $HANDOFF" >&2; fi
  if [ -n "$CLOUDURL" ]; then echo "The cloud session or task stays in the vendor's list; archive it there: $CLOUDURL"; fi
  rm -rf "$WORK"
  # The hand-off folder was a worktree of the checkout, in the scratch folder just removed.
  git -C "$CHECKOUT" worktree prune 2>/dev/null || true
}
trap cleanup EXIT

if [ "$MODE" = codex ]; then
  ENVIRONMENT=$SESSION
  BRANCH=$(git -C "$CHECKOUT" rev-parse --abbrev-ref HEAD)
  [ "$BRANCH" != HEAD ] || fail "$CHECKOUT is not on a branch"
  [ -n "$(git -C "$CHECKOUT" ls-remote --heads origin "$BRANCH")" ] || fail "branch $BRANCH is not on origin; push it first"
  CODEWORD="hsm-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"

  echo "== 1. the login, the commands and a listing, read-only"
  "$BIN" clouds allow codex-cloud >/dev/null
  "$BIN" clouds test codex-cloud || fail "Codex cloud cannot be used with this login (codex login status must say ChatGPT)"
  (cd "$CHECKOUT" && codex cloud list --limit 5 --json) > "$WORK/list.json" 2>"$WORK/list.err" || fail "codex cloud list: $(cat "$WORK/list.err")"

  echo "== 2. a tiny task in $ENVIRONMENT on $BRANCH (codex cloud exec; one task on your plan)"
  (cd "$CHECKOUT" && codex cloud exec --env "$ENVIRONMENT" --branch "$BRANCH" \
    "Create a file named hopsesh-smoke-$CODEWORD.txt at the top of the repository containing the single line $CODEWORD. Change nothing else, and open no pull request.") \
    > "$WORK/exec.out" 2>"$WORK/exec.err" || fail "codex cloud exec: $(cat "$WORK/exec.err")"
  CLOUDURL=$(grep -Eo 'https?://[^ ]+/tasks/task_[A-Za-z0-9_-]+' "$WORK/exec.out" | tail -1)
  TASK=${CLOUDURL##*/}
  case "$TASK" in task_*) ;; *) cat "$WORK/exec.out"; fail "codex cloud exec printed no task link hopsesh could read; update the module's notes" ;; esac
  echo "task: $TASK ($CLOUDURL)"

  echo "== 3. waiting for it (codex cloud status, every 15 s, up to ${CODEX_WAIT:-900} s)"
  waited=0
  until (cd "$CHECKOUT" && codex cloud status "$TASK") > "$WORK/status.out" 2>&1; do
    grep -q '^\[ERROR\]' "$WORK/status.out" && { cat "$WORK/status.out"; fail "the task failed"; }
    [ "$waited" -lt "${CODEX_WAIT:-900}" ] || { cat "$WORK/status.out"; fail "the task did not finish in time"; }
    sleep 15
    waited=$((waited + 15))
  done
  (cd "$CHECKOUT" && codex cloud diff "$TASK") > "$WORK/diff.out" 2>&1 || true
  (cd "$CHECKOUT" && codex cloud list --limit 5 --json) > "$WORK/list-after.json" 2>/dev/null || true

  echo "== 4. the plan (read-only), then back with hopsesh: the diff committed, the task written as a Codex session"
  "$BIN" plan "codex-cloud:$TASK" --to "$CHECKOUT" --json > "$WORK/plan.json" || { cat "$WORK/plan.json"; fail "the plan has a blocker"; }
  status=0
  "$BIN" pull "codex-cloud:$TASK" --to "$CHECKOUT" --yes --json > "$WORK/pull.json" || status=$?
  JOURNAL=$(json "$WORK/pull.json" result.journal) # before failing: cleanup undoes what it made
  [ "$status" -eq 0 ] || { cat "$WORK/pull.json"; fail "pull (exit $status)"; }
  OUTCOME=$(json "$WORK/pull.json" brought.outcome)
  WRITTEN=$(json "$WORK/pull.json" brought.written)
  BRANCH_HERE=$(json "$WORK/pull.json" brought.branch)
  WORKTREE=$(json "$WORK/pull.json" brought.worktree)
  KEY=$(json "$WORK/pull.json" brought.key)
  echo "outcome: $OUTCOME; written: $WRITTEN; branch: $BRANCH_HERE; worktree: $WORKTREE"
  if [ "$OUTCOME" != complete ] || [ "$WRITTEN" != True ]; then fail "the task did not come back: $(json "$WORK/pull.json" brought.message)"; fi
  [ "$BRANCH_HERE" = "hopsesh/from/codex-cloud/$TASK" ] || fail "the diff is not on hopsesh/from/codex-cloud/$TASK"
  grep -qx "$CODEWORD" "$WORKTREE/hopsesh-smoke-$CODEWORD.txt" || fail "the task's file is not in the worktree"
  [ -z "$(git -C "$CHECKOUT" status --porcelain --untracked-files=no)" ] || echo "note: your checkout has changes of its own; hopsesh did not touch it"
  "$BIN" show "$KEY" --json > "$WORK/show.json" || fail "the Codex session is not listed"
  grep -q "$TASK" "$(json "$WORK/show.json" session.path)" || fail "the Codex session does not name the task"

  echo
  echo "== what codex printed (compare with the shapes agents/codex/cloudtasks.go reads from source)"
  for f in list.json list-after.json exec.out status.out; do echo "-- $f"; cat "$WORK/$f"; done
  echo "-- diff.out (first 40 lines)"; head -40 "$WORK/diff.out"
  echo
  echo "Codex cloud smoke test passed: record the codex version (codex --version) in the module's Tested list. Undo runs now."
  exit 0
fi

if [ "$MODE" = handoff ]; then
  CODEWORD="hsm-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
  cat > "$WORK/note.md" <<NOTE
This is a connectivity check of hopsesh's hand-off: there is no open work, so do not change, commit or push anything.
The codeword for this check is $CODEWORD. Reply with only the codeword. Do not read, run or change anything.
NOTE

  echo "== 1. the login and the flags, read-only"
  "$BIN" clouds allow claude-cloud >/dev/null
  "$BIN" clouds test claude-cloud || fail "Claude Code cloud cannot be used with this login"

  echo "== 2. the plan (read-only)"
  "$BIN" plan "claude/$SESSION" --to claude-cloud --note-file "$WORK/note.md" --json > "$WORK/plan.json" || { cat "$WORK/plan.json"; fail "the plan has a blocker"; }
  grep -q "$CODEWORD" "$WORK/plan.json" || fail "the codeword is not in the briefing"

  echo "== 3. the hand-off: claude --cloud runs HERE, in your terminal. If Claude Code asks whether you trust"
  echo "   hopsesh's hand-off folder, answer it (yes starts the session); the script goes on when claude exits."
  status=0
  "$BIN" handoff "claude/$SESSION" --to claude-cloud --note-file "$WORK/note.md" --yes --json > "$WORK/handoff.json" || status=$?
  # Read the journal before anything can fail: cleanup then undoes a branch already pushed.
  HANDOFF=$(json "$WORK/handoff.json" result.journal)
  CLOUDURL=$(json "$WORK/handoff.json" handoff.url)
  if [ "$status" -ne 0 ]; then
    cat "$WORK/handoff.json"
    fail "handoff (exit $status): $(json "$WORK/handoff.json" handoff.message)"
  fi
  CLOUDID=$(json "$WORK/handoff.json" handoff.session)
  echo "session: $CLOUDID ($CLOUDURL); branch: $(json "$WORK/handoff.json" handoff.branch); link pasted: $(json "$WORK/handoff.json" handoff.pasted)"
  case "$CLOUDID" in session_*) ;; *) fail "hopsesh read no session id from what claude --cloud printed; update the module's notes on its output" ;; esac
  [ "$(json "$WORK/handoff.json" handoff.pasted)" != True ] || echo "note: the link was pasted by hand; hopsesh did not see it in claude's output"

  echo "== 4. no follow-up: Claude Code cloud takes none from hopsesh"
  if "$BIN" followup "claude-cloud:$CLOUDID" "x" --yes >/dev/null 2>&1; then fail "an unqualified follow-up was sent"; fi
  WAIT=${HANDOFF_WAIT:-240}
  echo "waiting ${WAIT}s for the cloud's turn (HANDOFF_WAIT)…"
  sleep "$WAIT"

  echo "== 5. back with the teleport (THIS STEP NEEDS YOUR TERMINAL: once the conversation shows, send a message (\"ok\") so Claude Code saves its copy, then /exit)"
  status=0
  "$BIN" pull "claude-cloud:$CLOUDID" --to "$CHECKOUT" --yes --run --json > "$WORK/pull.json" || status=$?
  JOURNAL=$(json "$WORK/pull.json" result.journal) # before failing: cleanup undoes what it made
  [ "$status" -eq 0 ] || { cat "$WORK/pull.json"; fail "pull (exit $status)"; }
  OUTCOME=$(json "$WORK/pull.json" brought.outcome)
  RESTORED=$(json "$WORK/pull.json" brought.restored)
  EXPECTED=$(json "$WORK/pull.json" brought.expected)
  KEY=$(json "$WORK/pull.json" brought.key)
  CHECK=$(json "$WORK/pull.json" brought.check)
  echo "outcome: $OUTCOME; restored $RESTORED (stated: ${EXPECTED:-none}; checked against: ${CHECK:-the count})"
  [ "$OUTCOME" != waiting ] || fail "Claude Code saved no copy: send a message in the teleported session before /exit"
  [ "$OUTCOME" = complete ] || fail "the copy is $OUTCOME: $(json "$WORK/pull.json" brought.message)"
  "$BIN" show "$KEY" --json > "$WORK/show.json" || fail "the copy is not listed"
  COPY=$(json "$WORK/show.json" session.path)
  python3 - "$COPY" "$CODEWORD" <<'PY' || fail "the cloud session did not reply with the codeword (or the copy lacks its reply)"
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

echo "== 3. the teleport (once the conversation shows, send a message (\"ok\") so Claude Code saves its copy, then /exit)"
status=0
"$BIN" pull "$SESSION" --to "$CHECKOUT" --yes --run --json > "$WORK/pull.json" || status=$?
JOURNAL=$(json "$WORK/pull.json" result.journal) # before failing: cleanup undoes what it made
[ "$status" -eq 0 ] || { cat "$WORK/pull.json"; fail "pull (exit $status)"; }
OUTCOME=$(json "$WORK/pull.json" brought.outcome)
RESTORED=$(json "$WORK/pull.json" brought.restored)
EXPECTED=$(json "$WORK/pull.json" brought.expected)
STATED=$(json "$WORK/pull.json" brought.stated)
BRANCH=$(json "$WORK/pull.json" brought.branch)
echo "outcome: $OUTCOME; restored $RESTORED of $EXPECTED (stated by Claude Code: $STATED); branch: ${BRANCH:-none}"

[ "$OUTCOME" != waiting ] || fail "Claude Code wrote no copy hopsesh could find (send a message in the teleported session before /exit; did the teleport fail?)"
[ "$STATED" != True ] || echo "note: the copy has a teleported-from record with a count again; update the module's notes"
# A session hopsesh did not start has no briefing to check against: unchecked is expected.
case "$OUTCOME" in complete|unchecked) ;; *) fail "the copy is $OUTCOME: $(json "$WORK/pull.json" brought.message)" ;; esac
case "$BRANCH" in hopsesh/from/claude-cloud/*|"") ;; *) fail "the cloud's branch was not renamed: $BRANCH" ;; esac

echo
echo "cloud smoke test passed: record the Claude Code version (claude --version) in the module's Tested list"
