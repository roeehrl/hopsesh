package gui

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// GuardDTO is how the copy left behind by a move is protected, as the app shows it.
// Effective is false when hopsesh cannot make the agent honor the choice here: the hook
// is not installed, the agent has not trusted it (Codex), or it cannot be verified.
type GuardDTO struct {
	Mode      string `json:"mode"` // block | advise | off | released
	Effective bool   `json:"effective"`
	Problem   string `json:"problem,omitempty"`
	Fix       string `json:"fix,omitempty"`
	Operation string `json:"operation,omitempty"`
}

// HookHealthDTO is what keeps movement protection and the skill working on this machine.
type HookHealthDTO struct {
	CLI      integrate.CLIStatus      `json:"cli"`
	CLIReady bool                     `json:"cliReady"` // the hopsesh command is installed and works
	Hooks    []app.MovementHookStatus `json:"hooks"`
	Problems []HookProblem            `json:"problems"`
	Mode     string                   `json:"mode"` // config original
	Checked  time.Time                `json:"checked"`
}

// HookProblem is one agent profile whose protection hook cannot protect originals.
type HookProblem struct {
	Agent   agent.ID `json:"agent"`
	Profile string   `json:"profile"`
	Label   string   `json:"label"`
	State   string   `json:"state"` // not-installed | unsupported | needs-review | disabled | missing | unknown | settings
	Message string   `json:"message"`
	Fix     string   `json:"fix,omitempty"`
}

var health struct {
	mu  sync.Mutex
	dto *HookHealthDTO
}

// CLIReady reports whether the hopsesh command is installed where the skill and hooks
// can run it (a link to this or another copy of the app, or a separate install).
func CLIReady(st integrate.CLIStatus) bool {
	return st.State == integrate.CLIOurs || st.State == integrate.CLIOtherApp || st.State == integrate.CLIStandalone ||
		st.State != integrate.CLIDangling && integrate.LookLoginPath("hopsesh") != ""
}

// HookHealth checks the hopsesh command and every local protection hook, asking agents that
// review hooks (Codex) whether they trust them. force rechecks now; otherwise a check
// from the last five minutes is reused.
func (a *App) HookHealth(force bool) (HookHealthDTO, error) {
	health.mu.Lock()
	if !force && health.dto != nil && time.Since(health.dto.Checked) < 5*time.Minute {
		d := *health.dto
		health.mu.Unlock()
		return d, nil
	}
	health.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	core := a.snapshot()
	d := HookHealthDTO{CLI: integrate.CheckCLI(), Mode: core.Cfg.OriginalGuard(), Checked: time.Now()}
	d.CLIReady = CLIReady(d.CLI)
	hooks, err := core.NoticeHooks(ctx)
	d.Hooks = hooks
	d.Problems = hookProblems(hooks, d.Mode)
	if err != nil && len(hooks) == 0 {
		d.Problems = append(d.Problems, HookProblem{State: "unknown", Message: "hopsesh could not check the protection hooks: " + err.Error()})
	}
	health.mu.Lock()
	health.dto = &d
	health.mu.Unlock()
	return d, nil
}

func hookProblems(hooks []app.MovementHookStatus, mode string) []HookProblem {
	if mode == config.OriginalOff {
		return []HookProblem{}
	}
	out := []HookProblem{}
	for _, h := range hooks {
		p := HookProblem{Agent: h.Agent, Profile: h.Profile, Label: h.ProfileLabel}
		switch {
		case !h.Supported:
			p.State, p.Message = "unsupported", "Movement protection is not available here: "+h.Reason
		case !h.Enabled:
			p.State, p.Message = "settings", h.Reason
		case !h.Installed:
			p.State, p.Message, p.Fix = "not-installed", "The protection hook is not installed, so originals are not protected.", "Install the hook in Settings › General."
		case h.Trust != nil && h.Trust.State != agent.HookTrusted:
			p.State, p.Fix = h.Trust.State, h.Trust.Fix
			p.Message = map[string]string{
				agent.HookNeedsReview: "The agent has not trusted the hopsesh hooks yet, so it skips them: originals are not blocked or advised.",
				agent.HookDisabled:    "The hopsesh hooks are turned off in the agent, so originals are not protected.",
				agent.HookMissing:     "The agent does not see the hopsesh hooks.",
				agent.HookUnknown:     "hopsesh could not ask the agent whether it trusts the hooks. " + h.Trust.Detail,
			}[h.Trust.State]
		default:
			continue
		}
		out = append(out, p)
	}
	return out
}

// guardFor is the protection of entry e, a copy that moved on (nil otherwise).
func guardFor(core *app.App, e app.Entry) *GuardDTO {
	n := e.Departure
	if n == nil || n.Status == "forked" {
		return nil
	}
	g := &GuardDTO{Operation: n.Operation, Mode: core.OriginalGuard(e.Session.Key, n.Operation, n.Status)}
	if core.Cfg.OriginalGuard() != config.OriginalOff && core.Released(e.Session.Key, n.Operation) {
		g.Mode = "released"
	}
	if g.Mode == config.OriginalOff || g.Mode == "released" {
		return g
	}
	if e.Machine != app.LocalName() {
		g.Problem = "Protected by hopsesh on " + e.Machine + " when its hooks are set up there."
		return g
	}
	health.mu.Lock()
	d := health.dto
	health.mu.Unlock()
	if d == nil {
		g.Problem = "Checking the agent's hooks…"
		return g
	}
	for _, p := range d.Problems {
		if p.Agent == e.Agent && (p.Profile == "" || p.Profile == e.Session.Key.Profile) {
			g.Problem, g.Fix = p.Message, p.Fix
			return g
		}
	}
	for _, h := range d.Hooks {
		if h.Agent == e.Agent && h.Profile == e.Session.Key.Profile && h.Installed {
			g.Effective = true
			return g
		}
	}
	g.Problem, g.Fix = "No protection hook covers this account, so the original is not protected.", "Install the hooks in Settings › General."
	return g
}

// ReleaseOriginal removes the block (or advice) from one original on this machine, for
// its current movement. The window confirms first: continuing there makes the copies
// diverge, and moving back then needs a comparison instead of a clean return.
func (a *App) ReleaseOriginal(machine, key string) error {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if e.Machine != app.LocalName() {
		return errors.New("the original is on " + e.Machine + ": remove its block in hopsesh there")
	}
	if e.Departure == nil {
		return errors.New("this session was not moved away")
	}
	return core.ReleaseOriginal(e.Session.Key, e.Departure.Operation)
}

// RestoreBlock puts a removed block back on an original on this machine.
func (a *App) RestoreBlock(machine, key string) error {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	return core.RestoreBlock(e.Session.Key)
}
