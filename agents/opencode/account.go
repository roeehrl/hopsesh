package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Account lists the providers OpenCode is signed in to, from `opencode auth list
// --format json`, which prints provider names and connection labels and never a secret
// (decision D7). OpenCode signs in per provider, not per person, so the account has no
// key: moving a session is never taken as moving it to another account. Its database's
// credential and account tables are never read.
func (m *Module) Account(ctx context.Context, h agent.Host, in agent.Install) (agent.Account, error) {
	svc := probeService(ctx, h)
	argv := []string{"auth", "list", "--format", "json"}
	if svc.standalone() {
		argv = append(argv, "--standalone")
	}
	res, err := run(ctx, h, argv, agent.RunOptions{})
	if err != nil {
		return agent.Account{}, err
	}
	if res.Code != 0 {
		return agent.Account{}, commandError("auth list", res)
	}
	return accountOf(res.Stdout)
}

// integration is one provider in `auth list --format json` (OpenCode's Integration with
// its Connection.Info list): a stored credential (its id, the label the person wrote and
// how it signs in) or an environment variable. Only the provider's id and name, and each
// connection's kind and problem, are read: never a label, an id or a variable's name.
type integration struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Connections []struct {
		Type   string          `json:"type"` // credential | env
		Status json.RawMessage `json:"status"`
	} `json:"connections"`
}

// needsAuth reports a connection whose provider asks the person to sign in again
// (Connection.Status {"status":"needs_auth", …}).
func needsAuth(status json.RawMessage) bool {
	var st struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(status, &st) == nil && st.Status == "needs_auth"
}

// accountOf reads the providers OpenCode is signed in to. A provider set up only in
// opencode.json (a local model's, say) is not listed by OpenCode, so "no provider signed
// in" says that too.
func accountOf(out []byte) (agent.Account, error) {
	var list []integration
	if err := json.Unmarshal(bytes.TrimSpace(out), &list); err != nil {
		return agent.Account{}, fmt.Errorf("OpenCode's list of sign-ins: %w", err)
	}
	var names, ids, again []string
	for _, it := range list {
		working := false
		for _, c := range it.Connections {
			if !needsAuth(c.Status) {
				working = true
			}
		}
		name := it.Name
		if name == "" {
			name = it.ID
		}
		switch {
		case working:
			names = append(names, name)
			ids = append(ids, it.ID)
		case len(it.Connections) > 0:
			again = append(again, name)
		}
	}
	sort.Strings(names)
	sort.Strings(ids)
	sort.Strings(again)
	a := agent.Account{LoggedIn: len(names) > 0, Confidence: "limited", Label: strings.Join(names, ", "),
		Observation: strings.Join(ids, ","), Why: "OpenCode has no remote control"}
	if !a.LoggedIn {
		a.Label = "no provider signed in (providers set up only in opencode.json are not listed)"
	}
	if len(again) > 0 {
		a.Label += "; sign in again to " + strings.Join(again, ", ")
	}
	return a, nil
}
