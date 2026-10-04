package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Cloud environments (Codex cloud runs every task in one the user made on the web): the
// one configured for a repository, the ones the cloud's listing shows as suggestions, and
// the repository a listed session works on, read back from the configuration.

// EnvChoices are the environments a hand-off to a cloud can run in, for a repository: the
// one configured for it first, then the ones the cloud's listing shows (the most used
// first), then the ones configured for other repositories.
func (a *App) EnvChoices(inv *Inventory, cloud, repo string) []move.EnvChoice {
	set := a.Cfg.CloudSettings(cloud)
	type seen struct {
		c     move.EnvChoice
		order int
	}
	byKey := map[string]*seen{}
	var all []*seen
	add := func(value, name string) *seen {
		key := strings.ToLower(nonEmpty(value, name))
		for _, k := range []string{strings.ToLower(value), strings.ToLower(name)} {
			if s := byKey[k]; k != "" && s != nil {
				if s.c.Name == s.c.Value && name != "" {
					s.c.Name = name
				}
				return s
			}
		}
		s := &seen{c: move.EnvChoice{Value: nonEmpty(value, name), Name: nonEmpty(name, value)}, order: len(all)}
		byKey[key] = s
		if name != "" {
			byKey[strings.ToLower(name)] = s
		}
		all = append(all, s)
		return s
	}
	if env := set.Environments[repo]; repo != "" && env != "" {
		add(env, "").c.Configured = true
	}
	if inv != nil {
		for _, e := range inv.Entries {
			if c := e.Cloud; c != nil && e.Location.Name == cloud && (c.Env != "" || c.EnvLabel != "") {
				add(c.Env, c.EnvLabel).c.Tasks++
			}
		}
	}
	others := map[string][]string{}
	for r, env := range set.Environments {
		if r != repo && env != "" {
			s := add(env, "")
			others[s.c.Value] = append(others[s.c.Value], r)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		x, y := all[i], all[j]
		if x.c.Configured != y.c.Configured {
			return x.c.Configured
		}
		if x.c.Tasks != y.c.Tasks {
			return x.c.Tasks > y.c.Tasks
		}
		return x.order < y.order
	})
	noun := "session"
	if _, cl, ok := a.cloudModule(cloud); ok {
		noun = cl.SessionNoun()
	}
	out := make([]move.EnvChoice, 0, len(all))
	for _, s := range all {
		c := s.c
		var why []string
		if c.Configured {
			why = append(why, "set for this repository")
		}
		switch {
		case c.Tasks == 1:
			why = append(why, "used by 1 of your recent "+noun+"s")
		case c.Tasks > 1:
			why = append(why, fmt.Sprintf("used by %d of your recent %ss", c.Tasks, noun))
		}
		if rs := others[c.Value]; len(rs) > 0 && !c.Configured {
			sort.Strings(rs)
			why = append(why, "set for "+strings.Join(rs, ", "))
		}
		c.Label = c.Name
		if len(why) > 0 {
			c.Label += " (" + strings.Join(why, "; ") + ")"
		}
		out = append(out, c)
	}
	return out
}

// repoForEnv is the repository the configuration sets an environment for, when exactly one
// has it (a listed session names its environment, not its repository).
func repoForEnv(envs map[string]string, s agent.CloudSession) string {
	found := ""
	for repo, env := range envs {
		if env == "" || env != s.Env && !strings.EqualFold(env, s.EnvLabel) {
			continue
		}
		if found != "" {
			return ""
		}
		found = repo
	}
	return found
}

// RepoEnv is one repository's environment in a cloud, for the Machines page's table.
type RepoEnv struct {
	Repo string `json:"repo"`
	Env  string `json:"env,omitempty"` // configured ("": the plan asks)
	// Unsupported says why the cloud cannot take the repository ("" when it can).
	Unsupported string `json:"unsupported,omitempty"`
}

// RepoEnvs are the repositories hopsesh knows (configured for the cloud, or a session's
// checkout) with the environment configured for each, for a cloud that needs one.
func (a *App) RepoEnvs(inv *Inventory, cloud string) []RepoEnv {
	_, cl, ok := a.cloudModule(cloud)
	if !ok || !needsEnv(cl) {
		return nil
	}
	set := a.Cfg.CloudSettings(cloud)
	repos := map[string]bool{}
	for r := range set.Environments {
		repos[r] = true
	}
	if inv != nil {
		for _, e := range inv.Entries {
			if g := e.Git; g != nil && g.Identity != "" && !e.Location.IsCloud() {
				repos[g.Identity] = true
			}
		}
	}
	var out []RepoEnv
	for r := range repos {
		re := RepoEnv{Repo: r, Env: set.Environments[r]}
		if host, _, _ := strings.Cut(r, "/"); !containsStr(cl.Hosts, host) {
			re.Unsupported = "not on " + hostsWords(cl.Hosts)
		}
		out = append(out, re)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Unsupported == "") != (out[j].Unsupported == "") {
			return out[i].Unsupported == ""
		}
		return out[i].Repo < out[j].Repo
	})
	return out
}

func needsEnv(cl agent.Cloud) bool {
	for _, n := range cl.Needs {
		if n == agent.NeedEnvironment {
			return true
		}
	}
	return false
}

// hostsWords names repository hosts for people ("GitHub").
func hostsWords(hosts []string) string {
	out := make([]string, len(hosts))
	for i, h := range hosts {
		out[i] = h
		if h == "github.com" {
			out[i] = "GitHub"
		}
	}
	return strings.Join(out, ", ")
}
