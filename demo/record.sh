#!/bin/sh
# Record the README demo in containers with made-up machines and sessions.
#
#   demo/record.sh               # records demo/hopsesh.tape → docs/assets/demo.gif
#   demo/record.sh shell         # a shell on the laptop, for trying things out
#   demo/record.sh gui           # the desktop app's UI at http://localhost:34115
#
# Needs Docker. Nothing from your own machine or ~/.claude is used.
set -eu
cd "$(dirname "$0")/.."
NET=hopsesh-demo VOL=hopsesh-demo-shared
cleanup() { docker rm -f hopsesh-studio >/dev/null 2>&1 || true; docker network rm "$NET" >/dev/null 2>&1 || true; docker volume rm "$VOL" >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup

docker build -q -f demo/Dockerfile --target studio -t hopsesh-demo-studio . >/dev/null
docker build -q -f demo/Dockerfile --target laptop -t hopsesh-demo-laptop . >/dev/null
docker network create "$NET" >/dev/null
docker volume create "$VOL" >/dev/null
docker run -d --rm --name hopsesh-studio --hostname studio --network "$NET" --network-alias studio \
  -v "$VOL:/shared" hopsesh-demo-studio >/dev/null
i=0
until docker exec hopsesh-studio test -f /shared/ready 2>/dev/null; do
  i=$((i + 1)); [ "$i" -lt 60 ] || { docker logs hopsesh-studio; exit 1; }; sleep 1
done

if [ "${1:-}" = gui ]; then
  echo "open http://localhost:34115 (Ctrl-C to stop)"
  docker run --rm --hostname laptop --network "$NET" -v "$VOL:/shared" -p 127.0.0.1:34115:34115 \
    --entrypoint /bin/bash hopsesh-demo-laptop \
    -c 'laptop-setup && exec su alice -c "cd ~ && WAILS_SERVER_HOST=0.0.0.0 WAILS_SERVER_PORT=34115 hopsesh-app"'
elif [ "${1:-}" = shell ]; then
  docker run --rm -it --hostname laptop --network "$NET" -v "$VOL:/shared" -v "$PWD/demo:/demo" \
    --entrypoint /bin/bash hopsesh-demo-laptop -c 'laptop-setup && exec su - alice'
else
  mkdir -p demo/out
  docker run --rm --hostname laptop --network "$NET" -v "$VOL:/shared" -v "$PWD/demo:/demo" \
    -w /demo --entrypoint /bin/bash hopsesh-demo-laptop -c "laptop-setup && vhs ${TAPE:-hopsesh.tape}"
  cp demo/out/demo.gif docs/assets/demo.gif
  echo "wrote docs/assets/demo.gif"
fi
