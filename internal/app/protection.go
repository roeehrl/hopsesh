package app

import (
	"context"
	"errors"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/integrate"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/version"
)

// Protection is what keeps a machine's originals protected and its agents knowing
// hopsesh: the hopsesh command, the protection hook in each agent's folders, and the
// skill. One model for this machine and, through hopsesh there, every other one.
type Protection struct {
	Machine string               `json:"machine"`
	Local   bool                 `json:"local"`
	Version string               `json:"version"`
	Program string               `json:"program,omitempty"` // the hopsesh command the hooks run
	Command bool                 `json:"command"`           // it is there and runs
	Hooks   []MovementHookStatus `json:"hooks"`
	Skill   SkillReport          `json:"skill"`
	Checked time.Time            `json:"checked"`
	Error   string               `json:"error,omitempty"`
}

// SkillFiles renders the hopsesh skill for a hopsesh program (bin); set by the front end
// (the skill's words live there).
type SkillFiles func(bin string) map[string][]byte

// LocalProtection checks this machine's command, hooks and skill.
func (a *App) LocalProtection(ctx context.Context) Protection {
	p := Protection{Machine: LocalName(), Local: true, Version: version.Version, Checked: time.Now().UTC(), Hooks: []MovementHookStatus{}}
	if bin, err := movementHookExecutable(); err == nil {
		p.Program, p.Command = bin, true
	}
	hooks, err := a.NoticeHooks(ctx)
	p.Hooks = hooks
	if err != nil && len(hooks) == 0 {
		p.Error = err.Error()
	}
	if a.Skills != nil {
		bin := integrate.SkillBin()
		p.Skill = a.Skill(ctx, a.Skills(bin), bin)
	}
	return p
}

// SetUpLocalProtection installs the protection hooks for every agent folder here and the
// skill in every folder its agents read it from (rules: also let agents run hopsesh's
// read-only commands without asking). A copy of the skill the user edited is left alone.
func (a *App) SetUpLocalProtection(ctx context.Context, rules bool) (Protection, error) {
	_, herr := a.InstallNoticeHooks(ctx, "", "")
	var serr error
	if a.Skills != nil {
		bin := integrate.SkillBin()
		_, serr = a.InstallSkill(ctx, a.Skills(bin), version.Version, bin, false, rules)
	}
	return a.LocalProtection(ctx), errors.Join(herr, serr)
}

// MachineProtection asks hopsesh on another machine for its protection.
func (a *App) MachineProtection(ctx context.Context, machine string) (Protection, error) {
	c, _, done, err := a.loginPeer(ctx, machine)
	if err != nil {
		return Protection{Machine: machine}, err
	}
	defer done()
	var p Protection
	err = c.Call(ctx, peer.MethodProtection, struct{}{}, &p)
	p.Machine, p.Local = machine, false
	return p, err
}

// SetUpProtectionOn sets protection up on another machine, through hopsesh there.
func (a *App) SetUpProtectionOn(ctx context.Context, machine string, rules bool) (Protection, error) {
	c, _, done, err := a.loginPeer(ctx, machine)
	if err != nil {
		return Protection{Machine: machine}, err
	}
	defer done()
	var p Protection
	err = c.Call(ctx, peer.MethodProtectionSetUp, peer.ProtectionSetUp{Rules: rules}, &p)
	p.Machine, p.Local = machine, false
	return p, err
}
