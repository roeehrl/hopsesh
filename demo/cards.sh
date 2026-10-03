#!/bin/sh
# Render social-card.html into demo/out/cards/: the GitHub social preview (1280x640, light) and
# Open Graph cards (1200x630). Needs Google Chrome and the stills from `record.sh media`.
set -eu
cd "$(dirname "$0")"
C=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p out/cards
for spec in "1280 640 light github-social" "1200 630 dark og-dark" "1200 630 light og-light"; do
  # shellcheck disable=SC2086 # split the spec into its four fields
  set -- $spec
  rm -f "out/cards/$4.png"
  "$C" --headless=new --user-data-dir="$tmp/$4" --hide-scrollbars --window-size="$1,$2" \
    --virtual-time-budget=6000 --allow-file-access-from-files \
    --screenshot="$PWD/out/cards/$4.png" "file://$PWD/social-card.html?w=$1&h=$2&theme=$3" >/dev/null 2>&1 &
  pid=$! i=0
  # Chrome writes the screenshot but doesn't always exit, so wait for the file instead.
  while [ ! -s "out/cards/$4.png" ] && [ $i -lt 30 ]; do sleep 1; i=$((i + 1)); done
  sleep 1
  kill "$pid" 2>/dev/null || true
  pkill -f "$tmp/$4" 2>/dev/null || true # its helper processes
  wait "$pid" 2>/dev/null || true
  echo "wrote demo/out/cards/$4.png"
done
