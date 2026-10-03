#!/bin/sh
# Turn the recorded clips into MP4 (README, website, social) and a GIF fallback. Needs ffmpeg.
set -eu
cd "$(dirname "$0")/out/media"
for f in *.webm; do
  [ -e "$f" ] || continue
  b=${f%.webm}
  ffmpeg -v error -y -i "$f" -c:v libx264 -preset slow -crf 22 -pix_fmt yuv420p -movflags +faststart "$b.mp4"
  ffmpeg -v error -y -i "$f" -vf "fps=15,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4" "$b.gif"
  echo "encoded $b.mp4 $b.gif"
done
