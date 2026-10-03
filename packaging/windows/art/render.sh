#!/bin/sh
# Render the installer's images from sidebar.html and header.html into 24-bit BMPs (NSIS needs
# BMP without alpha): sidebar.bmp 164×314 and sidebar@2x.bmp 328×628 (welcome and finish pages),
# header.bmp 150×57 and header@2x.bmp 300×114. Needs Google Chrome and ffmpeg; commit the BMPs.
set -eu
cd "$(dirname "$0")"
C=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
shot() { # name width height scale
  png="$tmp/$1-$4.png"
  "$C" --headless=new --user-data-dir="$tmp/u-$1-$4" --hide-scrollbars --window-size="$2,$3" \
    --force-device-scale-factor="$4" --virtual-time-budget=3000 --screenshot="$png" "file://$PWD/$1.html" >/dev/null 2>&1 &
  pid=$! i=0
  while [ ! -s "$png" ] && [ $i -lt 30 ]; do sleep 1; i=$((i + 1)); done
  sleep 1
  kill "$pid" 2>/dev/null || true
  pkill -f "$tmp/u-$1-$4" 2>/dev/null || true
  out=$([ "$4" = 1 ] && echo "$1.bmp" || echo "$1@2x.bmp")
  ffmpeg -v error -y -i "$png" -pix_fmt bgr24 "$out"
  echo "rendered $out"
}
shot sidebar 164 314 1; shot sidebar 164 314 2
shot header 150 57 1; shot header 150 57 2
