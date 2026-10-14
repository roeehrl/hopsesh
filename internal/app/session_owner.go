package app

import (
	"slices"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// SessionOwner is the account that ran a session, as far as the agent recorded it. A
// profile's sign-in only says who is signed in to that root now: a root that was signed
// in to another account before holds that account's sessions too, so a session is never
// labelled with its root's sign-in unless its own record matches it.
type SessionOwner struct {
	// Recorded is whether the account was recorded: by the agent (Claude Code's per-run
	// organisation and Remote Control records, Codex's session creator) or by hopsesh's
	// prompt hook. Without it the account that ran the session is not known.
	Recorded bool `json:"recorded"`
	// Email is the latest recorded account's email, when a registered profile is signed in
	// to exactly that account; "" for a recorded account no profile is signed in to now.
	Email string `json:"email,omitempty"`
	// Profile is the name of the profile signed in to it, when that is not the session's
	// own root.
	Profile string `json:"profile,omitempty"`
	// Current is whether the session's own root is signed in to its account now.
	Current bool `json:"current,omitempty"`
	// Key is a stable fingerprint of the latest recorded account (the same after any
	// sign-in change), for grouping and saved filters.
	Key string `json:"key,omitempty"`
	// Earlier names the other accounts it ran under before, when it changed accounts.
	Earlier string `json:"earlier,omitempty"`
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

// Owners are the accounts a session ran under, oldest first: its own records, or for an
// older summary only its Remote Control owner.
func Owners(s agent.Summary) []agent.OwnerRun {
	if len(s.Owners) > 0 {
		return s.Owners
	}
	if m := s.Mirror; m != nil && m.Account != "" {
		return []agent.OwnerRun{{Key: m.Account, Source: "agent"}}
	}
	return nil
}

// runOf: a profile signed in to the account a run names. A run that names its user
// matches only that user; one that names only the account (an organisation, for Claude
// Code) matches anyone signed in to it.
func runOf(r agent.OwnerRun, a *agent.Account) bool {
	if a == nil || !a.LoggedIn || a.Key == "" || a.Key != r.Key {
		return false
	}
	return r.Person == "" || a.Person == "" || r.Person == a.Person
}

// shared: an account several people can sign in to (a team or enterprise organisation).
func shared(a *agent.Account) bool {
	l := strings.ToLower(a.Label)
	return strings.Contains(l, "team") || strings.Contains(l, "enterprise") || strings.Contains(l, "business") || strings.Contains(l, "edu")
}

// OwnerOf is the account that ran e, as its records say, matched to a profile signed in
// to it now for the email. Several profiles signed in to the same fingerprint with
// different emails, or a run naming only a shared organisation, leave the email unknown
// rather than guess.
func (l Logins) OwnerOf(e Entry) SessionOwner {
	runs := Owners(e.Session)
	if len(runs) == 0 {
		return SessionOwner{}
	}
	o := SessionOwner{Recorded: true}
	last := runs[len(runs)-1]
	o.Key = "k:" + last.Key
	if last.Person != "" {
		o.Key = "p:" + last.Person
	}
	email, name := l.match(e, last)
	o.Email, o.Profile = email, name
	if p := e.Profile; p != nil && runOf(last, p.Account) && (last.Person != "" || !shared(p.Account)) {
		o.Current, o.Profile = true, ""
	}
	var earlier []string
	for _, r := range runs[:len(runs)-1] {
		label := "another account"
		if em, _ := l.match(e, r); em != "" {
			label = em
		}
		if label != o.Email && !slices.Contains(earlier, label) {
			earlier = append(earlier, label)
		}
	}
	o.Earlier = strings.Join(earlier, ", ")
	return o
}

func (l Logins) match(e Entry, r agent.OwnerRun) (email, profile string) {
	var emails, names []string
	for _, p := range l[string(e.Agent)+"\x00"+r.Key] {
		if !runOf(r, p.Account) || r.Person == "" && shared(p.Account) {
			continue
		}
		if p.Account.Email != "" && !slices.Contains(emails, p.Account.Email) {
			emails = append(emails, p.Account.Email)
		}
		if e.Profile == nil || p.ID != e.Profile.ID {
			names = append(names, p.Name)
		}
	}
	if len(emails) != 1 {
		return "", ""
	}
	if len(names) > 0 {
		profile = names[0]
	}
	return emails[0], profile
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
