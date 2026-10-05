#!/usr/bin/env bash
# The app's terminal window on macOS or Linux, end to end: a test build (-tags e2e) opens a
# terminal tab running termprobe and shows it in the terminal window, whose test page
# reaches hopsesh only through the tab's stream; its stand-in emulator answers the
# program's DA1 query there. Once the program ends the app writes the tab's backend, exit
# code and output, and quits. On Linux it runs under Xvfb. Used by the nightly CI run.
#
#   scripts/app-terminal-check.sh
set -euo pipefail
cd "$(dirname "$0")/.."
WORK=$(mktemp -d "${RUNNER_TEMP:-/tmp}/hsterm.XXXXXX")
go build -tags e2e -o "$WORK/hopsesh-app" ./cmd/hopsesh-app
go build -o "$WORK/termprobe" ./internal/devtools/termprobe
mkdir -p "$WORK/config" "$WORK/state"
export HOPSESH_CONFIG_DIR="$WORK/config" HOPSESH_STATE_DIR="$WORK/state"
export HOPSESH_E2E_TERMINAL="[\"$WORK/termprobe\"]" HOPSESH_E2E_TERMINAL_OUT="$WORK/terminal.txt"
if [ "$(uname -s)" = Linux ]; then
  export WEBKIT_DISABLE_DMABUF_RENDERER=1 WEBKIT_DISABLE_COMPOSITING_MODE=1 LIBGL_ALWAYS_SOFTWARE=1
  run=(xvfb-run -a -s "-screen 0 1280x1024x24")
  command -v dbus-run-session >/dev/null && run+=(dbus-run-session --)
  "${run[@]}" "$WORK/hopsesh-app" > "$WORK/app.log" 2>&1 &
else
  "$WORK/hopsesh-app" > "$WORK/app.log" 2>&1 &
fi
pid=$!
for _ in $(seq 1 120); do
  [ -s "$WORK/terminal.txt" ] && break
  kill -0 "$pid" 2>/dev/null || break
  sleep 1
done
kill "$pid" 2>/dev/null || true
pkill -f "$WORK/hopsesh-app" 2>/dev/null || true
if ! [ -s "$WORK/terminal.txt" ]; then
  echo "the app's terminal check wrote nothing" >&2
  head -n 60 "$WORK/app.log" >&2
  exit 1
fi
cat "$WORK/terminal.txt"
if ! grep -q '^backend=pty code=0$' "$WORK/terminal.txt" || ! grep -qF 'da1="\x1b[?' "$WORK/terminal.txt"; then
  echo "the tab's program did not get the window's answer, or ended badly" >&2
  exit 1
fi
echo "a tab's program ran in the real terminal window and got the window's answer through its stream"
