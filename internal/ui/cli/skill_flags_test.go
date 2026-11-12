package cli

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/ui/skill"
)

// Every flag the skill teaches agents exists on some command: a removed flag (--fork)
// would make an agent's command fail.
func TestSkillNamesOnlyRealFlags(t *testing.T) {
	defined := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		add := func(f *pflag.Flag) { defined[f.Name] = true }
		c.Flags().VisitAll(add)
		c.PersistentFlags().VisitAll(add)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(NewRoot(new(bytes.Buffer), all.Registry()))
	files, err := skill.Render(skill.NewParams("hopsesh", "0.0.0", []string{"Claude Code", "Codex"}, []string{"claude", "codex"}))
	if err != nil {
		t.Fatal(err)
	}
	flag := regexp.MustCompile("(?:^|[\\s`(|/\\[])--([a-z][a-z-]*)")
	for name, b := range files {
		for _, m := range flag.FindAllSubmatch(b, -1) {
			if !defined[string(m[1])] {
				t.Errorf("%s names --%s, which no hopsesh command has", name, m[1])
			}
		}
	}
}
