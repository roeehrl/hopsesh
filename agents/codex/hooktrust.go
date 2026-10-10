package codex

import (
	"context"
	"encoding/json"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

var _ agent.HookTrustReporter = (*Module)(nil)

// HookTrust asks Codex (app-server hooks/list) whether it trusts the hopsesh hooks.
// Codex records trust against a hash of each hook definition and skips untrusted or
// changed hooks without telling the session, so an installed hook can still deliver
// nothing. Trusting stays in Codex: /hooks in the CLI, or the review prompt in the app.
func (m *Module) HookTrust(ctx context.Context, h agent.Host, in agent.Install, commands []string) agent.HookTrust {
	fix := "Open Codex and run /hooks (or answer its Review hooks prompt), then trust the hopsesh notice hooks. Codex asks again whenever a hook changes."
	cwd := h.Facts().Home // user hooks apply in every folder
	if cwd == "" {
		cwd = in.Root(home)
	}
	lines, err := appServer(ctx, h, in, []map[string]any{{"id": 2, "method": "hooks/list", "params": map[string]any{"cwds": []string{cwd}}}}, `{"id":2,`, 15*time.Second)
	if err != nil {
		return agent.HookTrust{State: agent.HookUnknown, Detail: "could not ask Codex: " + err.Error(), Fix: fix}
	}
	return hookTrustFrom(lines, commands, fix)
}

func hookTrustFrom(lines [][]byte, commands []string, fix string) agent.HookTrust {
	ours := map[string]bool{}
	for _, c := range commands {
		ours[c] = true
	}
	for _, line := range lines {
		var r struct {
			ID     *int `json:"id"`
			Result struct {
				Data []struct {
					Hooks []struct {
						EventName   string `json:"eventName"`
						Command     string `json:"command"`
						Enabled     bool   `json:"enabled"`
						TrustStatus string `json:"trustStatus"`
					} `json:"hooks"`
				} `json:"data"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(line, &r) != nil || r.ID == nil || *r.ID != 2 {
			continue
		}
		if r.Error != nil {
			return agent.HookTrust{State: agent.HookUnknown, Detail: "Codex: " + r.Error.Message, Fix: fix}
		}
		var review, disabled []string
		found := 0
		for _, d := range r.Result.Data {
			for _, hk := range d.Hooks {
				if !ours[hk.Command] {
					continue
				}
				found++
				switch {
				case !hk.Enabled:
					disabled = append(disabled, hk.EventName)
				case hk.TrustStatus == "trusted" || hk.TrustStatus == "managed":
				default: // untrusted, modified, or a status this hopsesh does not know
					review = append(review, hk.EventName)
				}
			}
		}
		switch {
		case found == 0:
			return agent.HookTrust{State: agent.HookMissing, Detail: "Codex does not list the hopsesh hooks; reinstall them from hopsesh."}
		case len(disabled) > 0:
			return agent.HookTrust{State: agent.HookDisabled, Events: disabled, Fix: "Open Codex, run /hooks and turn the hopsesh notice hooks back on."}
		case len(review) > 0:
			return agent.HookTrust{State: agent.HookNeedsReview, Events: review, Fix: fix}
		}
		return agent.HookTrust{State: agent.HookTrusted}
	}
	return agent.HookTrust{State: agent.HookUnknown, Detail: "Codex did not answer hooks/list", Fix: fix}
}
