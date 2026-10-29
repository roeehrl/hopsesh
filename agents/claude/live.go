package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
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
	// Name is the session's name, set by the Claude app.
	Name string `json:"name"`
	// Bridge is the Remote Control session the process is connected to: Claude Code
	// writes its id when Remote Control connects and null when it disconnects; a process
	// that never connected has none.
	Bridge json.RawMessage `json:"bridgeSessionId"`
	// Version is the Claude Code that wrote the entry.
	Version string `json:"version"`
	// written is the registry file's modification time (not in the file).
	written time.Time
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
			le.written = e.ModTime()
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

// Live reports open sessions from Claude Code's process registry. One session can run in
// several processes (a terminal and the Claude app); all of them are in Procs, and the
// main one (a waiting one first, then the latest to write its entry) gives PID, Status
// and App.
func (m *Module) Live(ctx context.Context, h agent.Host, in agent.Install, ids []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	reg, err := registry(ctx, h, in)
	if err != nil {
		return nil, err
	}
	out := make(map[agent.SessionID]agent.LiveInfo, len(ids))
	for _, sid := range ids {
		out[sid] = agent.LiveInfo{State: agent.Ended}
	}
	bySession := map[agent.SessionID][]liveEntry{}
	for _, le := range reg {
		sid := agent.SessionID(le.SessionID)
		if _, asked := out[sid]; asked {
			bySession[sid] = append(bySession[sid], le)
		}
	}
	for sid, les := range bySession {
		out[sid] = liveInfo(les)
	}
	return out, nil
}

// bridgeRecorded is the oldest Claude Code seen writing bridgeSessionId to its registry.
const bridgeRecorded = "2.1.300"

// bridgeID is the Remote Control session id ("session_…") in a bridgeSessionId value.
var bridgeID = regexp.MustCompile(`^session_[A-Za-z0-9]{8,64}$`)

// remote is the process's Remote Control state and, when on, the page it reaches the
// session at.
func (le liveEntry) remote() (state, url string) {
	if len(le.Bridge) == 0 {
		// Never connected, in a version that records the connection (2.1.300 is the
		// oldest seen recording it); an older one doesn't say.
		if agent.HookVersionAtLeast(le.Version, bridgeRecorded) {
			return agent.RemoteOff, ""
		}
		return "", ""
	}
	var id *string
	if json.Unmarshal(le.Bridge, &id) != nil {
		return "", ""
	}
	if id == nil || *id == "" {
		return agent.RemoteOff, ""
	}
	if bridgeID.MatchString(*id) {
		return agent.RemoteOn, "https://claude.ai/code/" + *id
	}
	return agent.RemoteOn, ""
}

// liveInfo describes a session from its running processes' entries.
func liveInfo(les []liveEntry) agent.LiveInfo {

	if len(les) == 0 {
		return agent.LiveInfo{State: agent.Ended}
	}
	// A registry can contain stale aliases for the same PID. Its newest record wins.
	unique := map[int]liveEntry{}
	for _, le := range les {
		if old, ok := unique[le.PID]; !ok || le.written.After(old.written) {
			unique[le.PID] = le
		}
	}
	les = make([]liveEntry, 0, len(unique))
	for _, le := range unique {
		les = append(les, le)
	}
	slices.SortFunc(les, func(a, b liveEntry) int {
		if aw, bw := a.WaitingFor != "", b.WaitingFor != ""; aw != bw {
			if aw {
				return -1
			}
			return 1
		}
		if c := b.written.Compare(a.written); c != 0 {
			return c
		}
		return cmp.Compare(a.PID, b.PID)
	})
	top := les[0]
	status := top.Status
	if top.WaitingFor != "" {
		status = "waiting for " + top.WaitingFor
	}
	li := agent.LiveInfo{State: agent.Live, PID: top.PID, Status: status, App: top.Entrypoint == "claude-desktop"}
	var named time.Time
	for _, le := range les {
		rc, url := le.remote()
		li.Procs = append(li.Procs, agent.LiveProc{PID: le.PID, ObservedAt: le.written, App: le.Entrypoint == "claude-desktop", Waiting: le.WaitingFor != "", RemoteControl: rc})
		if rc == agent.RemoteOn && li.RemoteControl != agent.RemoteOn {
			li.RemoteControl, li.RemoteURL = agent.RemoteOn, url
		}
		if le.Name != "" && (li.Name == "" || le.written.After(named)) {
			li.Name, named = le.Name, le.written
		}
	}
	// Off only when every process says so; a process that doesn't say leaves it unknown.
	if li.RemoteControl == "" && !slices.ContainsFunc(li.Procs, func(p agent.LiveProc) bool { return p.RemoteControl != agent.RemoteOff }) {
		li.RemoteControl = agent.RemoteOff
	}
	return li
}

// ErrStillRunning means a stopped session did not exit in time.
var ErrStillRunning = errors.New("the session did not exit in time; quit it yourself (type /exit in it), then try again")

// Stop asks every process that has the session open to exit (SIGTERM lets each finish
// writing its transcript). The registry is read again first, so a recycled pid is never
// signalled.
func (m *Module) Stop(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, grace time.Duration) error {
	reg, err := registry(ctx, h, in)
	if err != nil {
		return err
	}
	var pids []int
	for _, le := range reg {
		if le.SessionID == string(s.Key.Session) && !slices.Contains(pids, le.PID) {
			pids = append(pids, le.PID)
		}
	}
	if len(pids) == 0 {
		return nil // already gone
	}
	for _, pid := range pids {
		if err := h.Procs().Terminate(ctx, pid); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		alive, err := h.Procs().Alive(ctx, pids)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(pids, func(pid int) bool { return alive[pid] }) {
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
