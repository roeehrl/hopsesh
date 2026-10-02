#!/usr/bin/env bash
# Build hopsesh.app (universal arm64+x86_64) with the CLI embedded, then optionally sign,
# notarize and package it as a DMG.
#
#   VERSION=0.1.0 scripts/build-macos-app.sh
#   SIGN_IDENTITY="Developer ID Application: …" scripts/build-macos-app.sh   # sign (hardened runtime)
#   NOTARY_PROFILE=hopsesh scripts/build-macos-app.sh                        # + notarize & staple
#     (create the profile once: xcrun notarytool store-credentials hopsesh --apple-id … --team-id …)
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${VERSION:-0.0.0-dev}"
OUT="${OUT:-dist/macos}"
APP="$OUT/hopsesh.app"
LDFLAGS="-s -w -X github.com/roeehrl/hopsesh/internal/version.Version=$VERSION -X github.com/roeehrl/hopsesh/internal/version.Commit=$(git rev-parse --short HEAD 2>/dev/null || echo none) -X github.com/roeehrl/hopsesh/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X github.com/roeehrl/hopsesh/internal/update.PublicKey=${HOPSESH_RELEASE_PUBKEY:-}"
export MACOSX_DEPLOYMENT_TARGET=13.0 CGO_CFLAGS="-mmacosx-version-min=13.0" CGO_LDFLAGS="-mmacosx-version-min=13.0"

rm -rf "$APP" && mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/bin" "$OUT/tmp"
for arch in arm64 amd64; do
  CGO_ENABLED=1 GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/tmp/app-$arch" ./cmd/hopsesh-app
  CGO_ENABLED=0 GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/tmp/cli-$arch" ./cmd/hopsesh
done
lipo -create -output "$APP/Contents/MacOS/hopsesh-app" "$OUT/tmp/app-arm64" "$OUT/tmp/app-amd64"
lipo -create -output "$APP/Contents/Resources/bin/hopsesh" "$OUT/tmp/cli-arm64" "$OUT/tmp/cli-amd64"
sed "s/@VERSION@/$VERSION/g" packaging/macos/Info.plist.in > "$APP/Contents/Info.plist"

# Icon: render, then build the .icns.
ICONSET="$OUT/tmp/hopsesh.iconset"; mkdir -p "$ICONSET"
go run ./internal/devtools/icon "$OUT/tmp/icon.png"
for s in 16 32 128 256 512; do
  sips -z $s $s "$OUT/tmp/icon.png" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s*2)) $((s*2)) "$OUT/tmp/icon.png" --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/hopsesh.icns"
rm -rf "$OUT/tmp"

if [ -n "${SIGN_IDENTITY:-}" ]; then
  codesign --force --timestamp --options runtime --sign "$SIGN_IDENTITY" "$APP/Contents/Resources/bin/hopsesh"
  codesign --force --timestamp --options runtime --entitlements packaging/macos/entitlements.plist --sign "$SIGN_IDENTITY" "$APP"
  codesign --verify --deep --strict --verbose=2 "$APP"
fi

DMG="$OUT/hopsesh-$VERSION-macos-universal.dmg"
rm -f "$DMG"
hdiutil create -quiet -volname "hopsesh $VERSION" -srcfolder "$APP" -ov -format UDZO "$DMG"
[ -n "${SIGN_IDENTITY:-}" ] && codesign --force --timestamp --sign "$SIGN_IDENTITY" "$DMG"

if [ -n "${NOTARY_PROFILE:-}" ]; then
  xcrun notarytool submit "$DMG" --keychain-profile "$NOTARY_PROFILE" --wait
  xcrun stapler staple "$DMG"
  xcrun stapler staple "$APP"
fi
echo "built $APP and $DMG"
