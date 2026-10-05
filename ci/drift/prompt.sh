#!/bin/sh
# Prints the drift review prompt for one group of targets: prompt.md, filled in from the
# probe's manifest (the project, the group's targets and their words), with the group's
# focus file. FOCUS is the folder that holds <group>.md: hopsesh's focus/ by default, a
# caller's own folder when the reusable workflow runs for another repository.
# Usage: ci/drift/prompt.sh GROUP [INTEL] [FOCUS]
set -eu
G=$1
M=${2:-intel}/manifest.json
D=$(dirname "$0")
F=${3:-$D/focus}
[ -f "$F/$G.md" ] || { echo "prompt.sh: no $G.md in $F" >&2; exit 1; }
jq -e --arg g "$G" 'any(.targets[]; .group == $g)' "$M" > /dev/null || { echo "prompt.sh: no targets in group $G" >&2; exit 1; }

PROJECT=$(jq -r .project.name "$M")
ABOUT=$(jq -r .project.about "$M")
CITE=$(jq -r .project.cite "$M")
TARGETS=$(jq -r --arg g "$G" '.targets[] | select(.group == $g)
  | "- `\(.id)`: \(.name), \(.vendor) (\(.kind), \(.priority) priority, tested \(if .tested == "" then "none" else .tested end)). It covers \(.surface)."' "$M")
GREP=$(jq -r --arg g "$G" '[.targets[] | select(.group == $g) | .watch.grep | gsub("\\\\"; "") | split("|")[]]
  | unique | map("`\(.)`") | join(", ")' "$M")
FOCUS=$(cat "$F/$G.md")
export PROJECT ABOUT CITE TARGETS GREP FOCUS

# Literal replacement: the values may hold & and \, which gsub would interpret.
awk 'function put(s, k, v,   i, out) {
       out = ""
       while ((i = index(s, k)) > 0) { out = out substr(s, 1, i - 1) v; s = substr(s, i + length(k)) }
       return out s
     }
     { s = put($0, "{{focus}}", ENVIRON["FOCUS"]); s = put(s, "{{targets}}", ENVIRON["TARGETS"])
       s = put(s, "{{grep}}", ENVIRON["GREP"]); s = put(s, "{{about}}", ENVIRON["ABOUT"])
       s = put(s, "{{cite}}", ENVIRON["CITE"]); print put(s, "{{project}}", ENVIRON["PROJECT"]) }' "$D/prompt.md"
