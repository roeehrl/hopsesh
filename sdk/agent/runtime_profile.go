package agent

import (
	"strings"
	"time"
)

// RuntimeProfile is a pinned, agent-specific state root, independent of account labels.
// Binding describes the observed login; it is not a credential or proof of ownership.
type RuntimeProfile struct {
	CheckedAt  time.Time `json:"checkedAt,omitempty"`
	Machine    string    `json:"machine,omitempty"`
	ID         string    `json:"id"`
	Endpoint   string    `json:"endpoint"`
	Agent      ID        `json:"agent"`
	Name       string    `json:"name"`
	Tags       []string  `json:"tags"`
	Root       string    `json:"root"`
	Default    bool      `json:"default"`
	Managed    bool      `json:"managed"`
	Generation int       `json:"generation"`
	Binding    string    `json:"binding,omitempty"`
	Account    *Account  `json:"account,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func (in Install) ProfileID() string {
	if in.Profile == nil {
		return ""
	}
	return in.Profile.ID
}
func (in Install) BindingID() string {
	if in.Profile == nil {
		return ""
	}
	return in.Profile.Binding
}

// ProfileSpec is the module's declaration of vendor-supported account isolation.
// Only module code supplies it; peer payloads never control launch policy.
type ProfileSpec struct {
	RootEnv      []string          `json:"rootEnv,omitempty"`
	Unset        []string          `json:"unset,omitempty"`
	Login        []string          `json:"login,omitempty"`
	InitialFiles map[string]string `json:"initialFiles,omitempty"`
}

func (in Install) ProfileEnv() []string {
	if in.Profile == nil || in.Accounts == nil {
		return nil
	}
	var env []string
	for _, key := range in.Accounts.RootEnv {
		env = append(env, key+"="+in.Profile.Root)
	}
	return env
}

// ScopeCommand applies the profile last. Caller-supplied roots and inherited account
// overrides cannot redirect a managed launch into a sibling profile.
func (in Install) ScopeCommand(c Command) Command {
	if in.Profile == nil {
		return c
	}
	if in.Accounts == nil {
		return c
	}
	unset := append(append([]string{}, in.Accounts.Unset...), in.Accounts.RootEnv...)
	filtered := []string{}
	for _, e := range c.Env {
		k, _, _ := strings.Cut(e, "=")
		blocked := false
		for _, u := range unset {
			if k == u || in.OS == "windows" && strings.EqualFold(k, u) {
				blocked = true
				break
			}
		}
		if !blocked {
			filtered = append(filtered, e)
		}
	}
	c.Env = append(filtered, in.ProfileEnv()...)
	c.Unset = append(append([]string{}, c.Unset...), unset...)
	if in.Binary != "" && len(c.Argv) > 0 && c.Argv[0] == string(in.Agent) {
		c.Argv = append([]string{}, c.Argv...)
		c.Argv[0] = in.Binary
	}
	return c
}
