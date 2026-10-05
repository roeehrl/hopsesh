#!/bin/sh
# Maintainer's manual check of hopsesh's iTerm2 API client against the real iTerm2 on this
# Mac (CI never runs it: CI has no iTerm2 and nobody to answer macOS's consent prompt).
#
# Run it from a tab in iTerm2, with iTerm2's Python API already enabled by you
# (iTerm2 > Settings > General > Magic > Enable Python API). hopsesh never enables it and
# this script changes no iTerm2 or Claude setting. The first run asks macOS whether
# hopsesh's smoke tool (via osascript) may control iTerm2, to fetch an API cookie.
#
# What it does, in order:
#   1. probes the API socket without credentials (nothing is prompted if the API is off);
#   2. connects with a cookie from AppleScript, lists windows and sessions, and reads the
#      key window;
#   3. finds the tab it runs in by the TTY the OS reports for it, and checks that
#      AppleScript's "unique ID" for that tab is the API's session id (hopsesh may focus
#      a tab found one way through the other);
#   4. opens a tab in the key window running sh -c 'sleep 8; exit 3', waits for iTerm2's
#      session-terminated event, and checks the exit status 3 from a status file the tab's
#      own command writes (iTerm2's protocol carries no exit status; hopsesh gets codes from
#      its launch records);
#   5. opens a split beside this tab running sh -c 'sleep 10', sets user.hopsesh_title on
#      it, waits for it to end, and focuses this tab again.
# It never sends text to a session and never reads a screen. To see hopsesh itself use the
# API, hand a session off from the app with the API on: the step opens in a split beside the
# session in front, and closing that split before the step ends stops the app's wait. If your profile keeps ended
# sessions open, close the test tab when its program has ended and the script goes on.
#
#   HOPSESH_ITERM_API=1 scripts/iterm-api-smoke.sh
set -eu

[ "$(uname -s)" = Darwin ] || { echo "macOS only" >&2; exit 2; }
[ "${HOPSESH_ITERM_API:-}" = 1 ] || { echo "This drives your real iTerm2 (opens two tabs/panes); set HOPSESH_ITERM_API=1 to run it." >&2; exit 2; }
[ "${TERM_PROGRAM:-}" = iTerm.app ] || echo "warning: not running inside iTerm2; the find-by-tty, split and focus steps will fail" >&2

cd "$(dirname "$0")/.."
exec go run ./internal/devtools/itermapismoke
