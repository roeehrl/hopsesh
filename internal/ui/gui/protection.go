package gui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// protections keeps the last check of every machine's protection, for the notices.
var protections struct {
	mu      sync.Mutex
	rows    []ProtectionRowDTO
	checked time.Time
	running bool
}

// ProtectionRowDTO is one machine in Settings › Machines: how hopsesh works with
// it, and, where hopsesh can check, its command, hooks and skill.
type ProtectionRowDTO struct {
	Machine    string          `json:"machine"`
	Local      bool            `json:"local"`
	OS         string          `json:"os,omitempty"`
	Presence   string          `json:"presence,omitempty"`
	Hopsesh    string          `json:"hopsesh,omitempty"`
	Protection *app.Protection `json:"protection,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// ProtectionAll checks every machine's protection: this one here, the others through
// hopsesh in their login session (in parallel); the rest say why they can't be checked.
func (a *App) ProtectionAll() []ProtectionRowDTO {
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	a.mu.Lock()
	inv := a.inv
	a.mu.Unlock()
	local := core.LocalProtection(ctx)
	out := []ProtectionRowDTO{{Machine: app.LocalName(), Local: true, Protection: &local}}
	if inv == nil {
		return out
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, m := range inv.Machines {
		if m.Local {
			continue
		}
		row := ProtectionRowDTO{Machine: m.Name, OS: m.OS, Presence: m.Presence, Hopsesh: m.Hopsesh}
		i := len(out)
		out = append(out, row)
		if m.Presence != app.PresenceLogin {
			continue
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			p, err := core.MachineProtection(ctx, name)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			out[i].Protection = &p
		}(m.Name)
	}
	wg.Wait()
	protections.mu.Lock()
	protections.rows, protections.checked = out, time.Now()
	protections.mu.Unlock()
	return out
}

// needsOf names what a machine's protection still needs ("Codex hook", "skill").
func needsOf(p *app.Protection) []string {
	var out []string
	if !p.Command {
		out = append(out, "the hopsesh command")
	}
	for _, h := range p.Hooks {
		name := map[agent.ID]string{"claude": "Claude Code", "codex": "Codex"}[h.Agent]
		if name == "" {
			name = string(h.Agent)
		}
		switch {
		case !h.Supported:
		case !h.Installed:
			out = append(out, name+" hook")
		case h.Trust != nil && (h.Trust.State == agent.HookNeedsReview || h.Trust.State == agent.HookDisabled || h.Trust.State == agent.HookMissing):
			out = append(out, name+" hook approval")
		}
	}
	if p.Skill.State != "" && p.Skill.State != "current" {
		out = append(out, "skill")
	}
	return slices.Compact(out)
}

// MachineNoticeDTO is one notice about another machine on the Sessions screen.
type MachineNoticeDTO struct {
	Machine     string `json:"machine"`
	Kind        string `json:"kind"` // no-hopsesh | older | protection
	Title       string `json:"title"`
	Text        string `json:"text"`
	Action      string `json:"action"`
	Fingerprint string `json:"fingerprint"`
}

// MachineNotices are what another machine needs from the user, found by the last scan and
// the last check of its protection (rechecked in the background now and then). Notices the
// user answered "Not now" to stay hidden until their findings change.
func (a *App) MachineNotices() []MachineNoticeDTO {
	a.mu.Lock()
	inv := a.inv
	dismissed := slices.Clone(a.core.Cfg.MachinesPrompt)
	a.mu.Unlock()
	out := []MachineNoticeDTO{}
	if inv == nil {
		return out
	}
	protections.mu.Lock()
	rows, checked, running := protections.rows, protections.checked, protections.running
	if !running && time.Since(checked) > 30*time.Minute {
		protections.running = true
		go func() {
			a.ProtectionAll()
			protections.mu.Lock()
			protections.running = false
			protections.mu.Unlock()
		}()
	}
	protections.mu.Unlock()
	add := func(n MachineNoticeDTO) {
		if !slices.Contains(dismissed, n.Fingerprint) {
			out = append(out, n)
		}
	}
	for _, m := range inv.Machines {
		if m.Local || m.Status != app.StatusOK {
			continue
		}
		sessions := 0
		for _, e := range inv.Entries {
			if e.Machine == m.Name {
				sessions++
			}
		}
		switch m.Presence {
		case app.PresenceSSH:
			if sessions > 0 {
				add(MachineNoticeDTO{Machine: m.Name, Kind: "no-hopsesh", Title: m.Name + " has sessions but no hopsesh.", Text: "Originals you move from " + m.Name + " won't be protected there.", Action: "Set up hopsesh on " + m.Name, Fingerprint: "ssh:" + m.Name})
			}
		case app.PresenceOlder:
			add(MachineNoticeDTO{Machine: m.Name, Kind: "older", Title: m.Name + " runs an older hopsesh (" + m.Hopsesh + ").", Text: "Sending sessions there and running sessions there wait until it's updated.", Action: "Update hopsesh on " + m.Name, Fingerprint: "older:" + m.Name + ":" + m.Hopsesh})
		case app.PresenceLogin:
			for _, r := range rows {
				if r.Machine != m.Name || r.Protection == nil {
					continue
				}
				if needs := needsOf(r.Protection); len(needs) > 0 {
					sum := sha256.Sum256([]byte(strings.Join(needs, "\x00")))
					add(MachineNoticeDTO{Machine: m.Name, Kind: "protection", Title: "Protection on " + m.Name + " needs " + strings.Join(needs, ", ") + ".", Text: "Set it up there from here: hopsesh on " + m.Name + " installs the hooks and the skill. It never approves hooks for you.", Action: "Set up on " + m.Name, Fingerprint: "prot:" + m.Name + ":" + hex.EncodeToString(sum[:6])})
				}
			}
		}
	}
	return out
}

// DismissMachineNotice remembers "Not now" for a machine notice.
func (a *App) DismissMachineNotice(fingerprint string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !slices.Contains(a.core.Cfg.MachinesPrompt, fingerprint) {
		a.core.Cfg.MachinesPrompt = append(a.core.Cfg.MachinesPrompt, fingerprint)
		if n := len(a.core.Cfg.MachinesPrompt); n > 50 {
			a.core.Cfg.MachinesPrompt = a.core.Cfg.MachinesPrompt[n-50:]
		}
	}
	return a.save()
}

// SetUpProtection installs the protection hooks and the skill on a machine (this one, or
// one with hopsesh running in its login session). rules also lets agents run hopsesh's
// read-only commands without asking.
//
// What it changed shows at once: the notices check every machine again, and that machine's
// hooks are read again for the copies left there (which the window is shown).
func (a *App) SetUpProtection(machine string, rules bool) (*app.Protection, error) {
	core := a.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	defer func() {
		protections.mu.Lock()
		protections.rows, protections.checked = nil, time.Time{}
		protections.mu.Unlock()
	}()
	if machine == "" || machine == app.LocalName() {
		p, err := core.SetUpLocalProtection(ctx, rules)
		_, _ = a.HookHealth(true) // shows the window what changed itself
		return &p, err
	}
	p, err := core.SetUpProtectionOn(ctx, machine, rules)
	a.mu.Lock()
	var m *app.Machine
	if a.inv != nil {
		if found := a.inv.Machine(machine); found != nil {
			mm := *found // read outside the lock
			m = &mm
		}
	}
	a.mu.Unlock()
	if m != nil {
		core.RefreshMachineStatus(ctx, m, true)
		forgetStatuses()
		a.guardsChanged()
	}
	return &p, err
}
