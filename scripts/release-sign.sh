#!/usr/bin/env bash
# Complete a draft hopsesh release on the maintainer's Mac. No signing key or Apple
# credential ever leaves this machine.
#
#   scripts/release-sign.sh v0.1.0
#
# 1. checks the draft release that the release workflow made for the tag, and verifies
#    GitHub's build provenance for every file it contains;
# 2. builds the macOS CLI (amd64, arm64) from the tagged commit, signs it with the
#    Developer ID identity (hardened runtime) and notarizes it;
# 3. builds, signs, notarizes and staples the app (scripts/build-macos-app.sh);
# 4. writes checksums.txt for every file, signs it with the release key, and uploads
#    the new files to the draft. Publishing stays a separate, manual step.
#
# Environment:
#   SIGN_IDENTITY             default: the first "Developer ID Application" identity
#   NOTARY_PROFILE            default: hopsesh-notary (xcrun notarytool store-credentials)
#   HOPSESH_RELEASE_KEY_FILE  default: ~/.hopsesh-release/hopsesh-release.pem
#   DRY_RUN=1                 build, sign and check everything, but skip notarization
#                             and upload nothing
set -euo pipefail

REPO="roeehrl/hopsesh"
WORKFLOW="$REPO/.github/workflows/release.yml"
TAG="${1:?usage: scripts/release-sign.sh vX.Y.Z}"
VERSION="${TAG#v}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KEY="${HOPSESH_RELEASE_KEY_FILE:-$HOME/.hopsesh-release/hopsesh-release.pem}"
NOTARY_PROFILE="${NOTARY_PROFILE:-hopsesh-notary}"
DRY_RUN="${DRY_RUN:-}"

say() { printf '\n==> %s\n' "$*"; }
die() { printf 'release-sign: %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = Darwin ] || die "run this on the maintainer's Mac"
for c in gh git go openssl xcrun codesign shasum ditto; do
  command -v "$c" >/dev/null || die "$c is required"
done
[ -r "$KEY" ] || die "release key not found at $KEY"
SIGN_IDENTITY="${SIGN_IDENTITY:-$(security find-identity -v -p codesigning | awk -F'"' '/Developer ID Application/{print $2; exit}')}"
[ -n "$SIGN_IDENTITY" ] || die "no Developer ID Application identity in the keychain"
if [ -z "$DRY_RUN" ]; then
  xcrun notarytool history --keychain-profile "$NOTARY_PROFILE" >/dev/null 2>&1 \
    || die "notary profile '$NOTARY_PROFILE' does not work; create it with: xcrun notarytool store-credentials $NOTARY_PROFILE --key <AuthKey.p8> --key-id <id> --issuer <issuer-id>"
fi

say "Checking the draft release for $TAG"
draft="$(gh release view "$TAG" -R "$REPO" --json isDraft --jq .isDraft 2>/dev/null)" \
  || die "no release for $TAG yet: push the tag and wait for the release workflow"
[ "$draft" = true ] || die "$TAG is already published; refusing to change it"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/src" "$WORK/ci" "$WORK/out"

say "Exporting the source at $TAG"
git -C "$ROOT" fetch -q origin "refs/tags/$TAG:refs/tags/$TAG" 2>/dev/null || true
git -C "$ROOT" rev-parse -q --verify "refs/tags/$TAG" >/dev/null || die "tag $TAG not found"
git -C "$ROOT" archive --format=tar "$TAG" | tar -x -C "$WORK/src"
FULL_COMMIT="$(git -C "$ROOT" rev-parse "$TAG^{commit}")"
COMMIT="${FULL_COMMIT:0:7}"
DATE="$(git -C "$ROOT" log -1 --format=%cI "$TAG^{commit}")"

# The release key must be the one whose public half ships in the tagged source.
openssl ec -in "$KEY" -pubout 2>/dev/null | diff -q - "$WORK/src/packaging/release-key.pub" >/dev/null \
  || die "$KEY does not match packaging/release-key.pub at $TAG"
PUBKEY="$(openssl ec -in "$KEY" -pubout -outform DER 2>/dev/null | base64 | tr -d '\n')"

say "Downloading and verifying the files the release workflow built"
gh release download "$TAG" -R "$REPO" -D "$WORK/ci"
n=0
for f in "$WORK"/ci/*; do
  case "$f" in
    *.tar.gz | *.zip | *.deb | *.rpm | *.apk)
      gh attestation verify "$f" --repo "$REPO" --signer-workflow "$WORKFLOW" \
        --source-ref "refs/tags/$TAG" --source-digest "$FULL_COMMIT" --deny-self-hosted-runners >/dev/null \
        || die "no valid build provenance for $(basename "$f"); not signing"
      n=$((n + 1)) ;;
    *checksums.txt*) rm -f "$f" ;; # replaced below
  esac
done
[ "$n" -gt 0 ] || die "the draft has no Linux/Windows files yet"
echo "verified provenance for $n file(s)"
cp "$WORK"/ci/* "$WORK/out/"

notarize() { # file-or-dir to submit
  if [ -n "$DRY_RUN" ]; then echo "(dry run: not notarizing $(basename "$1"))"; return; fi
  local status
  status="$(xcrun notarytool submit "$1" --keychain-profile "$NOTARY_PROFILE" --wait --output-format json | sed -n 's/.*"status" *: *"\([^"]*\)".*/\1/p')"
  [ "$status" = Accepted ] || die "notarization of $(basename "$1") ended with status '$status'"
}

LDFLAGS="-s -w -X github.com/roeehrl/hopsesh/internal/version.Version=$VERSION -X github.com/roeehrl/hopsesh/internal/version.Commit=$COMMIT -X github.com/roeehrl/hopsesh/internal/version.Date=$DATE -X github.com/roeehrl/hopsesh/internal/update.PublicKey=$PUBKEY"
for arch in amd64 arm64; do
  say "macOS CLI ($arch): build, sign, notarize"
  dir="$WORK/darwin_$arch"
  mkdir -p "$dir"
  (cd "$WORK/src" && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$dir/hopsesh" ./cmd/hopsesh)
  codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$dir/hopsesh"
  codesign --verify --strict "$dir/hopsesh"
  ditto -c -k --keepParent "$dir/hopsesh" "$WORK/notarize_$arch.zip"
  notarize "$WORK/notarize_$arch.zip"
  cp "$WORK/src/LICENSE" "$WORK/src/README.md" "$WORK/src/CHANGELOG.md" "$dir/"
  COPYFILE_DISABLE=1 tar --no-mac-metadata --uid 0 --gid 0 -czf "$WORK/out/hopsesh_${VERSION}_darwin_${arch}.tar.gz" \
    -C "$dir" hopsesh LICENSE README.md CHANGELOG.md
done

say "macOS app: build, sign, notarize, staple"
APP_NOTARY=""
[ -z "$DRY_RUN" ] && APP_NOTARY="$NOTARY_PROFILE"
(cd "$WORK/src" && env VERSION="$VERSION" COMMIT="$COMMIT" DATE="$DATE" SIGN_IDENTITY="$SIGN_IDENTITY" \
  HOPSESH_RELEASE_PUBKEY="$PUBKEY" OUT="$WORK/app" NOTARY_PROFILE="$APP_NOTARY" scripts/build-macos-app.sh)
cp "$WORK"/app/*.dmg "$WORK/out/"

say "checksums.txt and its signature"
(cd "$WORK/out" && shasum -a 256 -- * | sort -k2 > "$WORK/checksums.txt")
mv "$WORK/checksums.txt" "$WORK/out/checksums.txt"
openssl dgst -sha256 -sign "$KEY" -out "$WORK/out/checksums.txt.sig" "$WORK/out/checksums.txt"
openssl dgst -sha256 -verify "$WORK/src/packaging/release-key.pub" -signature "$WORK/out/checksums.txt.sig" "$WORK/out/checksums.txt"
cat "$WORK/out/checksums.txt"

if [ -n "$DRY_RUN" ]; then
  say "Dry run complete: nothing was uploaded"
  exit 0
fi

say "Uploading to the draft release"
gh release upload "$TAG" -R "$REPO" --clobber \
  "$WORK"/out/hopsesh_"$VERSION"_darwin_*.tar.gz "$WORK"/out/*.dmg "$WORK/out/checksums.txt" "$WORK/out/checksums.txt.sig"

say "Done. Review the draft, then publish it:"
echo "  https://github.com/$REPO/releases/tag/$TAG   (drafts: https://github.com/$REPO/releases)"
echo "  gh release edit $TAG -R $REPO --draft=false"
