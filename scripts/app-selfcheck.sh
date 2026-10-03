#!/usr/bin/env bash
# The real desktop app on macOS or Linux, end to end: a test build (-tags e2e) starts on a
# demo home, its window lists the demo sessions and, through the real backend, saves a
# setting this script waits for. On Linux it runs under Xvfb. Used by the nightly CI run.
#
#   scripts/app-selfcheck.sh
set -euo pipefail
cd "$(dirname "$0")/.."
WORK=$(mktemp -d "${RUNNER_TEMP:-/tmp}/hsapp.XXXXXX")
go build -tags e2e -o "$WORK/hopsesh-app" ./cmd/hopsesh-app
go run ./internal/devtools/webtest -home "$WORK/home" -prepare > "$WORK/env.json"
eval "$(jq -r 'to_entries[] | "export \(.key)=\(.value | @sh)"' "$WORK/env.json")"
export PATH="/usr/bin:/bin:/usr/sbin:/sbin" HOPSESH_E2E_SCRIPT="$PWD/internal/devtools/webtest/real/selfcheck.js"
CFG="$HOPSESH_CONFIG_DIR/config.toml"
if [ "$(uname -s)" = Linux ]; then
  xvfb-run -a -s "-screen 0 1280x1024x24" "$WORK/hopsesh-app" > "$WORK/app.log" 2>&1 &
else
  "$WORK/hopsesh-app" > "$WORK/app.log" 2>&1 &
fi
pid=$!
ok=""
for _ in $(seq 1 120); do
  if [ -f "$CFG" ] && grep -q 'receive *= *true' "$CFG"; then ok=1; break; fi
  kill -0 "$pid" 2>/dev/null || break
  sleep 1
done
kill "$pid" 2>/dev/null || true
pkill -f "$WORK/hopsesh-app" 2>/dev/null || true
if [ -z "$ok" ]; then
  echo "the app did not list the demo sessions and answer through its backend in time" >&2
  tail -n 50 "$WORK/app.log" >&2
  exit 1
fi
echo "the real app listed the demo sessions and saved a setting through its backend"
