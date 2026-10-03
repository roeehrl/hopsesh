#!/bin/sh
# The narrated cut: demo/narrate.sh TAKE.mp3 [MUSIC.mp3]. Needs the captionless story picture
# (TAG=story-vo CAPTIONS=off MODES=story demo/record.sh media), ffmpeg and Docker.
set -eu
cd "$(dirname "$0")/.."
take=$1 music=${2:-}
PLAYWRIGHT=mcr.microsoft.com/playwright:v1.63.0-noble
for s in ${SCHEMES:-dark light}; do
  m=demo/out/media
  python3 demo/narrate.py plan "$take" "$m/story-vo-$s.marks.json" "$m/story-vo-$s.mp4" "$m/story-vo-$s.captions.json"
  rm -rf "$m/caps-$s"
  docker run --rm -v "$PWD/demo:/demo" -w /demo/capture --ipc=host \
    -e CAPS="/demo/out/media/story-vo-$s.captions.json" -e CAPDIR="/demo/out/media/caps-$s" "$PLAYWRIGHT" \
    sh -c "npm i --silent --no-save --no-audit --no-fund playwright@1.63.0 >/dev/null 2>&1 && node captions.mjs"
  python3 demo/narrate.py mix "$take" "$m/story-vo-$s.marks.json" "$m/story-vo-$s.mp4" \
    "$m/story-vo-$s.captions.json" "$m/caps-$s" "$m/story-narrated-$s.mp4" ${music:+"$music"}
done
