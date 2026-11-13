package opencode

import (
	"context"
	"encoding/json"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// OpenCode keeps no flag that says a person chose a session's title. hopsesh records the
// titles it gave (a rename, a copy kept beside the original) in
// {data}/hopsesh/opencode/titles.json, by session id; a session whose title is still the
// one recorded is listed with a custom title.

func titlesFile(h agent.Host, in agent.Install) string {
	return h.Path().Join(in.Root(rootData), "hopsesh", string(id), "titles.json")
}

// customTitles are the titles hopsesh gave, by session id (none when the file is missing
// or unreadable).
func customTitles(ctx context.Context, h agent.Host, in agent.Install) map[string]string {
	if in.Root(rootData) == "" {
		return nil
	}
	b, err := h.FS().ReadFile(titlesFile(h, in), 4<<20)
	if err != nil {
		return nil
	}
	var out map[string]string
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// recordTitle records a title hopsesh gave a session. It is a note for listings only: a
// failure to write it changes nothing else.
func recordTitle(ctx context.Context, h agent.Host, in agent.Install, sid agent.SessionID, title string) {
	if in.Root(rootData) == "" {
		return
	}
	all := customTitles(ctx, h, in)
	if all == nil {
		all = map[string]string{}
	}
	if all[string(sid)] == title {
		return
	}
	all[string(sid)] = title
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return
	}
	_ = h.FS().WriteFile(titlesFile(h, in), append(b, '\n'), 0o600)
}
