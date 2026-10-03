package codex

import (
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Integration says Codex reads user skills from ~/.agents/skills, and approval rules from any
// .rules file under $CODEX_HOME/rules. hopsesh owns rules/hopsesh.rules outright (Codex
// rewrites default.rules itself).
func (m *Module) Integration(h agent.Host, in agent.Install) agent.Integration {
	pa := h.Path()
	return agent.Integration{
		SkillDir: pa.Join(h.Facts().Home, ".agents", "skills"),
		Rules:    &agent.RuleFile{Path: pa.Join(in.Root(home), "rules", "hopsesh.rules"), Owned: true, Render: renderRules},
	}
}

// renderRules writes one prefix rule per command: an explicit allow lets Codex run the
// command without asking and outside its sandbox (hopsesh needs SSH), so only read-only
// commands are allowed, and moves always ask.
func renderRules(r agent.Rules) []byte {
	var b strings.Builder
	b.WriteString("# Managed by hopsesh: which hopsesh commands Codex may run without asking.\n")
	b.WriteString("# Remove with: hopsesh skill remove\n")
	for _, p := range r.Allow {
		fmt.Fprintf(&b, "prefix_rule(pattern=%s, decision=\"allow\")\n", starlarkList(append([]string{r.Bin}, p...)))
	}
	for _, p := range r.Ask {
		fmt.Fprintf(&b, "prefix_rule(pattern=%s, decision=\"prompt\")\n", starlarkList(append([]string{r.Bin}, p...)))
	}
	return []byte(b.String())
}

func starlarkList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(x) + `"`
	}
	return "[" + strings.Join(q, ", ") + "]"
}
