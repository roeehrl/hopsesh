package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// OpenCode keeps no flag that says a person chose a session's title. hopsesh records the
// titles it gave (a rename, a copy kept beside the original) in
// {data}/hopsesh/opencode/titles.jsonl, one {"id","title"} line each, the last for an id
// counting; a session whose title is still the one recorded is listed with a custom title.
// Each line is a standalone append: an operation's undo takes out its own line only, and
// a line added later never makes an earlier operation's undo refuse.

func titlesFile(h agent.Host, in agent.Install) string {
	return h.Path().Join(in.Root(rootData), "hopsesh", string(id), "titles.jsonl")
}

type titleRecord struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// customTitles are the titles hopsesh gave, by session id (none when the file is missing
// or unreadable; a line that does not read is skipped).
func customTitles(ctx context.Context, h agent.Host, in agent.Install) map[string]string {
	if in.Root(rootData) == "" {
		return nil
	}
	b, err := h.FS().ReadFile(titlesFile(h, in), 16<<20)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		var r titleRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.ID != "" {
			out[r.ID] = r.Title
		}
	}
	return out
}

// recordTitle records a title hopsesh gave a session. It is a note for listings only: a
// failure to write it changes nothing else.
func recordTitle(ctx context.Context, h agent.Host, in agent.Install, sid agent.SessionID, title string) {
	if in.Root(rootData) == "" {
		return
	}
	if customTitles(ctx, h, in)[string(sid)] == title {
		return
	}
	b, err := json.Marshal(titleRecord{ID: string(sid), Title: title})
	if err != nil {
		return
	}
	_ = h.FS().Append(titlesFile(h, in), append(b, '\n'), agent.AppendOptions{NewLine: true, Standalone: true})
}
