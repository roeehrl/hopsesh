#!/bin/sh
# Writes the Homebrew formula and cask and the Scoop manifest for a published release, from
# its checksums.txt (the signed list scripts/release-sign.sh uploads, which names every file
# of the release). .github/workflows/packages.yml runs it when a release is published and
# pushes the files to roeehrl/homebrew-tap and roeehrl/scoop-bucket.
#
#	scripts/package-manifests.sh 0.6.4 checksums.txt out/
set -eu
v=$1 sums=$2 out=$3
url="https://github.com/roeehrl/hopsesh/releases/download/v$v"
desc="Move Claude Code and Codex sessions between machines and agents"
sha() {
	h=$(awk -v f="$1" '$2 == f { print $1 }' "$sums")
	[ -n "$h" ] || { echo "checksums.txt has no $1" >&2; exit 1; }
	echo "$h"
}
mkdir -p "$out/Formula" "$out/Casks" "$out/bucket"

cat >"$out/Formula/hopsesh.rb" <<RB
# Written by hopsesh's release workflow (scripts/package-manifests.sh); edits are overwritten.
class Hopsesh < Formula
  desc "$desc"
  homepage "https://github.com/roeehrl/hopsesh"
  version "$v"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "$url/hopsesh_${v}_darwin_arm64.tar.gz"
      sha256 "$(sha "hopsesh_${v}_darwin_arm64.tar.gz")"
    end
    on_intel do
      url "$url/hopsesh_${v}_darwin_amd64.tar.gz"
      sha256 "$(sha "hopsesh_${v}_darwin_amd64.tar.gz")"
    end
  end

  on_linux do
    on_arm do
      url "$url/hopsesh_${v}_linux_arm64.tar.gz"
      sha256 "$(sha "hopsesh_${v}_linux_arm64.tar.gz")"
    end
    on_intel do
      url "$url/hopsesh_${v}_linux_amd64.tar.gz"
      sha256 "$(sha "hopsesh_${v}_linux_amd64.tar.gz")"
    end
  end

  def install
    bin.install "hopsesh"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/hopsesh version")
  end
end
RB

cat >"$out/Casks/hopsesh-app.rb" <<RB
# Written by hopsesh's release workflow (scripts/package-manifests.sh); edits are overwritten.
cask "hopsesh-app" do
  version "$v"
  sha256 "$(sha "hopsesh-${v}-macos-universal.dmg")"

  url "$url/hopsesh-#{version}-macos-universal.dmg"
  name "hopsesh"
  desc "$desc"
  homepage "https://codonic.dev/apps/hopsesh"

  auto_updates true
  depends_on macos: :ventura

  app "hopsesh.app"
end
RB

cat >"$out/bucket/hopsesh.json" <<JSON
{
    "version": "$v",
    "description": "$desc",
    "homepage": "https://github.com/roeehrl/hopsesh",
    "license": "Apache-2.0",
    "architecture": {
        "64bit": {
            "url": "$url/hopsesh_${v}_windows_amd64.zip",
            "hash": "$(sha "hopsesh_${v}_windows_amd64.zip")"
        },
        "arm64": {
            "url": "$url/hopsesh_${v}_windows_arm64.zip",
            "hash": "$(sha "hopsesh_${v}_windows_arm64.zip")"
        }
    },
    "bin": "hopsesh.exe",
    "checkver": "github",
    "autoupdate": {
        "architecture": {
            "64bit": { "url": "https://github.com/roeehrl/hopsesh/releases/download/v\$version/hopsesh_\$version_windows_amd64.zip" },
            "arm64": { "url": "https://github.com/roeehrl/hopsesh/releases/download/v\$version/hopsesh_\$version_windows_arm64.zip" }
        }
    }
}
JSON
