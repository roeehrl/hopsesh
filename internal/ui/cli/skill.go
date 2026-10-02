package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/ui/skill"
	"github.com/roeehrl/hopsesh/internal/version"
)

// skillFiles renders this build's skill for the enabled agents.
func (r *run) skillFiles() (map[string][]byte, string) {
	bin := integrate.SkillBin()
	var names, ids []string
	for _, m := range r.app.Modules() {
		names = append(names, m.Spec().Name)
		ids = append(ids, string(m.Spec().ID))
	}
	files, _ := skill.Render(skill.NewParams(bin, version.Version, names, ids))
	return files, bin
}

func skillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Let your agents use hopsesh: install, update or remove the hopsesh skill",
		Long: `The hopsesh skill teaches your agents (Claude Code, Codex) to list your sessions, bring one
here or continue it in another agent with hopsesh: it always shows you the plan first and
acts only after you say yes. The same files go into every installed agent's skills folder,
and hopsesh reports any copy that drifted.

  hopsesh skill            show whether every copy is installed and up to date
  hopsesh skill install    install or update every copy (--add-rules: also let the agents run
                           hopsesh's read-only commands without asking; moves still ask)
  hopsesh skill remove     remove every copy and the rules`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			files, bin := r.skillFiles()
			rep := r.app.Skill(context.Background(), files, bin)
			if r.jsonOut {
				return r.emitJSON(rep)
			}
			r.printSkill(rep)
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "output JSON")
	install := &cobra.Command{
		Use:     "install",
		Aliases: []string{"update"},
		Short:   "Install or update the hopsesh skill for every agent",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool("force")
			rules, _ := cmd.Flags().GetBool("add-rules")
			files, bin := r.skillFiles()
			rep, err := r.app.InstallSkill(context.Background(), files, version.Version, bin, force, rules)
			if r.jsonOut {
				out := map[string]any{"skill": rep}
				if err != nil {
					out["error"] = err.Error()
				}
				_ = r.emitJSON(out)
				return err
			}
			switch {
			case errors.Is(err, integrate.ErrModified):
				return fmt.Errorf("%w; keep your edits, or replace them with: hopsesh skill install --force (your version is kept as a backup)", err)
			case errors.Is(err, integrate.ErrForeign):
				return fmt.Errorf("%w; replace it with --force (it is kept as a backup)", err)
			case err != nil:
				return err
			}
			r.printSkill(rep)
			r.printf("Try it: ask your agent \"what sessions do I have on my other machines?\" or \"continue this in Codex\".\n")
			r.app.Cfg.SkillPrompt = ""
			_ = config.Save(r.app.Cfg)
			return nil
		},
	}
	install.Flags().Bool("force", false, "replace copies you edited (or that hopsesh did not install); old ones are kept as backups")
	install.Flags().Bool("add-rules", false, "also add approval rules: read-only hopsesh commands run without asking, moves always ask")
	install.Flags().Bool("json", false, "output JSON")
	remove := &cobra.Command{
		Use:   "remove",
		Short: "Remove the hopsesh skill and its approval rules from every agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool("force")
			files, bin := r.skillFiles()
			if err := r.app.RemoveSkill(context.Background(), files, bin, force); err != nil {
				return err
			}
			r.printf("Removed the hopsesh skill and its rules.\n")
			return nil
		},
	}
	remove.Flags().Bool("force", false, "remove copies even if you edited them")
	cmd.AddCommand(install, remove)
	return cmd
}

func (r *run) printSkill(rep app.SkillReport) {
	if len(rep.Copies) == 0 {
		r.printf("No supported agent is installed on this machine.\n")
		return
	}
	r.printf("%s\n", skillLine(rep))
	for _, c := range rep.Copies {
		r.printf("  %-10s %s (%s)\n", c.Status.State, c.Status.Dir, strings.Join(c.Agents, ", "))
	}
	for _, rs := range rep.Rules {
		state := "not added (the agent asks before every hopsesh command)"
		if rs.Present {
			state = "read-only commands allowed, moves ask"
		}
		r.printf("  rules      %s: %s\n", rs.Agent, state)
	}
}

func skillLine(rep app.SkillReport) string {
	switch rep.State {
	case integrate.Current:
		return "The hopsesh skill is installed and in sync for every agent."
	case integrate.Absent:
		return "Not installed for every agent. Install with: hopsesh skill install"
	case integrate.Stale:
		return "Out of date; update with: hopsesh skill install"
	case integrate.Modified:
		return "A copy was edited and is out of sync; hopsesh leaves it alone (hopsesh skill install --force replaces it)"
	case integrate.Foreign:
		return "A skill called hopsesh exists that hopsesh did not install"
	case integrate.Broken:
		return "A copy is damaged; repair with: hopsesh skill install"
	}
	return rep.State
}

// offerSkill offers the skill once, and updates when it is out of date. Interactive
// sessions only, never with JSON output.
func (r *run) offerSkill() {
	if r.jsonOut || !r.interactive() {
		return
	}
	files, bin := r.skillFiles()
	rep := r.app.Skill(context.Background(), files, bin)
	switch rep.State {
	case integrate.Absent:
		if r.app.Cfg.SkillPrompt == "declined" || len(rep.Copies) == 0 {
			return
		}
		r.printf("\nYour agents can use hopsesh for you (\"bring my laptop session here\", \"continue this in Codex\"): they show the plan and act only after you say yes.\n")
		if !r.confirm("Install the hopsesh skill for your agents?") {
			r.app.Cfg.SkillPrompt = "declined"
			_ = config.Save(r.app.Cfg)
			r.printf("OK. You can install it later with: hopsesh skill install\n")
			return
		}
		rules := r.confirm("Also let them run hopsesh's read-only commands (ls, show, plan) without asking? Moves always ask.")
		if _, err := r.app.InstallSkill(context.Background(), files, version.Version, bin, false, rules); err != nil {
			r.printf("Could not install the skill: %v\n", err)
		} else {
			r.printf("Installed. Open agent sessions may need to reload their skills to see it.\n")
		}
	case integrate.Stale:
		if r.app.Cfg.SkillPrompt == "stale:"+version.Version {
			return
		}
		r.app.Cfg.SkillPrompt = "stale:" + version.Version
		_ = config.Save(r.app.Cfg)
		if r.confirm("\nThe hopsesh skill is out of date. Update every copy?") {
			if _, err := r.app.InstallSkill(context.Background(), files, version.Version, bin, false, false); err != nil {
				r.printf("Could not update the skill: %v\n", err)
			} else {
				r.printf("Updated the hopsesh skill.\n")
			}
		}
	}
}
