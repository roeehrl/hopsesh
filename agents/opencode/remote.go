package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Where hopsesh cannot read OpenCode's database (another machine with neither a sqlite3
// shell that marks blobs nor python3, a WAL database no program has open there, Windows),
// it asks OpenCode itself: `opencode api GET /api/session` to list and `opencode session
// export` to read. That is slower (each call is a process, and a private server when the
// background service is stopped) and returns no credential bytes.

// apiSessionPage is how many sessions one API call asks for (tests ask fewer);
// apiSessionMax how many a listing reads in all.
var apiSessionPage = 500

const apiSessionMax = 20000

// apiSessions lists every session (children and archived ones included) through the API,
// page by page.
func apiSessions(ctx context.Context, h agent.Host, svc service) ([]session, error) {
	var out []session
	cursor := ""
	for len(out) < apiSessionMax {
		var page struct {
			Data   []info `json:"data"`
			Cursor struct {
				Next string `json:"next"`
			} `json:"cursor"`
		}
		q := url.Values{"limit": {fmt.Sprint(apiSessionPage)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		} else {
			q.Set("order", "desc")
		}
		if err := apiJSON(ctx, h, svc, "GET", "/api/session?"+q.Encode(), nil, &page); err != nil {
			return nil, err
		}
		for _, in := range page.Data {
			out = append(out, sessionOf(in))
		}
		if len(page.Data) < apiSessionPage || page.Cursor.Next == "" || page.Cursor.Next == cursor {
			break
		}
		cursor = page.Cursor.Next
	}
	return out, nil
}

// listAPI is List through OpenCode's API. A session's size (its settled messages), its
// prompts and the OpenCode version that wrote it are not known without exporting it, so
// they are left empty here (an untitled session stays untitled); SessionStamp exports.
func (m *Module) listAPI(ctx context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	svc := probeService(ctx, h)
	all, err := apiSessions(ctx, h, svc)
	if err != nil {
		return agent.Listing{}, err
	}
	rows := make([]listed, 0, len(all))
	for _, s := range all {
		rows = append(rows, listed{session: s})
	}
	return m.summaries(ctx, h, in, rows, "api", customTitles(ctx, h, in))
}

// transcript is a session and its settled messages: from the database, else from
// OpenCode's export.
func (m *Module) transcript(ctx context.Context, h agent.Host, in agent.Install, sid agent.SessionID) (session, []message, error) {
	if r, ok, err := openSQL(ctx, h, in); ok {
		var (
			s    session
			msgs []message
		)
		if err == nil {
			s, msgs, err = r.transcript(ctx, sid)
		}
		if !readUnsupported(err) {
			return s, msgs, err
		}
	}
	return exportSession(ctx, h, probeService(ctx, h), sid)
}

// children are a session's descendants, parents first.
func (m *Module) children(ctx context.Context, h agent.Host, in agent.Install, sid agent.SessionID) ([]agent.SessionID, error) {
	if r, ok, err := openSQL(ctx, h, in); ok {
		var kids []agent.SessionID
		if err == nil {
			kids, err = r.descendants(ctx, sid)
		}
		if !readUnsupported(err) {
			return kids, err
		}
	}
	all, err := apiSessions(ctx, h, probeService(ctx, h))
	if err != nil {
		return nil, err
	}
	byParent := map[string][]string{}
	for _, s := range all {
		if s.ParentID != "" {
			byParent[s.ParentID] = append(byParent[s.ParentID], s.ID)
		}
	}
	return family(byParent, string(sid)), nil
}

// SessionStamp is a session's settled-message count and last update, the same from the
// database and from OpenCode's export. A session that is not there is ErrNotFound.
func (m *Module) SessionStamp(ctx context.Context, h agent.Host, in agent.Install, sid agent.SessionID) (agent.SessionStamp, error) {
	if r, ok, err := openSQL(ctx, h, in); ok {
		var st agent.SessionStamp
		if err == nil {
			st, err = r.stamp(ctx, sid)
		}
		if !readUnsupported(err) {
			return st, err
		}
	}
	s, msgs, err := exportSession(ctx, h, probeService(ctx, h), sid)
	if err != nil {
		if errors.Is(err, agent.ErrNotFound) {
			return "", err
		}
		return "", fmt.Errorf("the state of the OpenCode session %s: %w", sid, err)
	}
	return stampFor(int64(len(msgs)), s.Updated), nil
}
