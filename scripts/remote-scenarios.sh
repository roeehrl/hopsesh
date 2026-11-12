#!/bin/sh
# The remote-behaviour scenarios (internal/e2e/remote) over real SSH on one Linux machine:
# "there" is a throwaway user (hsremote) with the stand-in agents on its PATH and no
# hopsesh; "here" is this user, with hopsesh's own folders kept apart by the test. Needs
# sudo, sshd and nc. The scenarios set hopsesh up there themselves.
#
#   scripts/remote-scenarios.sh <bin dir with hopsesh, fakeagent, remote.test> [go test flags]
#   OLD_HOPSESH=<hopsesh 0.4.5 for this machine> adds the version-skew scenario.
#
#   scripts/remote-scenarios.sh --docker [OLD_HOPSESH]
#     builds everything for Linux and runs the same in a container (a Mac with Docker:
#     nothing is changed on this machine).
set -eu
SCRIPTS=$(cd "$(dirname "$0")" && pwd)

if [ "${1:-}" = --docker ]; then
  ROOT=$(cd "$SCRIPTS/.." && pwd)
  OUT=$(mktemp -d "${TMPDIR:-/tmp}/hopsesh-remote.XXXXXX")
  trap 'rm -rf "$OUT"' EXIT
  arch=$(docker version --format '{{.Server.Arch}}')
  for p in cmd/hopsesh:hopsesh internal/devtools/fakeagent:fakeagent; do
    (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$OUT/bin/${p#*:}" "./${p%%:*}")
  done
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c -o "$OUT/bin/remote.test" ./internal/e2e/remote)
  cp -R "$SCRIPTS" "$OUT/scripts"
  old=
  if [ -n "${2:-}" ]; then cp "$2" "$OUT/bin/hopsesh-old" && old=/hs/bin/hopsesh-old; fi
  docker build -q -t hopsesh-remote-scenarios -f "$SCRIPTS/remote-scenarios.Dockerfile" "$SCRIPTS" > /dev/null
  # Copied in, not mounted: Docker Desktop shares only some of the host's folders.
  cid=$(docker create -e OLD_HOPSESH="$old" hopsesh-remote-scenarios sh /hs/scripts/remote-scenarios.sh /hs/bin)
  docker cp -q "$OUT" "$cid:/hs"
  st=0
  docker start -a "$cid" || st=$?
  docker rm -f "$cid" > /dev/null
  exit $st
fi

# shellcheck source=lib/testhost.sh
. "$SCRIPTS/lib/testhost.sh"
BIN=$(cd "$1" && pwd)
shift
U=hsremote
th_add_user "$U"
th_clean_login_env
UHOME=$(th_home "$U")
sudo mkdir -p /usr/local/bin
for a in claude codex; do sudo install -m 0755 "$BIN/fakeagent" "/usr/local/bin/$a"; done
if command -v hopsesh > /dev/null 2>&1 || [ -e /usr/local/bin/hopsesh ]; then
  echo "hopsesh is installed system-wide; the first scenario needs a machine without it" >&2
  exit 1
fi

mkdir -p ~/.ssh && chmod 700 ~/.ssh
[ -f ~/.ssh/id_ed25519 ] || ssh-keygen -q -t ed25519 -N '' -f ~/.ssh/id_ed25519
sudo -u "$U" -H sh -c 'umask 077; mkdir -p ~/.ssh'
sudo install -o "$U" -m 0600 ~/.ssh/id_ed25519.pub "$UHOME/.ssh/authorized_keys"
# A machine nothing answers at (port 1 refuses at once).
if ! grep -q '^Host hsr-offline$' ~/.ssh/config 2> /dev/null; then
  printf 'Host hsr-offline\n  HostName 127.0.0.1\n  Port 1\n  User %s\n' "$U" >> ~/.ssh/config
fi
chmod 600 ~/.ssh/config
th_start_sshd

cd / # the test user cannot read the caller's working directory
HOPSESH_REMOTE_SCENARIOS=1 HOPSESH_BIN="$BIN/hopsesh" HOPSESH_REMOTE_THERE="$U@127.0.0.1" HOPSESH_REMOTE_OFFLINE=hsr-offline \
  HOPSESH_REMOTE_OLD_BIN="${OLD_HOPSESH:-}" exec "$BIN/remote.test" -test.v -test.count=1 -test.timeout=20m "$@"
