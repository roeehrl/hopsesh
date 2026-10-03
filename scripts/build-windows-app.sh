#!/usr/bin/env bash
# Build the hopsesh Windows app for amd64 and arm64: hopsesh-app.exe (a window program
# with its icon, manifest and version information) and hopsesh.exe side by side, as
#   hopsesh-<version>-windows-<arch>-app.zip    what the app updates itself from
#   hopsesh-<version>-windows-<arch>-setup.exe  the per-user installer (NSIS)
# Runs on Linux or macOS (no CGO; needs makensis).
#
#   VERSION=0.3.0 HOPSESH_RELEASE_PUBKEY=… scripts/build-windows-app.sh
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${VERSION:-0.0.0-dev}"
OUT="${OUT:-dist/windows}"
NUMVERSION=$(printf '%s' "$VERSION" | sed -E 's/^v?([0-9]+)\.([0-9]+)\.([0-9]+).*/\1.\2.\3.0/')
LDFLAGS="-s -w -X github.com/roeehrl/hopsesh/internal/version.Version=$VERSION -X github.com/roeehrl/hopsesh/internal/version.Commit=${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo none)} -X github.com/roeehrl/hopsesh/internal/version.Date=${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)} -X github.com/roeehrl/hopsesh/internal/update.PublicKey=${HOPSESH_RELEASE_PUBKEY:-}"
TAGS="${TAGS:-production}"

command -v makensis >/dev/null || { echo "makensis is missing (apt install nsis, or brew install makensis)" >&2; exit 1; }
rm -rf "$OUT" && mkdir -p "$OUT/tmp"
go run ./internal/devtools/icon "$OUT/tmp/icon.png"

for arch in amd64 arm64; do
  stage="$OUT/tmp/$arch"
  mkdir -p "$stage"
  syso="cmd/hopsesh-app/rsrc_windows_$arch.syso"
  go run ./internal/devtools/winres -icon "$OUT/tmp/icon.png" -ico "$OUT/tmp/hopsesh.ico" -arch "$arch" -version "$VERSION" \
    -name hopsesh-app.exe -description "hopsesh" -out "$syso"
  CGO_ENABLED=0 GOOS=windows GOARCH=$arch go build -trimpath -tags "$TAGS" -ldflags "$LDFLAGS -H windowsgui" -o "$stage/hopsesh-app.exe" ./cmd/hopsesh-app
  rm -f "$syso"
  CGO_ENABLED=0 GOOS=windows GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$stage/hopsesh.exe" ./cmd/hopsesh
  cp LICENSE "$stage/LICENSE"
  (cd "$stage" && zip -q -X "../../hopsesh-$VERSION-windows-$arch-app.zip" hopsesh-app.exe hopsesh.exe LICENSE)
  makensis -V2 -DVERSION="$VERSION" -DNUMVERSION="$NUMVERSION" -DARCH="$arch" -DSRC="$(cd "$stage" && pwd)" \
    -DICON="$(cd "$OUT/tmp" && pwd)/hopsesh.ico" -DART="$(pwd)/packaging/windows/art" -DOUT="$(cd "$OUT" && pwd)/hopsesh-$VERSION-windows-$arch-setup.exe" packaging/windows/hopsesh.nsi
done
rm -rf "$OUT/tmp"
ls -l "$OUT"
