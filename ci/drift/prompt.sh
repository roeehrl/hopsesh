#!/bin/sh
# Prints the drift review prompt for one group of targets: prompt.md, filled in from the
# probe's manifest (the modules, the group's targets and their words), with the group's
# focus/<group>.md. Usage: ci/drift/prompt.sh GROUP [INTEL]
set -eu
G=$1
M=${2:-intel}/manifest.json
D=$(dirname "$0")
[ -f "$D/focus/$G.md" ] || { echo "prompt.sh: no focus/$G.md" >&2; exit 1; }
jq -e --arg g "$G" 'any(.targets[]; .group == $g)' "$M" > /dev/null || { echo "prompt.sh: no targets in group $G" >&2; exit 1; }

MODULES=$(jq -r '[.modules[] | "`agents/\(.id)` (\(.name))"]
  | if length > 1 then (.[:-1] | join(", ")) + " and " + .[-1] else .[0] end' "$M")
TARGETS=$(jq -r --arg g "$G" '.targets[] | select(.group == $g)
  | "- `\(.id)`: \(.name), \(.vendor) (\(.kind), \(.priority) priority, tested \(if .tested == "" then "none" else .tested end)). It covers \(.surface)."' "$M")
GREP=$(jq -r --arg g "$G" '[.targets[] | select(.group == $g) | .watch.grep | gsub("\\\\"; "") | split("|")[]]
  | unique | map("`\(.)`") | join(", ")' "$M")
FOCUS=$(cat "$D/focus/$G.md")
export MODULES TARGETS GREP FOCUS

# Literal replacement: the values may hold & and \, which gsub would interpret.
awk 'function put(s, k, v,   i, out) {
       out = ""
       while ((i = index(s, k)) > 0) { out = out substr(s, 1, i - 1) v; s = substr(s, i + length(k)) }
       return out s
     }
     { s = put($0, "{{modules}}", ENVIRON["MODULES"]); s = put(s, "{{targets}}", ENVIRON["TARGETS"])
       s = put(s, "{{grep}}", ENVIRON["GREP"]); print put(s, "{{focus}}", ENVIRON["FOCUS"]) }' "$D/prompt.md"
