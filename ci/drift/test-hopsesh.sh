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
# The baselines sit at or above the code's version floors; the desktop apps, the formats
# and the probe's per-target tables are there.
jq -e '
  ([.targets[] | select(.id == "claude" or .id == "codex") | (.floors | length > 0) and .baseline != .tested] | all) and
  ([.targets[] | select(.kind == "app") | .id] | sort == ["claude-desktop", "codex-desktop"]) and
  ([.targets[].watch.formats // [] | .[].id] | index("codex-session-meta") != null and index("claude-live-registry") != null and
    index("claude-remote-control-default") != null and index("claude-desktop-handoff") != null)
' "$OUT/manifest.json" > /dev/null
jq -e 'map(select(.id == "claude")) | .[0].baseline == "2.1.300"' "$OUT/versions.json" > /dev/null
for id in claude codex claude-desktop codex-desktop; do test -s "$OUT/formats/$id.tsv"; done
grep -q '^auth status	' "$OUT/help/claude/relies.tsv" 2> /dev/null || ! command -v claude > /dev/null
mkdir -p "$OUT/prompts"
jq -r '.[].group' ci/drift/groups.json | while IFS= read -r group; do
  ci/drift/prompt.sh "$group" "$OUT" > "$OUT/prompts/$group.md"
  test -s "$OUT/prompts/$group.md"
  if grep -q '{{' "$OUT/prompts/$group.md"; then
    echo "Unexpanded review template: $group" >&2
    exit 1
  fi
done
