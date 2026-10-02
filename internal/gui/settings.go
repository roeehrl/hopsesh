package gui

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/roeehrl/hopsesh/internal/claudeskill"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/lnp"
	"github.com/roeehrl/hopsesh/internal/integrate"
	"github.com/roeehrl/hopsesh/internal/version"
)

// SettingsDTO is everything the Settings screen shows.
type SettingsDTO struct {
	Version    string `json:"version"`
	ReposDir   string `json:"reposDir"`
	Layout     string `json:"layout"`     // flat | ghq
	LivePolicy string `json:"livePolicy"` // handoff | fork
	RemoteCtl  bool   `json:"remoteControl"`
	MarkMoved  bool   `json:"markMoved"`
	SyncCode   bool   `json:"syncCode"`
	PushSource bool   `json:"pushSource"`
	UpdateChk  string `json:"updateCheck"`

	CLI         integrate.CLIStatus `json:"cli"`
	Skill       claudeskill.Status  `json:"skill"`
	SkillBin    string              `json:"skillBin"`
	SkillRules  bool                `json:"skillRules"`
	SkillPrompt string              `json:"skillPrompt"`
	ClaudeDir   string              `json:"claudeConfigDir"`

	LocalNetworkGated bool   `json:"localNetworkGated"`
	ConfigDir         string `json:"configDir"`
	StateDir          string `json:"stateDir"`
}

// Settings returns the current settings and integration state.
func (a *App) Settings() SettingsDTO {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	d := SettingsDTO{Version: version.Version, ReposDir: cfg.ReposDir, Layout: nonEmpty(cfg.Layout, "flat"),
		LivePolicy: nonEmpty(cfg.LivePolicy, "handoff"), RemoteCtl: cfg.RemoteCtl, MarkMoved: cfg.MarkMovedOn(),
		SyncCode: cfg.SyncCodeOn(), PushSource: cfg.PushSource, UpdateChk: cfg.UpdateCheck,
		CLI: integrate.CheckCLI(), SkillBin: integrate.SkillBin(), SkillRules: integrate.SkillRulesPresent(),
		SkillPrompt: cfg.SkillPrompt, ClaudeDir: integrate.ClaudeConfigDir(),
		LocalNetworkGated: lnp.Gated(), ConfigDir: config.Dir(), StateDir: config.StateDir()}
	d.Skill, _ = integrate.SkillStatus()
	return d
}

// SettingsInput are the settings the user can change.
type SettingsInput struct {
	Layout     string `json:"layout"`
	LivePolicy string `json:"livePolicy"`
	RemoteCtl  bool   `json:"remoteControl"`
	MarkMoved  bool   `json:"markMoved"`
	SyncCode   bool   `json:"syncCode"`
	PushSource bool   `json:"pushSource"`
	UpdateChk  string `json:"updateCheck"`
}

// SaveSettings stores the user's choices.
func (a *App) SaveSettings(in SettingsInput) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch in.Layout {
	case "flat", "ghq":
		a.cfg.Layout = in.Layout
	}
	switch in.LivePolicy {
	case "handoff", "fork":
		a.cfg.LivePolicy = in.LivePolicy
	}
	switch in.UpdateChk {
	case "on", "off":
		a.cfg.UpdateCheck = in.UpdateChk
	}
	a.cfg.RemoteCtl = in.RemoteCtl
	mark, sync := in.MarkMoved, in.SyncCode
	a.cfg.MarkMoved, a.cfg.SyncCode = &mark, &sync
	a.cfg.PushSource = in.PushSource
	return config.Save(a.cfg)
}

// InstallCLI links the hopsesh command into ~/.local/bin.
func (a *App) InstallCLI(force bool) (integrate.CLIStatus, error) {
	return integrate.InstallCLI(force)
}

// UninstallCLI removes the link the app made (and its PATH block).
func (a *App) UninstallCLI() (integrate.CLIStatus, error) {
	err := integrate.UninstallCLI()
	return integrate.CheckCLI(), err
}

// AddCLIToPath adds ~/.local/bin to the login shell's PATH (only on the user's click).
func (a *App) AddCLIToPath() (string, error) {
	return integrate.AddToPath()
}

// InstallSkill installs or updates the hopsesh skill for Claude Code.
func (a *App) InstallSkill(force, addRules bool) (claudeskill.Status, error) {
	st, _, err := integrate.InstallSkill(force, addRules)
	if err == nil {
		a.mu.Lock()
		a.cfg.SkillPrompt = ""
		_ = config.Save(a.cfg)
		a.mu.Unlock()
	}
	return st, err
}

// AddSkillRules adds the permission rules (read-only hopsesh commands allowed, moves ask)
// to Claude Code's settings.
func (a *App) AddSkillRules() ([]string, error) {
	return claudeskill.AddRules(integrate.ClaudeSettingsPath(), integrate.SkillBin())
}

// RemoveSkill removes the hopsesh skill.
func (a *App) RemoveSkill(force bool) (claudeskill.Status, error) {
	err := integrate.RemoveSkill(force)
	st, _ := integrate.SkillStatus()
	return st, err
}

// SkillPreview returns the SKILL.md this build would install.
func (a *App) SkillPreview() (string, error) {
	files, err := claudeskill.Render(integrate.SkillParams())
	if err != nil {
		return "", err
	}
	return string(files["SKILL.md"]), nil
}

// DismissSkillOffer stops the app from offering the skill (it stays in Settings).
func (a *App) DismissSkillOffer() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.SkillPrompt = "declined"
	return config.Save(a.cfg)
}

// DismissCLIOffer stops the app from offering to link the command-line tool.
func (a *App) DismissCLIOffer() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.CLIPrompt = "declined"
	return config.Save(a.cfg)
}

// Reveal shows a folder in Finder (or the system file manager).
func (a *App) Reveal(path string) error {
	if path == "" {
		return errors.New("no path")
	}
	path = filepath.Clean(path)
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Run()
	case "windows":
		return exec.Command("explorer", "/select,", path).Run()
	}
	return exec.Command("xdg-open", filepath.Dir(path)).Run()
}
