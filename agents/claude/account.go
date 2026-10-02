package claude

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// authStatus is what `claude auth status --json` reports. The email address is not kept.
type authStatus struct {
	LoggedIn     bool   `json:"loggedIn"`
	Method       string `json:"authMethod"`       // claude.ai | api-key | …
	Provider     string `json:"apiProvider"`      // firstParty | bedrock | vertex | …
	OrgID        string `json:"orgId"`            // identifies the account
	Subscription string `json:"subscriptionType"` // pro | max | team | enterprise | …
}

// Account asks claude which login it uses (it reads its own credentials; hopsesh never
// does). The key is a hash of the organisation id.
func (m *Module) Account(ctx context.Context, h agent.Host, in agent.Install) (agent.Account, error) {
	r, err := h.Exec().Run(ctx, []string{"claude", "auth", "status", "--json"}, agent.RunOptions{Timeout: 20 * time.Second})
	if err != nil {
		return agent.Account{}, err
	}
	var a authStatus
	if err := json.Unmarshal(r.Stdout, &a); err != nil {
		return agent.Account{}, &agent.FormatError{Path: "claude auth status", Err: err}
	}
	return account(a), nil
}

func account(a authStatus) agent.Account {
	acct := agent.Account{Label: a.Subscription}
	if a.OrgID != "" {
		sum := sha256.Sum256([]byte("claude:" + a.OrgID))
		acct.Key = hex.EncodeToString(sum[:8])
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
