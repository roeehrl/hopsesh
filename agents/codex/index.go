package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// AfterInstall makes Codex's own session lists show a session hopsesh installed, under
// its name. Codex lists sessions from an index it fills once, so a rollout added later
// stays out of `codex resume` and the app until Codex reads it by id; hopsesh asks
// `codex app-server` to (thread/read), and to record the name (thread/name/set). Codex
// not installed here is not an error: it finds the session by id when resumed.
func (m *Module) AfterInstall(ctx context.Context, h agent.Host, in agent.Install, key agent.SessionKey, p agent.Placement) error {
	if in.Binary == "" || !h.Facts().Local {
		return nil
	}
	reqs := []map[string]any{
		{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "hopsesh", "version": "1"}}},
		{"method": "initialized"},
		{"id": 2, "method": "thread/read", "params": map[string]any{"threadId": string(key.Session), "includeTurns": false}},
	}
	if p.Name != "" {
		reqs = append(reqs, map[string]any{"id": 3, "method": "thread/name/set", "params": map[string]any{"threadId": string(key.Session), "name": p.Name}})
	}
	var stdin bytes.Buffer
	for _, r := range reqs {
		b, err := marshal(r)
		if err != nil {
			return err
		}
		stdin.Write(b)
		stdin.WriteByte('\n')
	}
	res, err := h.Exec().Run(ctx, []string{in.Binary, "app-server"}, agent.RunOptions{
		Stdin: stdin.Bytes(), HoldStdin: 5 * time.Second, Timeout: 60 * time.Second,
		Env: []string{"CODEX_HOME=" + in.Root(home)},
	})
	if err != nil {
		return fmt.Errorf("asking codex app-server to list the session: %w", err)
	}
	want := map[int]bool{2: true}
	if p.Name != "" {
		want[3] = true
	}
	sc := bufio.NewScanner(bytes.NewReader(res.Stdout))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var r struct {
			ID    *int `json:"id"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.ID == nil || !want[*r.ID] {
			continue
		}
		if r.Error != nil {
			return errors.New("codex app-server did not list the session: " + r.Error.Message)
		}
		delete(want, *r.ID)
	}
	if len(want) > 0 {
		return errors.New("no answer from codex app-server in time; the session still opens with codex resume <id>")
	}
	return nil
}
