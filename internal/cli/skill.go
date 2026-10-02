package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/claudeskill"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/integrate"
	"github.com/roeehrl/hopsesh/internal/version"
)

func skillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Let Claude Code use hopsesh: install, update or remove the hopsesh skill",
		Long: `The hopsesh skill teaches Claude Code to list your sessions on other machines and move one
here with hopsesh: it always shows you the plan first and moves only after you say yes.
It is a personal skill in ~/.claude/skills/hopsesh (or $CLAUDE_CONFIG_DIR/skills).

  hopsesh skill            show whether it is installed and up to date
  hopsesh skill install    install it, or update an older one (--add-rules: also let Claude
                           run hopsesh's read-only commands without asking; moves still ask)
  hopsesh skill remove     remove it`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return skillStatus(cmd) },
	}
	cmd.Flags().Bool("json", false, "output JSON")
	install := &cobra.Command{
		Use:     "install",
		Aliases: []string{"update"},
		Short:   "Install the hopsesh skill for Claude Code, or update it",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool("force")
			rules, _ := cmd.Flags().GetBool("add-rules")
			st, added, err := integrate.InstallSkill(force, rules)
			if a.jsonOut {
				out := map[string]any{"status": st, "rulesAdded": added}
				if err != nil {
					out["error"] = err.Error()
				}
				_ = a.emitJSON(out)
				return err
			}
			switch {
			case errors.Is(err, claudeskill.ErrModified):
				return fmt.Errorf("%w (%v); keep your edits, or replace it with: hopsesh skill install --force (your version is kept as a backup)", err, st.Changed)
			case errors.Is(err, claudeskill.ErrForeign):
				return fmt.Errorf("%w at %s; replace it with --force (it is kept as a backup)", err, st.Dir)
			case err != nil:
				return err
			}
			a.printf("The hopsesh skill is installed and up to date: %s\n", st.Dir)
			for _, r := range added {
				a.printf("  added to %s: %s\n", integrate.ClaudeSettingsPath(), r)
			}
			if st.NewSkills {
				a.printf("Claude Code sessions that are already open need /reload-skills to see it; new sessions see it right away.\n")
			}
			a.printf("Try it: ask Claude \"what sessions do I have on my other machines?\"\n")
			a.cfg.SkillPrompt = ""
			_ = config.Save(a.cfg)
			return nil
		},
	}
	install.Flags().Bool("force", false, "replace a skill you edited (or one hopsesh did not install); the old one is kept as a backup")
	install.Flags().Bool("add-rules", false, "also add permission rules to Claude Code's settings: read-only hopsesh commands allowed, moves always ask")
	install.Flags().Bool("json", false, "output JSON")
	remove := &cobra.Command{
		Use:   "remove",
		Short: "Remove the hopsesh skill from Claude Code",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(cmd)
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool("force")
			if err := integrate.RemoveSkill(force); err != nil {
				return err
			}
			a.printf("Removed the hopsesh skill. (Permission rules hopsesh added, if any, stay in %s; remove them there if you like.)\n", integrate.ClaudeSettingsPath())
			return nil
		},
	}
	remove.Flags().Bool("force", false, "remove it even if you edited it")
	cmd.AddCommand(install, remove)
	return cmd
}

func skillStatus(cmd *cobra.Command) error {
	a, err := newApp(cmd)
	if err != nil {
		return err
	}
	st, err := integrate.SkillStatus()
	if err != nil {
		return err
	}
	rules := integrate.SkillRulesPresent()
	if a.jsonOut {
		return a.emitJSON(map[string]any{"status": st, "bin": integrate.SkillBin(), "version": version.Version, "rules": rules})
	}
	a.printf("%s\n", skillLine(st))
	if rules {
		a.printf("Permission rules: read-only hopsesh commands are allowed; moves ask.\n")
	} else if st.State != claudeskill.Absent {
		a.printf("Claude asks before every hopsesh command; to allow the read-only ones: hopsesh skill install --add-rules\n")
	}
	return nil
}

func skillLine(st claudeskill.Status) string {
	switch st.State {
	case claudeskill.Absent:
		return "Not installed. Let Claude Code use hopsesh with: hopsesh skill install"
	case claudeskill.Current:
		return "Installed and up to date: " + st.Dir
	case claudeskill.Stale:
		return fmt.Sprintf("Installed but out of date (from hopsesh %s); update with: hopsesh skill install", st.Version)
	case claudeskill.Modified:
		return fmt.Sprintf("Installed, and you edited %v; hopsesh leaves it alone (hopsesh skill install --force replaces it)", st.Changed)
	case claudeskill.Foreign:
		return "A skill called hopsesh exists at " + st.Dir + " that hopsesh did not install"
	case claudeskill.Broken:
		return "Damaged (SKILL.md is missing); repair with: hopsesh skill install"
	}
	return st.State
}

// offerSkill asks once whether Claude Code may use hopsesh, and offers updates to an
// out-of-date skill. Only in interactive sessions, never in JSON output.
func (a *app) offerSkill() {
	if a.jsonOut || !a.interactive() {
		return
	}
	st, err := integrate.SkillStatus()
	if err != nil {
		return
	}
	switch st.State {
	case claudeskill.Absent:
		if a.cfg.SkillPrompt == "declined" {
			return
		}
		a.printf("\nClaude Code can use hopsesh for you (\"bring my laptop session here\"): it shows the plan and moves only after you say yes.\n")
		if !a.confirm("Install the hopsesh skill for Claude Code?") {
			a.cfg.SkillPrompt = "declined"
			_ = config.Save(a.cfg)
			a.printf("OK. You can install it later with: hopsesh skill install\n")
			return
		}
		rules := a.confirm("Also let Claude run hopsesh's read-only commands (ls, show, plan) without asking? Otherwise Claude asks before every hopsesh command; moves always ask.")
		if st, _, err := integrate.InstallSkill(false, rules); err != nil {
			a.printf("Could not install the skill: %v\n", err)
		} else {
			a.printf("Installed: %s\n", st.Dir)
			if st.NewSkills {
				a.printf("Open Claude Code sessions need /reload-skills to see it.\n")
			}
		}
	case claudeskill.Stale:
		if a.cfg.SkillPrompt == "stale:"+version.Version {
			return
		}
		a.cfg.SkillPrompt = "stale:" + version.Version
		_ = config.Save(a.cfg)
		if a.confirm("\nThe hopsesh skill for Claude Code is out of date. Update it?") {
			if _, _, err := integrate.InstallSkill(false, false); err != nil {
				a.printf("Could not update the skill: %v\n", err)
			} else {
				a.printf("Updated the hopsesh skill.\n")
			}
		}
	}
}
