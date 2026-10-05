#!/bin/sh
# Maintainer's check of hopsesh's terminal apps on a real Mac, by hand (CI never runs it:
# no CI machine has iTerm2, and nobody could answer macOS's Automation prompt there).
#
#   scripts/iterm-smoke.sh <claude session> [<codex session>]
#
# Name a Claude Code session on this Mac that is NOT running (a throwaway one: resuming it
# shows its conversation and starts no model turn until you type). Optionally a Codex
# session too. The script builds hopsesh, then walks through the checks below, pausing for
# you to look and act at each step. It types nothing into any tab and reads no tab: you do
# the typing, and you judge what you see.
#
#   1. hopsesh terminals: iTerm2 and Terminal are found; iTerm2 is the one used.
#   2. hopsesh open <session> --terminal iterm2: a NEW TAB in iTerm2's front window (a new
#      window only when iTerm2 has none) runs claude --resume. The tab has a badge with the
#      session's title, "Claude Code · <this Mac>", and its title is Claude Code's own.
#      (macOS may ask whether the terminal you run this in may control iTerm2.)
#   3. Click another tab, then hopsesh open <session> again: it says the session is already
#      open in iTerm2 and brings that tab forward. No second claude starts.
#   4. In that tab, leave Claude Code (/exit): the tab says "[claude exited with code 0]"
#      and stays open as your shell; the badge is gone.
#   5. hopsesh open <session> --terminal terminal-app: a new Terminal window; then click
#      away and hopsesh open <session> again: it brings that window forward.
#   6. (With a Codex session) hopsesh open <codex session>: a new iTerm2 tab; hopsesh open
#      again shows it, found through hopsesh's record of the launch (Codex's files have no
#      process id). Leave Codex in it.
#   7. The app's denial path, by hand: see the notes printed at the end.
#
# It writes nothing outside a scratch folder except hopsesh's own launch tickets and
# records under its state folder (removed as each launch runs and ends). It never changes
# iTerm2's or Claude Code's settings.
set -eu

[ "$(uname -s)" = Darwin ] || { echo "macOS only"; exit 1; }
[ $# -ge 1 ] || { sed -n '2,12p' "$0"; exit 2; }
CLAUDE_SESSION=$1
CODEX_SESSION=${2:-}
[ -d /Applications/iTerm.app ] || [ -d "$HOME/Applications/iTerm.app" ] || { echo "iTerm2 is not installed"; exit 1; }

repo=$(cd "$(dirname "$0")/.." && pwd)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/hopsesh-iterm-smoke.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
hs="$scratch/hopsesh"
# hopsesh's own settings and launch records go to the scratch folder: the check never reads
# or changes your configuration (which an older hopsesh may have written).
export HOPSESH_CONFIG_DIR="$scratch/config" HOPSESH_STATE_DIR="$scratch/state"
echo "Building hopsesh into ${scratch}…"
(cd "$repo" && go build -o "$hs" ./cmd/hopsesh)
iterm_version=$(defaults read /Applications/iTerm.app/Contents/Info CFBundleShortVersionString 2>/dev/null || echo unknown)
echo "iTerm2 $iterm_version; macOS $(sw_vers -productVersion)"

pause() {
	printf '\n▸ %s\n  Press Return when done (or Ctrl-C to stop). ' "$1"
	read -r _
}
ok() {
	printf '  Check: %s\n  Did you see it? [y/N] ' "$1"
	read -r answer
	case $answer in
	y | Y | yes) echo "  ✓ $1" ;;
	*)
		echo "  ✗ $1"
		failed=$((failed + 1))
		;;
	esac
}
failed=0

echo
echo "1. Terminal apps"
"$hs" terminals
if "$hs" terminals --json | grep -q '"using": "iterm2"'; then
	echo "  ✓ iTerm2 is used"
else
	echo "  ✗ iTerm2 is not the one used (terminal.app set? hopsesh terminals --use auto)"
	failed=$((failed + 1))
fi

echo
echo "2. Open in a new iTerm2 tab"
"$hs" open "$CLAUDE_SESSION" --terminal iterm2
pause "Look at iTerm2: a new tab in the front window runs Claude Code on the session."
ok "a new tab (not a new window), running claude --resume"
ok "its badge shows the title, then Claude Code · $(hostname -s)"
ok "the tab's title is Claude Code's (hopsesh does not set it)"

echo
echo "3. Show it instead of a second copy"
pause "Click another iTerm2 tab (or another app) so that tab is not in front."
out=$("$hs" open "$CLAUDE_SESSION" 2>&1) || true
echo "  $out"
case $out in
*"already open in iTerm2"*) echo "  ✓ hopsesh found the tab" ;;
*)
	echo "  ✗ hopsesh did not find the tab"
	failed=$((failed + 1))
	;;
esac
ok "the session's tab came forward, and no second claude started"

echo
echo "4. When the agent ends"
pause "In that tab, leave Claude Code (/exit)."
ok "the tab says [claude exited with code 0], stays open as your shell, and the badge is gone"

echo
echo "5. Terminal"
"$hs" open "$CLAUDE_SESSION" --terminal terminal-app
pause "Look at Terminal: a new window runs Claude Code on the session. Then click another app."
out=$("$hs" open "$CLAUDE_SESSION" 2>&1) || true
echo "  $out"
case $out in
*"already open in Terminal"*) echo "  ✓ hopsesh found the window" ;;
*)
	echo "  ✗ hopsesh did not find the window (does Terminal still expose tty on this macOS?)"
	failed=$((failed + 1))
	;;
esac
ok "Terminal's window came forward"
pause "Leave Claude Code in that window (/exit)."

if [ -n "$CODEX_SESSION" ]; then
	echo
	echo "6. A Codex session, found through hopsesh's record"
	"$hs" open "$CODEX_SESSION" --terminal iterm2
	pause "A new iTerm2 tab runs Codex. Click another tab."
	out=$("$hs" open "$CODEX_SESSION" 2>&1) || true
	echo "  $out"
	case $out in
	*"already open in iTerm2"*) echo "  ✓ found through the launch record" ;;
	*)
		echo "  ✗ the Codex tab was not found"
		failed=$((failed + 1))
		;;
	esac
	pause "Leave Codex in that tab."
fi

cat <<'EOF'

7. The app (by hand; the script does not drive it):
   - Build and open the app (make app). Resume a session that is not running: it opens as a
     tab in iTerm2. The first time, macOS asks "hopsesh wants to control iTerm2"; its text
     names your terminal app.
   - Select that session once it runs: its first action is "Show its terminal tab", which
     brings the tab forward.
   - Hand a session off to Claude Code cloud from the app: the step opens as an iTerm2 tab,
     the session's link is captured, and the tab waits for Return when it ends.
   - Denial: in System Settings › Privacy & Security › Automation, turn hopsesh's iTerm2
     switch off, then Resume: it opens in Terminal and the app says "Opened in Terminal
     instead". Turn Terminal off too: the app says to copy the command. Turn both back on.
EOF

echo
if [ "$failed" -eq 0 ]; then
	echo "All checks passed (iTerm2 $iterm_version)."
else
	echo "$failed check(s) failed (iTerm2 $iterm_version)."
	exit 1
fi
