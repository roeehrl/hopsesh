package claude

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// liveEntry is one running Claude Code process, from <config>/sessions/<pid>.json. The
// format is internal to Claude Code; unknown fields are ignored.
type liveEntry struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Status     string `json:"status"`
	WaitingFor string `json:"waitingFor"`
	// Entrypoint is what started it: "cli" in a terminal, "claude-desktop" in the Claude
	// app.
	Entrypoint string `json:"entrypoint"`
}

// registry reads the per-process registry and keeps entries whose process runs.
func registry(ctx context.Context, h agent.Host, in agent.Install) ([]liveEntry, error) {
	pa := h.Path()
	dir := pa.Join(in.Root(home), "sessions")
	entries, err := h.FS().ReadDir(dir)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []liveEntry
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".json") { // skips <pid>.<hash>.key secrets
			continue
		}
		b, err := h.FS().ReadFile(pa.Join(dir, n), 256<<10)
		if err != nil {
			continue
		}
		var le liveEntry
		if json.Unmarshal(b, &le) == nil && le.SessionID != "" && le.PID > 0 {
			out = append(out, le)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	pids := make([]int, len(out))
	for i, le := range out {
		pids[i] = le.PID
	}
	alive, err := h.Procs().Alive(ctx, pids)
	if err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, le := range out {
		if alive[le.PID] {
			kept = append(kept, le)
		}
	}
	return kept, nil
}

// Live reports open sessions from Claude Code's process registry.
func (m *Module) Live(ctx context.Context, h agent.Host, in agent.Install, ids []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	reg, err := registry(ctx, h, in)
	if err != nil {
		return nil, err
	}
	out := make(map[agent.SessionID]agent.LiveInfo, len(ids))
	for _, sid := range ids {
		out[sid] = agent.LiveInfo{State: agent.Ended}
	}
	for _, le := range reg {
		sid := agent.SessionID(le.SessionID)
		if _, asked := out[sid]; asked {
			status := le.Status
			if le.WaitingFor != "" {
				status = "waiting for " + le.WaitingFor
			}
			out[sid] = agent.LiveInfo{State: agent.Live, PID: le.PID, Status: status, App: le.Entrypoint == "claude-desktop"}
		}
	}
	return out, nil
}

// ErrStillRunning means a stopped session did not exit in time.
var ErrStillRunning = errors.New("the session did not exit in time; quit it yourself (type /exit in it), then try again")

// Stop asks the session's process to exit (SIGTERM lets it finish writing its
// transcript). The registry is read again first, so a recycled pid is never signalled.
func (m *Module) Stop(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, grace time.Duration) error {
	reg, err := registry(ctx, h, in)
	if err != nil {
		return err
	}
	pid := 0
	for _, le := range reg {
		if le.SessionID == string(s.Key.Session) {
			pid = le.PID
		}
	}
	if pid == 0 {
		return nil // already gone
	}
	if err := h.Procs().Terminate(ctx, pid); err != nil {
		return err
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		alive, err := h.Procs().Alive(ctx, []int{pid})
		if err != nil {
			return err
		}
		if !alive[pid] {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return ErrStillRunning
}
