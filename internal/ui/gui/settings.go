package gui

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/lnp"
	"github.com/roeehrl/hopsesh/internal/ui/skill"
	"github.com/roeehrl/hopsesh/internal/version"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// SettingsDTO is everything the Settings screen shows.
type SettingsDTO struct {
	Version    string     `json:"version"`
	ReposDir   string     `json:"reposDir"`
	Layout     string     `json:"layout"` // flat | ghq
	MarkMoved  bool       `json:"markMoved"`
	SyncCode   bool       `json:"syncCode"`
	PushSource bool       `json:"pushSource"`
	UpdateChk  string     `json:"updateCheck"`
	Receive    bool       `json:"receive"` // other machines' hopsesh may push sessions here
	Agents     []AgentDTO `json:"agents"`

	CLI         integrate.CLIStatus `json:"cli"`
	Skill       app.SkillReport     `json:"skill"`
	SkillBin    string              `json:"skillBin"`
	SkillPrompt string              `json:"skillPrompt"`

	LocalNetworkGated bool   `json:"localNetworkGated"`
	ConfigDir         string `json:"configDir"`
	StateDir          string `json:"stateDir"`
}

// skillFiles renders this build's skill for the enabled agents.
func (a *App) skillFiles() (map[string][]byte, string) {
	bin := integrate.SkillBin()
	var names, ids []string
	for _, s := range a.snapshot().Specs() {
		names = append(names, s.Name)
		ids = append(ids, string(s.ID))
	}
	files, _ := skill.Render(skill.NewParams(bin, version.Version, names, ids))
	return files, bin
}

func ctx20() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

// Settings returns the current settings and integration state.
func (a *App) Settings() SettingsDTO {
	ctx, cancel := ctx20()
	defer cancel()
	files, bin := a.skillFiles()
	rep := a.snapshot().Skill(ctx, files, bin)
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg := a.core.Cfg
	return SettingsDTO{Version: version.Version, ReposDir: cfg.ReposDir, Layout: nonEmpty(cfg.Layout, "flat"),
		MarkMoved: cfg.MarkMovedOn(), SyncCode: cfg.SyncCodeOn(), PushSource: cfg.PushSource, UpdateChk: cfg.UpdateCheck,
		Receive: cfg.Peer.Receive, Agents: a.agentsLocked(), CLI: integrate.CheckCLI(), Skill: rep, SkillBin: bin, SkillPrompt: cfg.SkillPrompt,
		LocalNetworkGated: lnp.Gated(), ConfigDir: config.Dir(), StateDir: config.StateDir()}
}

// SettingsInput are the settings the user can change.
type SettingsInput struct {
	Layout     string `json:"layout"`
	MarkMoved  bool   `json:"markMoved"`
	SyncCode   bool   `json:"syncCode"`
	PushSource bool   `json:"pushSource"`
	UpdateChk  string `json:"updateCheck"`
	Receive    bool   `json:"receive"`
}

// SaveSettings stores the user's choices.
func (a *App) SaveSettings(in SettingsInput) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch in.Layout {
	case "flat", "ghq":
		a.core.Cfg.Layout = in.Layout
	}
	switch in.UpdateChk {
	case "on", "off":
		a.core.Cfg.UpdateCheck = in.UpdateChk
	}
	mark, sync := in.MarkMoved, in.SyncCode
	a.core.Cfg.MarkMoved, a.core.Cfg.SyncCode = &mark, &sync
	a.core.Cfg.PushSource = in.PushSource
	a.core.Cfg.Peer.Receive = in.Receive
	return a.save()
}

// SetAgent turns an agent on or off (an agent that is off is not scanned and cannot be
// moved into) and sets whether continued sessions turn its remote control on.
func (a *App) SetAgent(id string, enabled, remoteControl bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.core.Reg.Get(agent.ID(id)); !ok {
		return errors.New("unknown agent " + id)
	}
	if a.core.Cfg.Agents == nil {
		a.core.Cfg.Agents = map[string]config.Agent{}
	}
	ac := config.Agent{Disabled: !enabled, RemoteControl: remoteControl}
	if ac == (config.Agent{}) {
		delete(a.core.Cfg.Agents, id)
	} else {
		a.core.Cfg.Agents[id] = ac
	}
	return a.save()
}

// cliOffer reports whether to offer linking the command-line tool (the app has one and
// nothing is linked yet).
func cliOffer() bool {
	if _, err := integrate.AppCLI(); err != nil {
		return false
	}
	return integrate.CheckCLI().State == integrate.CLIMissing
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

// InstallSkill installs or updates the hopsesh skill in every installed agent, the same
// files in each; addRules also adds each agent's approval rules (read-only commands run
// without asking, moves always ask).
func (a *App) InstallSkill(force, addRules bool) (app.SkillReport, error) {
	ctx, cancel := ctx20()
	defer cancel()
	files, bin := a.skillFiles()
	rep, err := a.snapshot().InstallSkill(ctx, files, version.Version, bin, force, addRules)
	if err == nil {
		a.mu.Lock()
		a.core.Cfg.SkillPrompt = ""
		_ = a.save()
		a.mu.Unlock()
	}
	return rep, err
}

// RemoveSkill removes every copy of the skill and the approval rules.
func (a *App) RemoveSkill(force bool) (app.SkillReport, error) {
	ctx, cancel := ctx20()
	defer cancel()
	files, bin := a.skillFiles()
	core := a.snapshot()
	err := core.RemoveSkill(ctx, files, bin, force)
	return core.Skill(ctx, files, bin), err
}

// SkillPreview returns the SKILL.md this build installs.
func (a *App) SkillPreview() (string, error) {
	files, _ := a.skillFiles()
	b, ok := files["SKILL.md"]
	if !ok {
		return "", errors.New("the skill did not render")
	}
	return string(b), nil
}

// DismissSkillOffer stops the app from offering the skill (it stays in Settings).
func (a *App) DismissSkillOffer() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.core.Cfg.SkillPrompt = "declined"
	return a.save()
}

// DismissCLIOffer stops the app from offering to link the command-line tool.
func (a *App) DismissCLIOffer() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.core.Cfg.CLIPrompt = "declined"
	return a.save()
}

// Reveal shows a file or folder in Finder (or the system file manager).
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
