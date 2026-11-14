package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A session's folder changes the way OpenCode's own /cd changes it: `opencode api POST
// /api/session/{id}/move`. OpenCode queues the move and its background service applies it
// at the session's next step boundary, adding a location-switched message; hopsesh waits
// for that message. A private server (--standalone) ends before it applies the move and
// leaves the session marked busy, so with the service stopped hopsesh does not ask.

// How long ChangeDir waits for OpenCode to apply a move, and how often it looks: in the
// database, or, where hopsesh cannot read it, through OpenCode's API (a process each time,
// so less often).
var (
	cdWait    = 30 * time.Second
	cdPoll    = 100 * time.Millisecond
	cdAPIPoll = time.Second
)

// errNoService: only OpenCode's running background service applies a folder change.
var errNoService = errors.New("OpenCode's background service is not running, and only it applies a folder change " +
	"(a private server ends before it does, and leaves the session marked busy)")

func noServiceCD(dir string) error {
	return fmt.Errorf("%w; type /cd %s in the session in OpenCode, or start OpenCode (the desktop app, or `opencode service start`), then try again", errNoService, dir)
}

// PlanChangeDir has OpenCode's background service move the session (DirAgent); its anchor
// stays where it is.
func (m *Module) PlanChangeDir(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, dir string) (agent.DirPlan, error) {
	main := s.Path
	if main == "" {
		main = anchorOf(h, in, s.Key.Session)
	}
	p := agent.DirPlan{From: s.CWD, To: dir, Method: agent.DirAgent, Main: main,
		Records: []string{"a location-switched message, as OpenCode's own /cd adds"},
		Notes:   []string{"OpenCode reads the new folder's AGENTS.md and settings when the session continues"}}
	if svc := probeService(ctx, h); svc.State != serviceRunning {
		p.Blockers = append(p.Blockers, noServiceCD(dir).Error())
	}
	return p, nil
}

// ChangeDir asks OpenCode's background service to move the session and waits until it
// has: the session's newest location-switched message names the folder.
func (m *Module) ChangeDir(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, p agent.DirPlan) error {
	svc := probeService(ctx, h)
	if svc.State != serviceRunning {
		return noServiceCD(p.To)
	}
	sid := s.Key.Session
	before, _, viaAPI, err := m.lastSwitch(ctx, h, in, svc, sid)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"directory": p.To})
	path := "/api/session/" + string(sid) + "/move"
	res, err := api(ctx, h, svc, "POST", path, body)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("OpenCode did not move the session: %w", apiError("POST", path, res))
	}
	deadline := time.Now().Add(cdWait)
	for {
		poll := cdPoll
		if viaAPI {
			// An API call without --standalone starts the service when it stopped since:
			// ask first, and stop waiting when it is not running.
			poll = cdAPIPoll
			if probeService(ctx, h).State != serviceRunning {
				return fmt.Errorf("OpenCode took the folder change, but its background service stopped before it made it; it makes it at the session's next step once OpenCode runs again")
			}
		}
		mid, dir, api, err := m.lastSwitch(ctx, h, in, svc, sid)
		if err != nil {
			return err
		}
		viaAPI = api
		if mid != before && dir == p.To {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("OpenCode took the folder change but has not made it after %s; it makes it at the session's next step", cdWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

const lastSwitchQuery = `SELECT m.id, json_extract(m.data, '$.location.directory') AS dir FROM session_message m ` +
	`WHERE m.session_id = ? AND m.type = 'location-switched' ORDER BY m.seq DESC LIMIT 1`

// lastSwitch is the session's newest location-switched message and the folder it names
// ("" for none): from the database, else from OpenCode's API (viaAPI).
func (m *Module) lastSwitch(ctx context.Context, h agent.Host, in agent.Install, svc service, sid agent.SessionID) (mid, dir string, viaAPI bool, err error) {
	if r, ok, err := openSQL(ctx, h, in); ok {
		if err == nil {
			var rows agent.Rows
			if rows, err = r.sq.Query(ctx, r.db, lastSwitchQuery, string(sid)); err == nil {
				if len(rows.Rows) == 0 {
					return "", "", false, nil
				}
				return rows.Rows[0].String("id"), nativeDir(h, rows.Rows[0].String("dir")), false, nil
			}
		}
		if !readUnsupported(err) {
			return "", "", false, err
		}
	}
	var page struct {
		Data []struct {
			ID       string   `json:"id"`
			Location location `json:"location"`
		} `json:"data"`
	}
	q := url.Values{"type": {"location-switched"}, "order": {"desc"}, "limit": {"1"}}
	if err := apiJSON(ctx, h, svc, "GET", "/api/session/"+string(sid)+"/message?"+q.Encode(), nil, &page); err != nil {
		return "", "", true, err
	}
	if len(page.Data) == 0 {
		return "", "", true, nil
	}
	return page.Data[0].ID, nativeDir(h, page.Data[0].Location.Directory), true, nil
}

// InSession is OpenCode's own folder change, typed in the session.
func (*Module) InSession(_ agent.Install, dir string) string { return "/cd " + dir }

// ChangeDirHelp says how OpenCode changes a session's folder, and how undo puts it back.
func (*Module) ChangeDirHelp() (how, undo string) {
	return "OpenCode's background service moves the session, as its own /cd does (it must be running)", "OpenCode moves it back"
}
