#!/usr/bin/env bash
# Install dumbpipe (n0-computer/dumbpipe, iroh 1.0) for the cross-runner mesh, checked
# against the release's SHA-256, and put it on PATH for the next steps. On Windows (Git
# Bash) also let its UDP traffic in, so peers can connect directly instead of by relay.
set -euo pipefail
V=v0.39.0
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) A=linux-x86_64 S=9ac5e71983eba4cf4f47e92e4694dad0f0133abba4f14dd2a2a8428cece88fef E=tar.gz ;;
  Linux-aarch64) A=linux-aarch64 S=8cd099afe80c69ac58bfbc975b28e29d41caea141f3dd396457345a739836a0e E=tar.gz ;;
  Darwin-arm64) A=darwin-aarch64 S=13f284b36f2df429487975878ac8890f50ddd7916d040d988f467a919b0fba3e E=tar.gz ;;
  Darwin-x86_64) A=darwin-x86_64 S=4480d175d7888f8f705418cd81b1512aa3c29bb84bc0f8566424c32cdc636042 E=tar.gz ;;
  MINGW*-x86_64 | MSYS*-x86_64) A=windows-x86_64 S=a58f4e1f58281b6f5407baa6502c58cc824c41d8e0c418bbb6eea51bfd674856 E=zip ;;
  *) echo "no dumbpipe build for $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac
DEST="${RUNNER_TEMP:-/tmp}/dumbpipe"
mkdir -p "$DEST"
F="$DEST/dumbpipe.$E"
curl -fsSL --retry 3 -o "$F" "https://github.com/n0-computer/dumbpipe/releases/download/$V/dumbpipe-$V-$A.$E"
# From stdin: Git Bash's sha256sum escapes a path with backslashes (and the hash with it).
if command -v sha256sum >/dev/null; then got=$(sha256sum < "$F" | cut -d' ' -f1); else got=$(shasum -a 256 < "$F" | cut -d' ' -f1); fi
[ "$got" = "$S" ] || { echo "dumbpipe checksum mismatch: $got" >&2; exit 1; }
if [ "$E" = zip ]; then unzip -q -o "$F" -d "$DEST"; else tar -xzf "$F" -C "$DEST"; fi
BIN=$(find "$DEST" -type f \( -name dumbpipe -o -name dumbpipe.exe \) | head -n 1)
[ -n "$BIN" ] || { echo "no dumbpipe binary in the archive" >&2; exit 1; }
chmod +x "$BIN"
echo "dumbpipe $V at $BIN"
dir=$(dirname "$BIN")
if [ -n "${GITHUB_PATH:-}" ]; then
  if command -v cygpath >/dev/null; then cygpath -w "$dir" >> "$GITHUB_PATH"; else echo "$dir" >> "$GITHUB_PATH"; fi
fi
if [ "$E" = zip ]; then
  win=$(cygpath -w "$BIN")
  powershell -NoProfile -Command "New-NetFirewallRule -DisplayName dumbpipe -Direction Inbound -Program '$win' -Action Allow -Protocol UDP | Out-Null"
fi
