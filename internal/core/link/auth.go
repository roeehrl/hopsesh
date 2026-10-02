package link

import (
	"encoding/json"
	"strings"
)

// Auth is what `claude auth status --json` reports about a machine's login. The email
// address is deliberately not kept.
type Auth struct {
	LoggedIn     bool   `json:"loggedIn"`
	Method       string `json:"authMethod"`       // claude.ai | api-key | …
	Provider     string `json:"apiProvider"`      // firstParty | bedrock | vertex | …
	OrgID        string `json:"orgId"`            // identifies the account for comparisons
	Subscription string `json:"subscriptionType"` // pro | max | team | enterprise | …
}

// ParseAuth reads `claude auth status --json` output.
func ParseAuth(b []byte) (*Auth, error) {
	var a Auth
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// RemoteControl reports whether this login can use Remote Control, and why not. Remote
// Control needs a claude.ai subscription login with Anthropic as the provider.
func (a *Auth) RemoteControl() (bool, string) {
	switch {
	case a == nil:
		return true, "" // unknown: let claude decide
	case !a.LoggedIn:
		return false, "Claude Code is not logged in on this machine"
	case a.Provider != "" && a.Provider != "firstParty":
		return false, "Claude Code here uses " + a.Provider + ", and Remote Control needs a claude.ai subscription login"
	case a.Method != "" && !strings.EqualFold(a.Method, "claude.ai"):
		return false, "Claude Code here is logged in with " + a.Method + ", and Remote Control needs a claude.ai subscription login"
	}
	return true, ""
}

// SameAccount compares two logins. known is false when either side could not be read.
func SameAccount(a, b *Auth) (same, known bool) {
	if a == nil || b == nil || a.OrgID == "" || b.OrgID == "" {
		return false, false
	}
	return a.OrgID == b.OrgID, true
}
