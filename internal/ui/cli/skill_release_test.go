package cli

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/devtools/clitree"
	"github.com/roeehrl/hopsesh/internal/ui/skill"
)

// update rewrites testdata/cli-tree.golden (scripts/update-cli-golden.sh).
var updateGolden = flag.Bool("update", false, "rewrite testdata/cli-tree.golden from the commands")

// cobraTree is the visible command tree as help shows it: the same parser reads pflag's
// usage lines here and a built program's help in internal/devtools/skillcheck.
func cobraTree(root *cobra.Command) clitree.Tree {
	t := clitree.Tree{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		cmd := clitree.Command{Path: c.CommandPath(), Flags: clitree.ParseFlags(c.LocalFlags().FlagUsages()),
			Global: clitree.ParseFlags(c.InheritedFlags().FlagUsages())}
		for _, sub := range c.Commands() {
			if sub.IsAvailableCommand() && !clitree.Skip[sub.Name()] {
				cmd.Children = append(cmd.Children, sub.Name())
				walk(sub)
			}
		}
		t[cmd.Path] = cmd
	}
	walk(root)
	return t
}

func renderedSkill(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := skill.Render(skill.NewParams("hopsesh", "0.0.0", []string{"Claude Code", "Codex"}, []string{"claude", "codex"}))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// Every command the skill runs exists, with the flags it gives it, and every flag in its
// tables exists on the commands the table is for.
func TestSkillNamesOnlyRealCommands(t *testing.T) {
	tree := cobraTree(NewRoot(new(bytes.Buffer), all.Registry()))
	for _, e := range clitree.CheckSkill(tree, "hopsesh", renderedSkill(t)) {
		t.Error(e)
	}
}

// Every visible flag is in the skill, or the allowlist says why agents need not know it.
func TestEveryVisibleFlagDocumented(t *testing.T) {
	tree := cobraTree(NewRoot(new(bytes.Buffer), all.Registry()))
	b, err := os.ReadFile(filepath.Join("testdata", "skill-undocumented-flags.txt"))
	if err != nil {
		t.Fatal(err)
	}
	allowed, errs := clitree.ParseAllowed(string(b))
	for _, e := range append(errs, clitree.CheckDocumented(tree, renderedSkill(t), allowed)...) {
		t.Error(e)
	}
}

// The visible command tree is the golden: a changed command or flag is a reviewed change
// (and the skill's checks above see it).
func TestCLIGolden(t *testing.T) {
	got := cobraTree(NewRoot(new(bytes.Buffer), all.Registry())).Golden()
	path := filepath.Join("testdata", "cli-tree.golden")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if w := strings.ReplaceAll(string(want), "\r\n", "\n"); got != w {
		t.Errorf("the command tree differs from %s; if intended, run scripts/update-cli-golden.sh and review the diff.\n%s", path, clitree.Diff(w, got))
	}
}
