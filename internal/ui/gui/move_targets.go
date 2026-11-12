package gui

import (
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// TargetDTO is one place a session can continue: a machine, a folder there and a tool, who
// it runs as there, what of it carries over, and what becomes of the copy left behind.
type TargetDTO struct {
	Machine    string `json:"machine"`
	Local      bool   `json:"local"`
	Profile    string `json:"profile"`
	Folder     string `json:"folder"` // "default" or its name
	Path       string `json:"path"`
	Tool       string `json:"tool"` // terminal | app
	OpensIn    string `json:"opensIn"`
	OpensNote  string `json:"opensNote,omitempty"`
	RunsAs     string `json:"runsAs,omitempty"` // an email, or "" unknown
	Person     string `json:"person,omitempty"`
	Carries    string `json:"carries"` // everything | conversation | unknown
	CarriesWhy string `json:"carriesWhy,omitempty"`
	Left       string `json:"left"` // blocked | advised | unprotected | kept
	LeftWhy    string `json:"leftWhy,omitempty"`
	Disabled   bool   `json:"disabled,omitempty"`
	Why        string `json:"why,omitempty"`
	Here       bool   `json:"here,omitempty"` // the session's own place: it continues where it is
}

// MoveTargets lists where a session can continue: this machine and every machine with
// hopsesh, in each of the agent's folders there, in a terminal or the desktop app.
func (a *App) MoveTargets(machine, key string) ([]TargetDTO, error) {
	core := a.snapshot()
	a.mu.Lock()
	e, err := a.find(machine, key)
	inv := a.inv
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := []TargetDTO{}
	if inv == nil {
		return out, nil
	}
	mod, ok := core.Module(e.Agent)
	if !ok {
		return out, nil
	}
	spec := mod.Spec()
	ran := ""
	if len(e.Session.Owners) > 0 {
		ran = ownerEmail(inv, e.Agent, e.Session.Owners[len(e.Session.Owners)-1])
	}
	// The copy left behind.
	src := inv.Machine(e.Machine)
	left, leftWhy := "blocked", "by hopsesh on "+machineLabel(src)
	switch {
	case !core.Cfg.MovementNoticesOn():
		left, leftWhy = "kept", "left as it is (protection is off)"
	case src != nil && !src.Local && src.Presence == app.PresenceSSH:
		left, leftWhy = "unprotected", "no hopsesh on "+src.Name+": nothing stops it from being continued there too"
	case core.Cfg.OriginalGuard() == "advise":
		left = "advised"
	}
	for _, m := range inv.Machines {
		if !m.Local && m.Presence != app.PresenceLogin && m.Presence != app.PresenceInstalled && m.Presence != app.PresenceOlder && m.Presence != app.PresenceSSH {
			continue
		}
		var ins []agent.Install
		for _, st := range m.Agents {
			if st.Agent == e.Agent && st.Install.Present && st.Install.Profile != nil {
				ins = append(ins, st.Install)
			}
		}
		disabled, why := false, ""
		switch {
		case m.Local:
		case m.Presence == app.PresenceSSH:
			disabled, why = true, "Set up hopsesh on "+m.Name+" to send sessions there."
		case m.Presence == app.PresenceOlder:
			disabled, why = true, "Update hopsesh on "+m.Name+" ("+m.Hopsesh+") to send sessions there."
		}
		if m.Presence == app.PresenceSSH && len(ins) == 0 {
			out = append(out, TargetDTO{Machine: m.Name, Folder: "", Tool: "terminal", OpensIn: "—", Carries: "unknown", Left: left, Disabled: true, Why: why})
			continue
		}
		for _, in := range ins {
			p := in.Profile
			folder := "default"
			if !p.Default {
				folder = p.Name
			}
			acct := p.Account
			if acct != nil && !acct.LoggedIn {
				acct = nil
			}
			t := TargetDTO{Machine: m.Name, Local: m.Local, Profile: p.ID, Folder: folder, Path: p.Root, Tool: "terminal", Left: left, LeftWhy: leftWhy, Disabled: disabled, Why: why}
			t.Here = m.Name == e.Machine && p.ID == e.Session.Key.Profile
			if m.Local {
				t.OpensIn = "Terminal here"
			} else {
				t.OpensIn, t.OpensNote = "Terminal on "+m.Name, "you work in it here"
			}
			if acct != nil {
				t.RunsAs = acct.Email
			}
			t.Carries, t.CarriesWhy = carries(spec, e.Session, acct, ran)
			out = append(out, t)
			// The desktop app: its own sign-in, the default folder, this machine only (on
			// another it opens when you are at it).
			if m.Local && p.Default && in.Desktop != "" && agent.Has(mod, agent.CapApp) {
				d := t
				d.Tool = "app"
				d.OpensIn = map[agent.ID]string{"claude": "Claude desktop", "codex": "ChatGPT desktop"}[e.Agent]
				var da *agent.Account
				if in.DesktopAccount != nil {
					da = &agent.Account{Key: in.DesktopAccount.Key, Person: in.DesktopAccount.Person, LoggedIn: true}
					d.RunsAs = ownerEmail(inv, e.Agent, *in.DesktopAccount)
				} else if e.Agent == "codex" {
					da = acct // the Codex app shares the folder's sign-in
				} else {
					da, d.RunsAs = nil, ""
				}
				d.Carries, d.CarriesWhy = carries(spec, e.Session, da, ran)
				out = append(out, d)
			}
		}
	}
	return out, nil
}

func machineLabel(m *app.Machine) string {
	if m == nil || m.Local {
		return "this machine"
	}
	return m.Name
}

// carries says what of a session continues as an account: everything when the same account
// ran it; else the conversation (what is bound to the other account is left out).
func carries(spec agent.Spec, s agent.Summary, acct *agent.Account, ran string) (string, string) {
	crosses, known := move.Crosses(s, acct)
	switch {
	case !known:
		return "unknown", "who it runs as there isn't known"
	case !crosses:
		return "everything", "same account that ran it"
	case ran == "":
		ran = "the account that ran it"
	}
	if spec.ResumeAcrossAccounts {
		return "conversation", "reasoning signed for " + ran + " is left out"
	}
	return "conversation", "a cleaned copy: reasoning encrypted for " + ran + " is left out"
}

// ownerEmail names the account of a recorded run (or a desktop's sign-in) by the email of
// a sign-in with the same account anywhere ("" unknown).
func ownerEmail(inv *app.Inventory, id agent.ID, r agent.OwnerRun) string {
	for _, m := range inv.Machines {
		for _, st := range m.Agents {
			p := st.Install.Profile
			if st.Agent != id || p == nil || p.Account == nil || p.Account.Email == "" || p.Account.Key == "" {
				continue
			}
			if r.Person != "" && p.Account.Person == r.Person || r.Person == "" && p.Account.Key == r.Key {
				return p.Account.Email
			}
		}
	}
	return ""
}
