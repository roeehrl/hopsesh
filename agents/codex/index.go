package codex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// appServer sends requests to a one-shot `codex app-server` on this machine and returns
// its output lines once until appears in them (or wait passes). Codex stops at the end of
// its input before answering, so the input stays open until then.
func appServer(ctx context.Context, h agent.Host, in agent.Install, reqs []map[string]any, until string, wait time.Duration) ([][]byte, error) {
	initialize, _ := marshal(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "hopsesh", "version": "1"}}})
	var stdin bytes.Buffer
	all := append([]map[string]any{{"method": "initialized"}}, reqs...)
	for _, r := range all {
		b, err := marshal(r)
		if err != nil {
			return nil, err
		}
		stdin.Write(b)
		stdin.WriteByte('\n')
	}
	initialized := false
	untilID := 0
	if strings.HasPrefix(until, `{"id":`) {
		_, _ = fmt.Sscanf(until, `{"id":%d,`, &untilID)
	}
	untilMethod := strings.TrimSuffix(strings.TrimPrefix(until, `"method":"`), `"`)
	exchange := func(line []byte) ([]byte, bool) {
		var r reply
		if json.Unmarshal(line, &r) != nil {
			return nil, false
		}
		if r.Method != "" {
			return nil, untilID == 0 && r.Method == untilMethod
		}
		if r.ID != nil && *r.ID == 1 && !initialized && (r.Error != nil || len(r.Result) > 0) {
			initialized = true
			if r.Error != nil {
				return nil, true
			}
			return stdin.Bytes(), false
		}
		done := untilID != 0 && r.ID != nil && *r.ID == untilID || untilID == 0 && r.Method == untilMethod
		// Stop immediately on a request error instead of waiting for an impossible completion.
		return nil, done || r.ID != nil && *r.ID > 1 && r.Error != nil
	}

	res, err := h.Exec().Run(ctx, []string{in.Binary, "app-server"}, agent.RunOptions{
		Stdin: append(initialize, '\n'), HoldStdin: wait, StdinReply: exchange, Timeout: wait + 5*time.Second,
		Env: []string{"CODEX_HOME=" + in.Root(home)},
	})
	if err != nil {
		return nil, err
	}
	var lines [][]byte
	sc := bufio.NewScanner(bytes.NewReader(res.Stdout))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		lines = append(lines, append([]byte(nil), sc.Bytes()...))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// reply is an app-server answer or notification.
type reply struct {
	ID     *int            `json:"id"`
	Result json.RawMessage `json:"result"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// AfterInstall makes Codex's own session lists show a session hopsesh installed, under
// its name. Codex lists sessions from an index it fills once, so a rollout added later
// stays out of `codex resume` and the app until Codex reads it by id; hopsesh asks
// `codex app-server` to (thread/read), and to record the name (thread/name/set). Codex
// not installed here is not an error: it finds the session by id when resumed.
func (m *Module) AfterInstall(ctx context.Context, h agent.Host, in agent.Install, key agent.SessionKey, p agent.Placement) error {
	// A copy coming home may replace one an older hopsesh labelled. Codex keeps the label
	// as the thread's name in the shared index, where it outlives the replaced file: name
	// it again.
	if cur := names(h, in)[string(key.Session)]; cur != "" {
		if _, orig, ok := agent.StripLegacyLabel(cur); ok {
			name := p.Name
			if name == "" {
				name = orig
			}
			if err := setName(h, in, string(key.Session), name); err != nil {
				return fmt.Errorf("clearing the legacy label of the copy this replaced: %w", err)
			}
		}
	}
	if in.Binary == "" || !h.Facts().Local {
		return nil
	}
	reqs := []map[string]any{{"id": 2, "method": "thread/read", "params": map[string]any{"threadId": string(key.Session), "includeTurns": false}}}
	last := 2
	if p.Name != "" {
		reqs = append(reqs, map[string]any{"id": 3, "method": "thread/name/set", "params": map[string]any{"threadId": string(key.Session), "name": p.Name}})
		last = 3
	}
	lines, err := appServer(ctx, h, in, reqs, fmt.Sprintf(`{"id":%d,`, last), 15*time.Second)
	if err != nil {
		return fmt.Errorf("asking codex app-server to list the session: %w", err)
	}
	want := map[int]bool{}
	for i := 2; i <= last; i++ {
		want[i] = true
	}
	for _, l := range lines {
		var r reply
		if json.Unmarshal(l, &r) != nil || r.ID == nil || !want[*r.ID] {
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

// CanImport reports that Codex's importer reads Claude Code sessions.
func (m *Module) CanImport(from agent.ID) bool { return from == "claude" }

// Import has Codex's own importer convert a Claude Code session file
// (externalAgentConfig/import) into a new thread.
func (m *Module) Import(ctx context.Context, h agent.Host, in agent.Install, from agent.ID, path, cwd, title string) (agent.SessionID, error) {
	if !m.CanImport(from) {
		return "", fmt.Errorf("%w: Codex imports only Claude Code sessions", agent.ErrUnsupported)
	}
	if in.Binary == "" || !h.Facts().Local {
		return "", fmt.Errorf("%w: Codex's importer needs codex installed on this machine", agent.ErrUnsupported)
	}
	capacity, err := m.ContextCapacity(ctx, h, in, nil)
	if err != nil {
		return "", err
	}
	// Keep a checked snapshot for recovery. Codex only imports paths in its detected
	// Claude inventory, so pass the original path and verify it against this snapshot
	// again after import. Core also measures the resulting native active context.
	raw, err := h.FS().ReadFile(path, int64(capacity.Allowance()/2))
	if err != nil {
		return "", fmt.Errorf("vendor import input cannot fit safely; use portable history: %w", err)
	}
	if len(raw)*2 > capacity.Allowance() {
		return "", fmt.Errorf("vendor import exceeds context allowance; use portable history")
	}
	snapshot := h.Path().Join(in.Root(home), "hopsesh", "imports", newUUID()+".jsonl")
	if err = h.FS().WriteFile(snapshot, raw, 0o600); err != nil {
		return "", err
	}
	item := map[string]any{"itemType": "SESSIONS", "description": "a session hopsesh brings over", "cwd": cwd,
		"details": map[string]any{"sessions": []any{map[string]any{"cwd": cwd, "path": path, "title": title}}}}
	reqs := []map[string]any{{"id": 2, "method": "externalAgentConfig/import",
		"params": map[string]any{"migrationItems": []any{item}, "providerId": "hopsesh", "source": "hopsesh"}}}
	const done = "externalAgentConfig/import/completed"
	lines, err := appServer(ctx, h, in, reqs, `"method":"`+done+`"`, 5*time.Minute)
	if err != nil {
		return "", fmt.Errorf("running the Codex importer: %w", err)
	}
	for _, l := range lines {
		var r reply
		if json.Unmarshal(l, &r) != nil {
			continue
		}
		if r.ID != nil && *r.ID == 2 && r.Error != nil {
			return "", errors.New("the Codex importer refused: " + r.Error.Message)
		}
		if r.Method != done {
			continue
		}
		var c struct {
			Results []struct {
				Successes []struct {
					Target string `json:"target"`
				} `json:"successes"`
				Failures []struct {
					Message string `json:"message"`
				} `json:"failures"`
			} `json:"itemTypeResults"`
		}
		if err := json.Unmarshal(r.Params, &c); err != nil {
			return "", err
		}
		var fails []string
		for _, t := range c.Results {
			for _, s := range t.Successes {
				if s.Target != "" {
					current, err := h.FS().ReadFile(path, int64(capacity.Allowance()/2))
					if err != nil || !bytes.Equal(current, raw) {
						return agent.SessionID(s.Target), fmt.Errorf("source changed during vendor import; retain the imported session for undo and make a new plan")
					}
					return agent.SessionID(s.Target), nil
				}
			}
			for _, f := range t.Failures {
				fails = append(fails, f.Message)
			}
		}
		return "", errors.New("the Codex importer did not import the session: " + strings.Join(fails, "; "))
	}
	return "", errors.New("the Codex importer did not finish in time")
}

// Account identifies the Codex login on a machine through Codex itself (account/read),
// never by reading auth.json. ChatGPT logins are keyed by a hash of the account's email;
// an API key or another provider has no identity to compare.
func (m *Module) Account(ctx context.Context, h agent.Host, in agent.Install) (agent.Account, error) {
	if in.Binary == "" {
		return agent.Account{}, fmt.Errorf("%w: codex is not installed there", agent.ErrNotInstalled)
	}
	lines, err := appServer(ctx, h, in, []map[string]any{{"id": 2, "method": "account/read", "params": map[string]any{"refreshToken": false}}}, `{"id":2,`, 20*time.Second)
	if err != nil {
		return agent.Account{}, err
	}
	for _, l := range lines {
		var r struct {
			ID     *int `json:"id"`
			Result struct {
				Account *struct {
					Type  string `json:"type"`
					Email string `json:"email"`
					Plan  string `json:"planType"`
				} `json:"account"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(l, &r) != nil || r.ID == nil || *r.ID != 2 {
			continue
		}
		if r.Error != nil {
			return agent.Account{}, errors.New("codex app-server: " + r.Error.Message)
		}
		a := r.Result.Account
		switch {
		case a == nil:
			return agent.Account{Label: "not logged in", Confidence: "unknown", Observation: "signed-out"}, nil
		case a.Type == "chatgpt" && a.Email != "":
			sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(a.Email))))
			return agent.Account{Key: "chatgpt:" + hex.EncodeToString(sum[:8]), Label: "ChatGPT " + a.Plan, LoggedIn: true, Email: a.Email, Provider: a.Type, Confidence: "limited", Observation: "chatgpt:" + hex.EncodeToString(sum[:])}, nil
		default:
			return agent.Account{Label: a.Type, LoggedIn: true, Provider: a.Type, Confidence: "unknown", Observation: a.Type}, nil
		}
	}
	return agent.Account{}, errors.New("no answer from codex app-server")
}

// Sanitize is the policy for a move to a machine signed in to another account: Codex's
// encrypted reasoning and compaction are bound to the organization that produced them, and
// replaying them under another one makes the session fail to resume, so those records go
// (the conversation itself stays; Codex compacts again when it needs to).
func (m *Module) Sanitize() agent.RewritePolicy {
	return agent.RewritePolicy{RenumberOrdinal: "ordinal", DropRecords: []agent.FieldMatch{
		{Field: "payload.type", Values: []string{"reasoning", "compaction", "compaction_summary"}},
		{Field: "type", Values: []string{"compacted"}},
	}}
}
