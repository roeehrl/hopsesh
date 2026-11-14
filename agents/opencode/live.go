package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A session is open while OpenCode runs a turn of it. Its row says when a turn is in
// flight: OpenCode claims the session (time_suspended, the execution claim) when a turn
// starts, in any process, and releases it when the turn ends; since 2.2.2 it also sets
// time_busy, and time_idle once the turn ends. When the background service shuts down
// mid-turn, it keeps the claim and records no idle, and resumes the turn when it starts
// again; a process that dies leaves the claim too. So a claimed row is a running turn or a
// suspended one, and only the processes tell which: the running service lists the
// sessions it has a turn of (GET /api/session/active), and a window with its own server
// (--standalone) cannot be asked. An idle TUI or desktop window that shows a session
// leaves no trace hopsesh can see.

// Words for LiveInfo.Status.
const (
	statusWorking = "working"
	// statusSuspended: the row claims a turn while the background service is not running.
	statusSuspended = "working in an OpenCode window with its own server, or suspended until OpenCode's background service starts again"
)

// liveRow is what a session's row says about its turns.
type liveRow struct {
	busy, idle, claimed, compacting int64
}

// inFlight reports a turn started and not ended: running, or suspended.
func (r liveRow) inFlight() bool {
	return r.claimed != 0 || r.busy != 0 && r.busy > r.idle || r.compacting != 0
}

func (r sqlReader) liveRows(ctx context.Context) (map[agent.SessionID]liveRow, error) {
	rows, err := r.sq.Query(ctx, r.db, r.schema.liveQuery())
	if err != nil {
		return nil, err
	}
	out := make(map[agent.SessionID]liveRow, len(rows.Rows))
	for _, row := range rows.Rows {
		out[agent.SessionID(row.String("id"))] = liveRow{busy: row.Int("time_busy"), idle: row.Int("time_idle"),
			claimed: row.Int("time_suspended"), compacting: row.Int("time_compacting")}
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

// Live says which sessions run a turn now (or have one suspended). The rows answer alone
// unless one of the sessions has a turn in flight (or they cannot be read): only then is
// OpenCode asked, since each question is a process, and a service that stops between the
// probe and the API call would be started again by that call.
func (m *Module) Live(ctx context.Context, h agent.Host, in agent.Install, ids []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	var rows map[agent.SessionID]liveRow
	if r, ok, err := openSQL(ctx, h, in); ok {
		if err == nil {
			rows, err = r.liveRows(ctx)
		}
		switch {
		case errors.Is(err, agent.ErrNotInstalled):
			rows = map[agent.SessionID]liveRow{}
		case errors.Is(err, agent.ErrUnsupported):
			rows = nil // a database hopsesh cannot read here, or does not recognise
		case err != nil:
			return nil, err
		}
	}
	ask := rows == nil
	for _, sid := range ids {
		ask = ask || rows[sid].inFlight()
	}
	svc := service{State: serviceUnknown}
	var active map[agent.SessionID]bool
	if ask {
		svc = probeService(ctx, h)
		var err error
		if active, err = activeSessions(ctx, h, svc); err != nil {
			active = nil // the rows still say what they say
		}
	}
	out := make(map[agent.SessionID]agent.LiveInfo, len(ids))
	for _, sid := range ids {
		row, known := rows[sid]
		switch {
		case active[sid]:
			out[sid] = agent.LiveInfo{State: agent.Live, Status: statusWorking}
		case known && row.inFlight() && row.claimed != 0 && svc.State != serviceRunning:
			out[sid] = agent.LiveInfo{State: agent.Live, Status: statusSuspended}
		case known && row.inFlight():
			// With the service running, a turn it does not list runs in a window with
			// its own server.
			out[sid] = agent.LiveInfo{State: agent.Live, Status: statusWorking}
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
	// ErrSuspended: the background service is not running and the session's row claims a
	// turn: a window with its own server runs it, or it waits for the service, which
	// hopsesh never starts. Nothing tells the two apart.
	ErrSuspended = errors.New("the session's turn runs in an OpenCode window with its own server (opencode --standalone), or it stopped with OpenCode's background service and continues when the service starts again; stop it in that window (Esc), or open OpenCode and stop it there")
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
	if svc.State != serviceRunning {
		if li.Status == statusSuspended {
			return ErrSuspended
		}
		return ErrOwnServer
	}
	var ans struct {
		Interrupted bool `json:"interrupted"`
	}
	if err := apiJSON(ctx, h, svc, "POST", "/api/session/"+string(s.Key.Session)+"/interrupt", nil, &ans); err != nil {
		return fmt.Errorf("OpenCode did not interrupt the session: %w", err)
	}
	if !ans.Interrupted {
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
