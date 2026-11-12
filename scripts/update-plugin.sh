#!/bin/sh
# Rewrites plugin/ (the skill and both manifests' versions) for the newest release in
# CHANGELOG.md. Run it in the release's preparation, after its CHANGELOG section is written.
set -eu
cd "$(dirname "$0")/.."
go test ./internal/ui/skill -run '^TestPlugin$' -count=1 -update
go test ./internal/ui/skill -run '^TestPlugin$' -count=1
git --no-pager diff --stat -- plugin
