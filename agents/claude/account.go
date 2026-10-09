package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
// does). The key is a hash of the organisation id.
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
	return account(a), nil
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
