package app

import (
	"context"
	"errors"
	"os"
	"sort"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Rules are the hopsesh commands agents may run without asking (read-only) and the ones
// that always ask.
func Rules(bin string) agent.Rules {
	return agent.Rules{
		Bin:   bin,
		Allow: [][]string{{"ls"}, {"show"}, {"plan"}, {"agents"}, {"hosts", "--json"}, {"clouds", "--json"}, {"clouds", "test"}, {"doctor"}, {"version"}},
		Ask:   [][]string{{"pull"}, {"push"}, {"handoff"}, {"followup"}, {"undo"}, {"unblock"}, {"history"}, {"notices"}, {"clouds", "cleanup"}, {"clouds", "continue"}},
	}
}

// SkillCopy is one place the skill goes, shared by every agent that reads that folder.
type SkillCopy struct {
	Dir    string                `json:"dir"`
	Agents []string              `json:"agents"` // display names
	Status integrate.SkillStatus `json:"status"`
}

// RuleState is one agent's approval rules.
type RuleState struct {
	Agent   string `json:"agent"`
	Path    string `json:"path"`
	Present bool   `json:"present"`
}

// SkillReport is the skill and rules across every agent installed here.
type SkillReport struct {
	Copies []SkillCopy `json:"copies"`
	Rules  []RuleState `json:"rules"`
	InSync bool        `json:"inSync"` // every copy is current
	State  string      `json:"state"`  // the worst copy's state
}

type integration struct {
	name string
	in   agent.Install
	ig   agent.Integration
}

// integrations are the installed agents' integration points on this machine.
func (a *App) integrations(ctx context.Context) []integration {
	m := a.localMachine(ctx)
	var out []integration
	for _, mod := range a.Modules() {
		ig, ok := mod.(agent.Integrator)
		if !ok {
			continue
		}
		h, err := m.For(ctx, mod.Spec(), agent.Install{}, nil)
		if err != nil {
			continue
		}
		in, err := mod.Detect(ctx, h)
		if err != nil || !in.Present && in.Binary == "" {
			continue // not used on this machine
		}
		out = append(out, integration{name: mod.Spec().Name, in: in, ig: ig.Integration(h, in)})
	}
	return out
}

// severity orders states for the report (worst last).
var severity = map[string]int{integrate.Current: 0, integrate.Stale: 1, integrate.Absent: 2, integrate.Broken: 3, integrate.Modified: 4, integrate.Foreign: 5}

// Skill reports the skill copies and rules against files (this build's rendering).
func (a *App) Skill(ctx context.Context, files map[string][]byte, bin string) SkillReport {
	var rep SkillReport
	by := map[string]*SkillCopy{}
	for _, it := range a.integrations(ctx) {
		dir := it.ig.SkillDir + string(os.PathSeparator) + integrate.SkillName
		c, ok := by[dir]
		if !ok {
			c = &SkillCopy{Dir: dir, Status: integrate.CheckSkill(dir, files)}
			by[dir] = c
		}
		c.Agents = append(c.Agents, it.name)
		if r := it.ig.Rules; r != nil {
			rep.Rules = append(rep.Rules, RuleState{Agent: it.name, Path: r.Path, Present: rulesPresent(r, Rules(bin))})
		}
	}
	rep.InSync, rep.State = true, integrate.Current
	for _, c := range by {
		rep.Copies = append(rep.Copies, *c)
		if c.Status.State != integrate.Current {
			rep.InSync = false
		}
		if severity[c.Status.State] > severity[rep.State] {
			rep.State = c.Status.State
		}
	}
	sort.Slice(rep.Copies, func(i, j int) bool { return rep.Copies[i].Dir < rep.Copies[j].Dir })
	if len(rep.Copies) == 0 {
		rep.InSync, rep.State = false, integrate.Absent
	}
	return rep
}

func rulesPresent(rf *agent.RuleFile, r agent.Rules) bool {
	b, err := os.ReadFile(rf.Path)
	if err != nil {
		return false
	}
	if rf.Owned {
		return string(b) == string(rf.Render(r))
	}
	return rf.Has(b, r)
}

// InstallSkill writes the same files into every agent's skills folder in one go (and,
// with rules, each agent's approval rules). A copy the user edited stops the install
// unless force.
func (a *App) InstallSkill(ctx context.Context, files map[string][]byte, version, bin string, force, rules bool) (SkillReport, error) {
	its := a.integrations(ctx)
	if len(its) == 0 {
		return SkillReport{}, errors.New("no supported agent is installed on this machine")
	}
	// Check every copy first, so an edited copy stops the install before anything changes.
	seen := map[string]bool{}
	for _, it := range its {
		dir := it.ig.SkillDir + string(os.PathSeparator) + integrate.SkillName
		if st := integrate.CheckSkill(dir, files); !force && (st.State == integrate.Modified || st.State == integrate.Foreign) {
			if st.State == integrate.Modified {
				return a.Skill(ctx, files, bin), errors.Join(integrate.ErrModified, errors.New(dir+" (use --force; the old copy is kept)"))
			}
			return a.Skill(ctx, files, bin), errors.Join(integrate.ErrForeign, errors.New(dir+" (use --force; the old copy is kept)"))
		}
	}
	for _, it := range its {
		dir := it.ig.SkillDir + string(os.PathSeparator) + integrate.SkillName
		if !seen[dir] {
			seen[dir] = true
			if _, err := integrate.InstallSkill(dir, version, files, force); err != nil {
				return a.Skill(ctx, files, bin), err
			}
		}
		if rf := it.ig.Rules; rules && rf != nil {
			var err error
			if rf.Owned {
				err = integrate.WriteOwned(rf.Path, rf.Render(Rules(bin)))
			} else {
				err = integrate.MergeShared(rf.Path, func(b []byte) ([]byte, error) { return rf.Merge(b, Rules(bin)) })
			}
			if err != nil {
				return a.Skill(ctx, files, bin), err
			}
		}
	}
	a.Audit.Write(audit.Entry{Action: "skill.install", Detail: map[string]any{"copies": len(seen), "rules": rules}})
	return a.Skill(ctx, files, bin), nil
}

// RemoveSkill removes every copy hopsesh owns, and the approval rules.
func (a *App) RemoveSkill(ctx context.Context, files map[string][]byte, bin string, force bool) error {
	var errs []error
	seen := map[string]bool{}
	for _, it := range a.integrations(ctx) {
		dir := it.ig.SkillDir + string(os.PathSeparator) + integrate.SkillName
		if !seen[dir] {
			seen[dir] = true
			if err := integrate.RemoveSkill(dir, files, force); err != nil {
				errs = append(errs, err)
			}
		}
		rf := it.ig.Rules
		if rf == nil {
			continue
		}
		if rf.Owned {
			if err := os.Remove(rf.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		} else if _, err := os.Stat(rf.Path); err == nil {
			if err := integrate.MergeShared(rf.Path, func(b []byte) ([]byte, error) { return rf.Remove(b, Rules(bin)) }); err != nil {
				errs = append(errs, err)
			}
		}
	}
	a.Audit.Write(audit.Entry{Action: "skill.remove", Detail: map[string]any{"ok": len(errs) == 0}})
	return errors.Join(errs...)
}
