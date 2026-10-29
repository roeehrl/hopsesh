#!/bin/sh
# Check, on a real Mac, that an older hopsesh app updates itself in place to the newest
# release: the bundle swap that `hopsesh update` (and updating another machine from the app)
# does, which needs the signed, notarized disk image and so cannot run in CI. Run it right
# after publishing a release (`hopsesh update` reads GitHub's latest release):
#
#   scripts/app-update-check.sh v0.6.3        # the release before the one just published
#
# Works in a folder of its own; /Applications and the installed app are not touched.
set -eu

from=${1:?usage: app-update-check.sh <older tag>}
work=$(mktemp -d "${TMPDIR:-/tmp}/hopsesh-app-update.XXXXXX")
trap 'hdiutil detach -quiet "$work/mnt" 2>/dev/null || true; rm -rf "$work"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

latest=$(gh release view -R roeehrl/hopsesh --json tagName -q .tagName)
[ "$latest" != "$from" ] || fail "$from is the latest release; pass the one before it"

echo "==> $from's app, in $work/Applications"
gh release download "$from" -R roeehrl/hopsesh -p "hopsesh-${from#v}-macos-universal.dmg" -D "$work"
mkdir -p "$work/mnt" "$work/Applications"
hdiutil attach -quiet -nobrowse -readonly -mountpoint "$work/mnt" "$work"/hopsesh-*.dmg
ditto "$work/mnt/hopsesh.app" "$work/Applications/hopsesh.app"
hdiutil detach -quiet "$work/mnt"
cli="$work/Applications/hopsesh.app/Contents/Resources/bin/hopsesh"
"$cli" version | grep -q "hopsesh ${from#v} " || fail "the old app is not ${from#v}: $("$cli" version)"

echo "==> hopsesh update, from the old app's own command"
# A home of its own: the update refreshes the skill in agent folders it finds there.
mkdir -p "$work/home"
HOME="$work/home" HOPSESH_CONFIG_DIR="$work/config" HOPSESH_STATE_DIR="$work/state" "$cli" update --yes

echo "==> the app in its place is $latest, signed and accepted by macOS"
"$cli" version | grep -q "hopsesh ${latest#v} " || fail "after the update: $("$cli" version)"
codesign --verify --deep --strict "$work/Applications/hopsesh.app" || fail "the new app's signature does not verify"
codesign -dv "$work/Applications/hopsesh.app" 2>&1 | grep -q "TeamIdentifier=MKNM96EU7J" || fail "the new app is not signed by the release team"
spctl --assess --type execute "$work/Applications/hopsesh.app" || fail "macOS does not accept the new app"
for f in "$work/Applications"/* "$work/Applications"/.[!.]*; do
  if [ -e "$f" ] && [ "$f" != "$work/Applications/hopsesh.app" ]; then fail "the update left $f beside the app"; fi
done
echo "OK: $from → $latest in place"
