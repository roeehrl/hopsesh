#!/bin/sh
# Record the README demo in containers with made-up machines and sessions.
#
#   demo/record.sh               # records demo/hopsesh.tape → docs/assets/demo.gif
#   demo/record.sh shell         # a shell on the laptop, for trying things out
#   demo/record.sh gui           # the desktop app's UI at http://localhost:34115
#   demo/record.sh survey        # screenshots of every app screen into demo/out/survey
#   demo/record.sh media         # final app stills and clips into demo/out/media
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

PLAYWRIGHT=mcr.microsoft.com/playwright:v1.63.0-noble
start_app() { # the laptop serving the app's UI, reachable as http://laptop:34115 on the network
  docker run -d --rm --name hopsesh-laptop --hostname laptop --network "$NET" --network-alias laptop \
    -v "$VOL:/shared" --entrypoint /bin/bash hopsesh-demo-laptop \
    -c 'laptop-setup && exec su - alice -c "WAILS_SERVER_HOST=0.0.0.0 WAILS_SERVER_PORT=34115 hopsesh-app"' >/dev/null
  i=0
  until docker run --rm --network "$NET" curlimages/curl -fs -o /dev/null http://laptop:34115/ 2>/dev/null; do
    i=$((i + 1)); [ "$i" -lt 90 ] || { docker logs hopsesh-laptop; exit 1; }; sleep 2
  done
}
playwright() { # run a capture script from demo/capture against the app
  docker run --rm --network "$NET" -v "$PWD/demo:/demo" -w /demo/capture --ipc=host \
    -e SCHEME="${SCHEME:-light}" -e MODE="${MODE:-}" -e LOGO_B64="$(base64 < docs/assets/logo.svg | tr -d '\n')" "$PLAYWRIGHT" \
    sh -c "npm i --silent --no-save --no-audit --no-fund playwright@1.63.0 >/dev/null 2>&1 && node $1"
}
fresh() { # a fresh pair of demo machines with the app served (a capture changes their state)
  docker rm -f hopsesh-laptop >/dev/null 2>&1 || true
  cleanup
  docker network create "$NET" >/dev/null
  docker volume create "$VOL" >/dev/null
  docker run -d --rm --name hopsesh-studio --hostname studio --network "$NET" --network-alias studio \
    -v "$VOL:/shared" hopsesh-demo-studio >/dev/null
  i=0
  until docker exec hopsesh-studio test -f /shared/ready 2>/dev/null; do
    i=$((i + 1)); [ "$i" -lt 60 ] || { docker logs hopsesh-studio; exit 1; }; sleep 1
  done
  start_app
}

if [ "${1:-}" = media ]; then
  # Stills in light and dark, then the hero and undo clips, each on fresh machines.
  trap 'docker rm -f hopsesh-laptop >/dev/null 2>&1 || true; cleanup' EXIT
  for SCHEME in ${SCHEMES:-light dark}; do
    for MODE in ${MODES:-stills hero undo}; do
      export SCHEME MODE
      fresh
      playwright capture.mjs
    done
  done
  demo/encode.sh
elif [ "${1:-}" = survey ]; then
  trap 'docker rm -f hopsesh-laptop >/dev/null 2>&1 || true; cleanup' EXIT
  start_app
  playwright survey.mjs
elif [ "${1:-}" = gui ]; then
  echo "open http://localhost:34115 (Ctrl-C to stop)"
  docker run --rm --hostname laptop --network "$NET" -v "$VOL:/shared" -p 127.0.0.1:34115:34115 \
    --entrypoint /bin/bash hopsesh-demo-laptop \
    -c 'laptop-setup && exec su - alice -c "WAILS_SERVER_HOST=0.0.0.0 WAILS_SERVER_PORT=34115 hopsesh-app"'
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
