#!/bin/sh
# Render background.html into background.png (540×380, 72 dpi) and background@2x.png
# (1080×760, 144 dpi). Needs Google Chrome. Run after editing background.html and commit the PNGs.
set -eu
cd "$(dirname "$0")"
C=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for s in 1 2; do
  out=$([ "$s" = 1 ] && echo background.png || echo background@2x.png)
  rm -f "$out"
  "$C" --headless=new --user-data-dir="$tmp/$s" --hide-scrollbars --window-size=540,380 \
    --force-device-scale-factor="$s" --virtual-time-budget=3000 \
    --screenshot="$PWD/$out" "file://$PWD/background.html" >/dev/null 2>&1 &
  pid=$! i=0
  # Chrome writes the screenshot but doesn't always exit, so wait for the file instead.
  while [ ! -s "$out" ] && [ $i -lt 30 ]; do sleep 1; i=$((i + 1)); done
  sleep 1
  kill "$pid" 2>/dev/null || true
  pkill -f "$tmp/$s" 2>/dev/null || true
  sips -s dpiWidth $((72 * s)) -s dpiHeight $((72 * s)) "$out" >/dev/null
  echo "rendered $out ($(sips -g pixelWidth -g pixelHeight "$out" | awk '/pixel/{printf "%s ", $2}'))"
done
