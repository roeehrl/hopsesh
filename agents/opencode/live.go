package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A session is open while OpenCode runs a turn of it: its row says so (time_busy after
// time_idle, or compacting), or the background service lists it as active. A turn the
// service was running when it stopped stays claimed (time_suspended) and resumes when the
// service starts again: such a session is open too. An idle TUI or desktop window that
// shows a session leaves no trace hopsesh can see.

// Words for LiveInfo.Status.
const (
	statusWorking   = "working"
	statusSuspended = "suspended until OpenCode's background service starts again"
)

// liveRow is what a session's row says about its turns.
type liveRow struct {
	busy, idle, suspended, compacting int64
}

func (r liveRow) working() bool { return r.busy != 0 && r.busy > r.idle || r.compacting != 0 }

func (r sqlReader) liveRows(ctx context.Context) (map[agent.SessionID]liveRow, error) {
	rows, err := r.sq.Query(ctx, r.db, liveQuery)
	if err != nil {
		return nil, err
	}
	out := make(map[agent.SessionID]liveRow, len(rows.Rows))
	for _, row := range rows.Rows {
		out[agent.SessionID(row.String("id"))] = liveRow{busy: row.Int("time_busy"), idle: row.Int("time_idle"),
			suspended: row.Int("time_suspended"), compacting: row.Int("time_compacting")}
	}
	return out, nil
}

// activeSessions are the sessions the running background service has a turn of
// (GET /api/session/active). With the service stopped none runs there.
func activeSessions(ctx context.Context, h agent.Host, svc service) (map[agent.SessionID]bool, error) {
	if svc.State != serviceRunning {
		return map[agent.SessionID]bool{}, nil
	}
	var page struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := apiJSON(ctx, h, svc, "GET", "/api/session/active", nil, &page); err != nil {
		return nil, err
	}
	out := make(map[agent.SessionID]bool, len(page.Data))
	for k := range page.Data {
		out[agent.SessionID(k)] = true
	}
	return out, nil
}

// Live says which sessions run a turn now (or have one suspended).
func (m *Module) Live(ctx context.Context, h agent.Host, in agent.Install, ids []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	var rows map[agent.SessionID]liveRow
	if r, ok := newSQLReader(h, in); ok {
		var err error
		rows, err = r.liveRows(ctx)
		switch {
		case errors.Is(err, agent.ErrNotInstalled):
			rows = map[agent.SessionID]liveRow{}
		case readUnsupported(err):
			rows = nil
		case err != nil:
			return nil, err
		}
	}
	svc := probeService(ctx, h)
	active, err := activeSessions(ctx, h, svc)
	if err != nil {
		active = nil // the rows still say what they say
	}
	out := make(map[agent.SessionID]agent.LiveInfo, len(ids))
	for _, sid := range ids {
		row, known := rows[sid]
		switch {
		case active[sid] || known && row.working():
			out[sid] = agent.LiveInfo{State: agent.Live, Status: statusWorking}
		case known && row.suspended != 0:
			out[sid] = agent.LiveInfo{State: agent.Live, Status: statusSuspended}
		case known || rows == nil && active != nil && svc.State == serviceRunning:
			out[sid] = agent.LiveInfo{State: agent.Ended}
		default:
			out[sid] = agent.LiveInfo{State: agent.Unknown}
		}
	}
	return out, nil
}

// Stop errors.
var (
	// ErrOwnServer: the turn runs in an OpenCode process with a server of its own
	// (--standalone), which hopsesh cannot reach.
	ErrOwnServer = errors.New("the session runs in an OpenCode window with its own server (opencode --standalone); stop it there (Esc)")
	// ErrSuspended: the turn waits for the background service, which hopsesh never starts.
	ErrSuspended = errors.New("the session's turn stopped with OpenCode's background service and continues when it starts again; open OpenCode and stop the turn there (Esc)")
	// ErrStillRunning: the turn did not end within the grace period.
	ErrStillRunning = errors.New("the OpenCode session is still working; stop it in OpenCode (Esc), then try again")
)

// Stop interrupts the session's turn through the background service
// (POST /api/session/<id>/interrupt) and waits for it to end. It never signals a process:
// the service serves every session, its desktop app and other windows, and a TUI attached
// to it is only a client.
func (m *Module) Stop(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, grace time.Duration) error {
	live, err := m.Live(ctx, h, in, []agent.SessionID{s.Key.Session})
	if err != nil {
		return err
	}
	li := live[s.Key.Session]
	if li.State != agent.Live {
		return nil
	}
	svc := probeService(ctx, h)
	switch {
	case svc.State != serviceRunning && li.Status == statusSuspended:
		return ErrSuspended
	case svc.State != serviceRunning:
		return ErrOwnServer
	}
	var ans struct {
		Interrupted bool `json:"interrupted"`
	}
	if err := apiJSON(ctx, h, svc, "POST", "/api/session/"+string(s.Key.Session)+"/interrupt", nil, &ans); err != nil {
		return fmt.Errorf("OpenCode did not interrupt the session: %w", err)
	}
	if !ans.Interrupted && li.Status != statusSuspended {
		// The service runs no turn of it: another process with its own server does.
		if again, err := m.Live(ctx, h, in, []agent.SessionID{s.Key.Session}); err == nil && again[s.Key.Session].State == agent.Live {
			return ErrOwnServer
		}
		return nil
	}
	deadline := time.Now().Add(grace)
	for {
		again, err := m.Live(ctx, h, in, []agent.SessionID{s.Key.Session})
		if err != nil {
			return err
		}
		if again[s.Key.Session].State != agent.Live {
			return nil
		}
		if !time.Now().Before(deadline) {
			return ErrStillRunning
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
