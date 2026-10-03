#!/bin/sh
# Turn the recorded clips into MP4 (README, website, social) and a GIF fallback. Needs ffmpeg.
set -eu
cd "$(dirname "$0")/out/media"
for f in *.webm; do
  [ -e "$f" ] || continue
  b=${f%.webm}
  ss=0
  [ -f "$b.start" ] && ss=$(cat "$b.start") # the story's title card covered the app loading
  ffmpeg -v error -y -ss "$ss" -i "$f" -c:v libx264 -preset slow -crf 22 -pix_fmt yuv420p -movflags +faststart "$b.mp4"
  case "$b" in story-*) echo "encoded $b.mp4"; continue ;; esac # the long video is MP4 only
  ffmpeg -v error -y -i "$f" -vf "fps=15,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4" "$b.gif"
  echo "encoded $b.mp4 $b.gif"
done
