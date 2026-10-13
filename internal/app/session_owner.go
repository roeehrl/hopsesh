package app

import (
	"slices"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// SessionOwner is the account that ran a session, as far as the agent recorded it. A
// profile's sign-in only says who is signed in to that root now: a root that was signed
// in to another account before holds that account's sessions too, so a session is never
// labelled with its root's sign-in unless its own record matches it.
type SessionOwner struct {
	// Recorded is whether the agent wrote down the account (Claude Code does in its Remote
	// Control records). Without it the account that ran the session is not known.
	Recorded bool `json:"recorded"`
	// Email is the recorded account's email, when a registered profile is signed in to
	// exactly that account; "" for a recorded account no profile is signed in to now.
	Email string `json:"email,omitempty"`
	// Profile is the name of the profile signed in to it, when that is not the session's
	// own root.
	Profile string `json:"profile,omitempty"`
	// Current is whether the session's own root is signed in to its account now.
	Current bool `json:"current,omitempty"`
}

// Logins maps the account fingerprints the registered profiles are signed in to now to
// those profiles, on every machine the inventory reached (a few installs: cheap to build
// per session).
type Logins map[string][]*agent.RuntimeProfile

// Logins collects the profiles' sign-ins, for OwnerOf.
func (inv *Inventory) Logins() Logins {
	out := Logins{}
	add := func(p *agent.RuntimeProfile) {
		if p == nil || p.Account == nil || !p.Account.LoggedIn || p.Account.Key == "" {
			return
		}
		key := string(p.Agent) + "\x00" + p.Account.Key
		if !slices.ContainsFunc(out[key], func(q *agent.RuntimeProfile) bool { return q.ID == p.ID && q.Machine == p.Machine }) {
			out[key] = append(out[key], p)
		}
	}
	if inv == nil {
		return out
	}
	for _, m := range inv.Machines {
		for i := range m.Agents {
			add(m.Agents[i].Install.Profile)
		}
	}
	return out
}

// OwnerOf is the account that ran e: the one its own record names, matched to a profile
// signed in to it now for the email. Several profiles signed in to the same fingerprint
// with different emails leave the email unknown rather than guess.
func (l Logins) OwnerOf(e Entry) SessionOwner {
	m := e.Session.Mirror
	if m == nil || m.Account == "" {
		return SessionOwner{}
	}
	o := SessionOwner{Recorded: true}
	if p := e.Profile; p != nil && p.Account != nil && p.Account.LoggedIn && p.Account.Key == m.Account {
		o.Current = true
	}
	var emails []string
	var names []string
	for _, p := range l[string(e.Agent)+"\x00"+m.Account] {
		if p.Account.Email != "" && !slices.Contains(emails, p.Account.Email) {
			emails = append(emails, p.Account.Email)
		}
		if e.Profile == nil || p.ID != e.Profile.ID {
			names = append(names, p.Name)
		}
	}
	if len(emails) == 1 {
		o.Email = emails[0]
		if !o.Current && len(names) > 0 {
			o.Profile = names[0]
		}
	}
	return o
}

// OwnerLabel names a session's account for people: its email, "another account" for one
// no profile is signed in to now, or "" when the agent did not record it.
func OwnerLabel(o SessionOwner) string {
	switch {
	case o.Email != "":
		return o.Email
	case o.Recorded && !o.Current:
		return "another account"
	}
	return ""
}

// OwnerLine describes a session's account and where it is stored, for text views: "run by
// a@x · stored in Claude Code default (signed in as b@y now)".
func OwnerLine(o SessionOwner, p *agent.RuntimeProfile) string {
	who := "account not recorded"
	if l := OwnerLabel(o); l != "" {
		who = "run by " + l
	}
	if p == nil {
		return who
	}
	return who + " · stored in " + p.Name + " (signed in as " + AccountLabel(p) + " now)"
}
