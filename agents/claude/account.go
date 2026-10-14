package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// authStatus is what `claude auth status --json` reports. Only public login metadata is kept.
type authStatus struct {
	Email           string `json:"email"`
	LoggedIn        bool   `json:"loggedIn"`
	Method          string `json:"authMethod"`  // claude.ai | api-key | …
	Provider        string `json:"apiProvider"` // firstParty | bedrock | vertex | …
	OrgID           string `json:"orgId"`       // identifies the organization, not the user
	ConfigDirectory string `json:"configDirectory"`
	Subscription    string `json:"subscriptionType"` // pro | max | team | enterprise | …
}

// Account asks claude which login it uses (it reads its own credentials; hopsesh never
// does). The key is a hash of the organisation id; the person, of the user id in the
// root's public identity record.
func (m *Module) Account(ctx context.Context, h agent.Host, in agent.Install) (agent.Account, error) {
	binary := in.Binary
	if binary == "" {
		binary = "claude"
	}
	r, err := h.Exec().Run(ctx, []string{binary, "auth", "status", "--json"}, agent.RunOptions{Timeout: 20 * time.Second})
	if err != nil {
		return agent.Account{}, err
	}
	var a authStatus
	if err := json.Unmarshal(r.Stdout, &a); err != nil {
		return agent.Account{}, &agent.FormatError{Path: "claude auth status", Err: err}
	}
	if a.ConfigDirectory != "" && h.Path().Clean(a.ConfigDirectory) != h.Path().Clean(in.Root("home")) {
		return agent.Account{}, fmt.Errorf("claude reported a different configuration directory; re-register this profile")
	}
	if r.Code > 1 || r.Code != 0 && a.LoggedIn {
		return agent.Account{}, fmt.Errorf("claude auth status exited %d", r.Code)
	}
	acct := account(a)
	if a.LoggedIn {
		acct.Person = person(h, in, a)
	}
	return acct, nil
}

// identity is the public part of Claude Code's own record of its login (oauthAccount in
// .claude.json). Its credentials live elsewhere (the Keychain, .credentials.json) and are
// never read.
type identity struct {
	OAuth *struct {
		Account string `json:"accountUuid"`
		Org     string `json:"organizationUuid"`
		Email   string `json:"emailAddress"`
	} `json:"oauthAccount"`
}

// person fingerprints the signed-in user from the root's .claude.json, when that record
// agrees with what claude reported (it can be stale); "" otherwise.
func person(h agent.Host, in agent.Install, a authStatus) string {
	var files []string
	if root := in.Root("home"); in.NativeDefaultRoot != "" && root == in.NativeDefaultRoot {
		files = []string{h.Path().Join(h.Facts().Home, ".claude.json")}
	} else if root != "" {
		files = []string{h.Path().Join(root, ".config.json"), h.Path().Join(root, ".claude.json")}
	}
	for _, f := range files {
		b, err := h.FS().ReadFile(f, 64<<20)
		if err != nil || len(bytes.TrimSpace(b)) == 0 {
			continue
		}
		var id identity
		if json.Unmarshal(b, &id) != nil || id.OAuth == nil {
			return ""
		}
		if id.OAuth.Org != a.OrgID || !strings.EqualFold(id.OAuth.Email, a.Email) {
			return ""
		}
		return personKey(id.OAuth.Account)
	}
	return ""
}

// accountKey fingerprints an organisation id ("" for none): the same for a login and for
// the owner a Remote Control record names.
func accountKey(org string) string {
	if org == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("claude:" + org))
	return hex.EncodeToString(sum[:8])
}

// personKey fingerprints a Claude account (user) id ("" for none).
func personKey(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("claude-person:" + id))
	return hex.EncodeToString(sum[:8])
}

func account(a authStatus) agent.Account {
	raw, _ := json.Marshal([]any{a.LoggedIn, a.Method, a.Provider, a.OrgID, a.Email})
	fingerprint := sha256.Sum256(raw)
	acct := agent.Account{Label: a.Subscription, Key: accountKey(a.OrgID), LoggedIn: a.LoggedIn, Email: a.Email, Provider: a.Provider, Confidence: "limited", Observation: hex.EncodeToString(fingerprint[:])}
	if a.LoggedIn && strings.Contains(strings.ToLower(a.Method), "console") {
		acct.IsolationWhy = "Claude Console logins without API keys are not isolated by CLAUDE_CONFIG_DIR; use a supported subscription login"
	}
	// Remote Control needs a claude.ai subscription login with Anthropic as the provider.
	switch {
	case !a.LoggedIn:
		acct.Why = "Claude Code is not logged in there"
	case a.Provider != "" && a.Provider != "firstParty":
		acct.Why = "Claude Code there uses " + a.Provider + ", and Remote Control needs a claude.ai subscription login"
	case a.Method != "" && !strings.EqualFold(a.Method, "claude.ai"):
		acct.Why = "Claude Code there is logged in with " + a.Method + ", and Remote Control needs a claude.ai subscription login"
	default:
		acct.RemoteControl = true
	}
	return acct
}

// LocalIdentity reads the root's oauthAccount record: what Claude Code itself stamps as a
// run's organisation.
func (m *Module) LocalIdentity(root string, native bool) (agent.OwnerRun, bool) {
	files := []string{filepath.Join(root, ".config.json"), filepath.Join(root, ".claude.json")}
	if native {
		home, err := os.UserHomeDir()
		if err != nil {
			return agent.OwnerRun{}, false
		}
		files = []string{filepath.Join(home, ".claude.json")}
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil || len(bytes.TrimSpace(b)) == 0 {
			continue
		}
		var id identity
		if json.Unmarshal(b, &id) != nil || id.OAuth == nil || id.OAuth.Org == "" {
			return agent.OwnerRun{}, false
		}
		return agent.OwnerRun{Key: accountKey(id.OAuth.Org), Person: personKey(id.OAuth.Account), Source: "hopsesh"}, true
	}
	return agent.OwnerRun{}, false
}
