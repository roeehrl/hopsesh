#!/bin/sh
# Exercise hopsesh's actual generated manifest and all four reviewer inputs, without
# network probes, installations, paid review, credentials or issue writes.
set -eu
OUT=${1:?usage: test-hopsesh.sh OUTPUT_DIRECTORY}
DRIFT_OFFLINE=1 DRIFT_NO_INSTALL=1 ci/drift/probe.sh "$OUT"
jq -e '
  [.modules[] | select(.accounts != null)] as $profiles |
  ($profiles | length >= 2) and
  ($profiles | all(.integrationFiles | length > 0)) and
  any($profiles[]; .id == "codex" and .desktopScheme == "codex" and
    (.accounts.rootEnv | index("CODEX_SQLITE_HOME") != null)) and
  any($profiles[]; .id == "claude" and
    (.accounts.rootEnv | index("CLAUDE_CONFIG_DIR") != null))
' "$OUT/manifest.json" > /dev/null
mkdir -p "$OUT/prompts"
jq -r '.[].group' ci/drift/groups.json | while IFS= read -r group; do
  ci/drift/prompt.sh "$group" "$OUT" > "$OUT/prompts/$group.md"
  test -s "$OUT/prompts/$group.md"
  if grep -q '{{' "$OUT/prompts/$group.md"; then
    echo "Unexpanded review template: $group" >&2
    exit 1
  fi
done
