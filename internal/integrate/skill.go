package integrate

import (
	"os"
	"path/filepath"

	"github.com/roeehrl/hopsesh/internal/claudeskill"
	"github.com/roeehrl/hopsesh/internal/version"
)

// SkillBin is how Claude Code should run hopsesh: plain "hopsesh" when the user's login
// shell finds it, otherwise an absolute path (the linked tool, the app's tool, or this
// program).
func SkillBin() string {
	if LookLoginPath("hopsesh") != "" {
		return "hopsesh"
	}
	if st := CheckCLI(); st.State == CLIOurs || st.State == CLIOtherApp || st.State == CLIStandalone {
		return st.Path
	}
	if cli, err := AppCLI(); err == nil {
		return cli
	}
	exe, err := os.Executable()
	if err != nil {
		return "hopsesh"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe
}

// SkillParams are what this build renders into the skill.
func SkillParams() claudeskill.Params {
	return claudeskill.Params{Bin: SkillBin(), Version: version.Version}
}

// SkillStatus checks the installed skill against this build.
func SkillStatus() (claudeskill.Status, error) {
	return claudeskill.Check(ClaudeConfigDir(), SkillParams())
}

// ClaudeSettingsPath is Claude Code's user settings file.
func ClaudeSettingsPath() string { return filepath.Join(ClaudeConfigDir(), "settings.json") }

// SkillRulesPresent reports whether the optional permission rules are in place.
func SkillRulesPresent() bool { return claudeskill.HasRules(ClaudeSettingsPath(), SkillBin()) }

// InstallSkill installs or updates the skill, and adds the permission rules when asked.
func InstallSkill(force, addRules bool) (claudeskill.Status, []string, error) {
	p := SkillParams()
	st, err := claudeskill.Install(ClaudeConfigDir(), p, force)
	if err != nil {
		return st, nil, err
	}
	var added []string
	if addRules {
		added, err = claudeskill.AddRules(ClaudeSettingsPath(), p.Bin)
	}
	return st, added, err
}

// RemoveSkill removes the skill hopsesh installed (force: even an edited one).
func RemoveSkill(force bool) error {
	return claudeskill.Remove(ClaudeConfigDir(), SkillParams(), force)
}
