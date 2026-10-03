#!/usr/bin/env bash
# Run the scenario matrix on this machine alone: "there" is a second home folder, and a
# stand-in ssh runs its commands (and SFTP, through the system's sftp-server) there instead
# of logging in anywhere. For quick runs while working on hopsesh; CI uses real ssh.
#
#   scripts/matrix-local.sh [hsmatrix run flags, e.g. -only move,push]
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(cd "$(mktemp -d "${TMPDIR:-/tmp}/hsmatrix-local.XXXXXX")" && pwd -P) # resolved, as agents record folders
BIN="$ROOT/bin"; THERE="$ROOT/there"; mkdir -p "$BIN" "$THERE/bin"
go build -o "$BIN/hopsesh" ./cmd/hopsesh
go build -o "$BIN/hsmatrix" ./internal/devtools/hsmatrix
go build -o "$BIN/fakeagent" ./internal/devtools/fakeagent
for p in hopsesh hsmatrix; do cp "$BIN/$p" "$THERE/bin/$p"; done
for a in claude codex; do cp "$BIN/fakeagent" "$BIN/$a"; cp "$BIN/fakeagent" "$THERE/bin/$a"; done
SFTP=$(for p in /usr/libexec/sftp-server /usr/lib/openssh/sftp-server /usr/lib/ssh/sftp-server; do [ -x "$p" ] && echo "$p" && break; done)
[ -n "$SFTP" ] || { echo "no sftp-server found" >&2; exit 1; }

cat > "$BIN/ssh" <<SSH
#!/usr/bin/env bash
# Stand-in ssh: runs the command (or the sftp subsystem) as "there", locally.
sub=0; rest=()
for a; do
  if [ "\$a" = -G ]; then # how the destination resolves: always here
    for d; do :; done
    printf 'hostname 127.0.0.1\nport 22\nuser %s\n' "\${d%%@*}"; exit 0
  fi
done
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o|-p|-F|-i|-l|-E|-S|-J|-W|-b|-c|-D|-L|-R|-m|-O|-Q|-w|-B|-e|-I) shift 2 ;;
    -s) sub=1; shift ;;
    -*) shift ;;
    *) shift; [ "\${1:-}" = -- ] && shift; rest=("\$@"); break ;;
  esac
done
env -i HOME="$THERE" USER="\${USER:-u}" SHELL=/bin/sh LANG="\${LANG:-C.UTF-8}" HOPSESH_MACHINE=box HOPSESH_TAILSCALE=off \
  PATH="$THERE/bin:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin" \
  sh -c 'cd "\$HOME" && if [ "\$0" = 1 ]; then exec "$SFTP"; else exec /bin/sh -c "\$1"; fi' "\$sub" "\${rest[*]}"
SSH
cat > "$BIN/ssh-keyscan" <<'KS'
#!/bin/sh
# Stand-in ssh-keyscan: a made-up host key for whatever is asked.
for h; do last=$h; done
echo "$last ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcH"
KS
chmod +x "$BIN/ssh" "$BIN/ssh-keyscan"
echo "matrix workspace: $ROOT"
PATH="$BIN:$PATH" "$BIN/hsmatrix" run -hopsesh "$BIN/hopsesh" -there "box@local" -alias "box-alias" -label "local (stand-in ssh)" -out "$ROOT/out" "$@"
