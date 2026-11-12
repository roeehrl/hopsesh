#!/bin/sh
# Rewrites internal/ui/cli/testdata/cli-tree.golden, the visible command tree (commands
# and their flags) that CI and the release hold the built program to, then checks the
# skill against it. Review the diff: a new visible flag needs the skill to name it, or an
# entry in internal/ui/cli/testdata/skill-undocumented-flags.txt saying why not.
set -eu
cd "$(dirname "$0")/.."
go test ./internal/ui/cli -run '^TestCLIGolden$' -count=1 -update
go test ./internal/ui/cli -run '^(TestCLIGolden|TestSkillNames|TestEveryVisibleFlagDocumented|TestSkillJSONPathsExist)' -count=1
git --no-pager diff --stat -- internal/ui/cli/testdata/cli-tree.golden
