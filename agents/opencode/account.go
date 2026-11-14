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

// integration is one provider in `auth list --format json`.
type integration struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Connections []struct {
		Type string `json:"type"` // credential | env
	} `json:"connections"`
}

func accountOf(out []byte) (agent.Account, error) {
	var list []integration
	if err := json.Unmarshal(bytes.TrimSpace(out), &list); err != nil {
		return agent.Account{}, fmt.Errorf("OpenCode's list of sign-ins: %w", err)
	}
	var names, ids []string
	for _, it := range list {
		if len(it.Connections) == 0 {
			continue
		}
		name := it.Name
		if name == "" {
			name = it.ID
		}
		names = append(names, name)
		ids = append(ids, it.ID)
	}
	sort.Strings(names)
	sort.Strings(ids)
	a := agent.Account{LoggedIn: len(names) > 0, Confidence: "limited", Label: strings.Join(names, ", "),
		Observation: strings.Join(ids, ","), Why: "OpenCode has no remote control"}
	if !a.LoggedIn {
		a.Label = "no provider signed in"
	}
	return a, nil
}
