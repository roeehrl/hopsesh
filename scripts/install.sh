#!/bin/sh
# Install the hopsesh command-line tool on macOS or Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/roeehrl/hopsesh/main/scripts/install.sh | sh
#
# Environment: HOPSESH_VERSION (default: latest release), HOPSESH_INSTALL_DIR
# (default: ~/.local/bin). The archive is checked against the release's checksums.txt,
# and checksums.txt against the release signing key below (with openssl).
set -eu

REPO="roeehrl/hopsesh"
# Public half of the key that signs checksums.txt (packaging/release-key.pub,
# docs/RELEASING.md).
RELEASE_PUBKEY="-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEv/ATZNvxb7prYETkiikx+XrVirJJ
rDHoF6azgJ5jvnMcjc6nMWhY25liChLfhNbvP0GTOftoE6OfFuH3claq/g==
-----END PUBLIC KEY-----"

say() { printf '%s\n' "$*" >&2; }
die() { say "hopsesh install: $*"; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

need curl
need tar
case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) die "unsupported system $(uname -s) (on Windows use install.ps1)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

tag="${HOPSESH_VERSION:-}"
if [ -z "$tag" ]; then
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") || die "cannot reach GitHub"
  tag=${url##*/}
fi
case "$tag" in v*) ;; *) tag="v$tag" ;; esac
version=${tag#v}
archive="hopsesh_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
say "Downloading hopsesh $version for $os/$arch…"
curl -fsSL -o "$tmp/$archive" "$base/$archive" || die "no $archive in release $tag"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || die "release $tag has no checksums.txt"

if [ -n "$RELEASE_PUBKEY" ]; then
  need openssl
  curl -fsSL -o "$tmp/checksums.txt.sig" "$base/checksums.txt.sig" || die "release $tag is not signed"
  printf '%s\n' "$RELEASE_PUBKEY" > "$tmp/release-key.pub"
  openssl dgst -sha256 -verify "$tmp/release-key.pub" -signature "$tmp/checksums.txt.sig" "$tmp/checksums.txt" >/dev/null 2>&1 \
    || die "checksums.txt signature is NOT valid; not installing"
else
  say "warning: this installer has no release key yet; checking the checksum only"
fi

want=$(awk -v f="$archive" '$2 == f || $2 == "*"f {print $1}' "$tmp/checksums.txt")
[ -n "$want" ] || die "checksums.txt has no entry for $archive"
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
else got=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1); fi
[ "$got" = "$want" ] || die "checksum mismatch for $archive; not installing"

tar -xzf "$tmp/$archive" -C "$tmp" hopsesh
dir="${HOPSESH_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$dir"
# A link into hopsesh.app belongs to the app (it updates with it): leave it alone.
if [ -L "$dir/hopsesh" ] && [ -z "${HOPSESH_FORCE:-}" ]; then
  case "$(readlink "$dir/hopsesh")" in
    *.app/Contents/*) die "$dir/hopsesh comes from the hopsesh app, which keeps it up to date; update the app instead (or rerun with HOPSESH_FORCE=1 to replace it)" ;;
  esac
fi
install -m 0755 "$tmp/hopsesh" "$dir/hopsesh.new" 2>/dev/null || { cp "$tmp/hopsesh" "$dir/hopsesh.new"; chmod 0755 "$dir/hopsesh.new"; }
mv -f "$dir/hopsesh.new" "$dir/hopsesh"
say "Installed $("$dir/hopsesh" version) at $dir/hopsesh"
case ":$PATH:" in
  *":$dir:"*) ;;
  *)
    case "$(basename "${SHELL:-sh}")" in
      zsh) profile="$HOME/.zprofile" ;;
      bash) profile="$HOME/.bash_profile" ;;
      *) profile="$HOME/.profile" ;;
    esac
    say "Add $dir to your PATH, e.g.:  echo 'export PATH=\"$dir:\$PATH\"' >> $profile   (then open a new terminal)" ;;
esac
say "Start with: hopsesh   (or hopsesh --help)"
