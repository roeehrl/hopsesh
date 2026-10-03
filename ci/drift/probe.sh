#!/bin/sh
# Weekly upstream-drift probe: what changed in Claude Code and Codex since the versions
# hopsesh is tested with. No model, no secrets (GH_TOKEN reads public releases). It
# writes intel/ for the review: probe.md (the facts), manifest.json (what the modules
# rely on), the Codex app-server schema changes, real-agent test results, and the agents'
# changelogs, releases and docs. Run from the repository root; needs node, go and gh.
set -u
OUT=${1:-intel}
rm -rf "$OUT"
mkdir -p "$OUT/sources" "$OUT/schema"
note() { printf '%s\n' "$@" >> "$OUT/probe.md"; }
count() { find "$1" -type f -name "${2:-*}" | wc -l | tr -d ' '; }
newest() { find "$1" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | sort -t. -k1,1n -k2,2n -k3,3n | tail -n 1; }
fetch() { curl -fsSL --retry 3 "$1" -o "$OUT/sources/$2" || note "- could not fetch $1"; }

note "# Upstream drift probe, $(date -u +%Y-%m-%d)"
go run ./internal/devtools/driftmanifest > "$OUT/manifest.json"

# Versions: the newest fixture folder is the exact version each module was tested with.
CLAUDE_TESTED=$(newest agents/claude/testdata)
CODEX_TESTED=$(newest agents/codex/testdata)
CLAUDE_LATEST=$(npm view @anthropic-ai/claude-code version)
CODEX_LATEST=$(npm view @openai/codex version)
note "" "## Versions" "" "| agent | tested | latest |" "|---|---|---|"
note "| Claude Code | $CLAUDE_TESTED | $CLAUDE_LATEST |" "| Codex | $CODEX_TESTED | $CODEX_LATEST |"
jq -n --arg ct "$CLAUDE_TESTED" --arg cl "$CLAUDE_LATEST" --arg xt "$CODEX_TESTED" --arg xl "$CODEX_LATEST" \
  '{claude: {tested: $ct, latest: $cl}, codex: {tested: $xt, latest: $xl}}' > "$OUT/versions.json"

# The latest CLIs, in a private prefix.
PREFIX=$(mktemp -d)
npm install -g --silent --prefix "$PREFIX" "@anthropic-ai/claude-code@$CLAUDE_LATEST" "@openai/codex@$CODEX_LATEST" >/dev/null 2>&1 \
  || note "- could not install the latest CLIs"
PATH=$PREFIX/bin:$PATH
export PATH
note "" "## Version output of the latest CLIs (hopsesh parses these)" "" '```'
claude --version >> "$OUT/probe.md" 2>&1
codex --version >> "$OUT/probe.md" 2>&1
note '```'

# Codex app-server: the JSON-RPC protocol hopsesh speaks, tested version against latest.
SCRATCH=$(mktemp -d)
mkdir -p "$SCRATCH/home"
CODEX_HOME=$SCRATCH/home npx -y "@openai/codex@$CODEX_TESTED" app-server generate-json-schema --out "$SCRATCH/tested" >/dev/null 2>&1
CODEX_HOME=$SCRATCH/home codex app-server generate-json-schema --out "$SCRATCH/latest" >/dev/null 2>&1
note "" "## Codex app-server protocol, $CODEX_TESTED → $CODEX_LATEST" ""
if [ -d "$SCRATCH/tested" ] && [ -d "$SCRATCH/latest" ]; then
  diff -rq "$SCRATCH/tested" "$SCRATCH/latest" | sed "s|$SCRATCH/||g" > "$OUT/schema/changed-files.txt"
  note "$(wc -l < "$OUT/schema/changed-files.txt" | tr -d ' ') schema files differ (list: schema/changed-files.txt)."
  # Full diffs only for what hopsesh uses: initialize, threads, accounts, the importer.
  for f in "$SCRATCH"/latest/* "$SCRATCH"/tested/*; do
    n=$(basename "$f")
    case $n in Initialize*|Thread*|Account*|ExternalAgentConfig*|ClientRequest.json|ServerNotification.json) ;; *) continue ;; esac
    [ -f "$OUT/schema/$n.diff" ] && continue
    diff -u "$SCRATCH/tested/$n" "$SCRATCH/latest/$n" > "$OUT/schema/$n.diff" 2>&1 || true
    [ -s "$OUT/schema/$n.diff" ] || rm -f "$OUT/schema/$n.diff"
  done
  note "Diffs of the files hopsesh relies on: $(count "$OUT/schema" '*.diff') (schema/*.diff)."
else
  note "- generate-json-schema did not run for both versions"
fi
rm -rf "$SCRATCH"

# hopsesh's tests against the real latest Codex (no model calls).
note "" "## Real-agent tests against Codex $CODEX_LATEST" "" '```'
HOPSESH_REAL_AGENTS=1 go test ./internal/e2e/ -run 'TestCodex' -count=1 > "$OUT/real-agents.txt" 2>&1
tail -n 25 "$OUT/real-agents.txt" >> "$OUT/probe.md"
note '```'

# What the vendors say: changelogs, releases and the docs pages the modules depend on.
# Changelogs only since the tested versions.
if curl -fsSL --retry 3 https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md -o "$OUT/changelog.tmp"; then
  awk -v stop="## $CLAUDE_TESTED" '$0 == stop { exit } { print }' "$OUT/changelog.tmp" \
    > "$OUT/sources/claude-code-changelog-since-$CLAUDE_TESTED.md"
else
  note "- could not fetch the Claude Code changelog"
fi
rm -f "$OUT/changelog.tmp"
RELEASES=$OUT/sources/codex-releases-since-$CODEX_TESTED.jsonl
: > "$RELEASES"
for page in 1 2 3 4 5 6 7 8; do
  # Small pages: a page of 100 releases with their notes times out at GitHub.
  if ! gh api "repos/openai/codex/releases?per_page=25&page=$page" > "$OUT/page.tmp" \
    && ! { sleep 5; gh api "repos/openai/codex/releases?per_page=25&page=$page" > "$OUT/page.tmp"; }; then
    note "- could not read Codex releases (page $page)"
    break
  fi
  jq -c --arg stop "rust-v$CODEX_TESTED" \
    '(map(.tag_name) | index($stop)) as $end
     | if $end == null then error("tested release not on this page") else .[:$end][] end
     | select(.prerelease == false) | {tag: .tag_name, published: .published_at, notes: .body}' \
    "$OUT/page.tmp" >> "$RELEASES" 2>/dev/null && break
  jq -c '.[] | select(.prerelease == false) | {tag: .tag_name, published: .published_at, notes: .body}' "$OUT/page.tmp" >> "$RELEASES"
done
rm -f "$OUT/page.tmp"
for p in sessions settings skills hooks headless cli-reference claude-directory permissions; do
  fetch "https://code.claude.com/docs/en/$p.md" "claude-code-docs-$p.md"
done
for p in app-server config-file/config-reference build-skills agent-configuration/agents-md non-interactive-mode; do
  fetch "https://learn.chatgpt.com/docs/$p.md" "codex-docs-$(echo "$p" | tr / -).md"
done
note "" "Sources fetched: $(count "$OUT/sources") (sources/)."
