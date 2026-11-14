package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The module's queries. Each names only allowed tables and columns (the permission
// column of session_v2 shares its name with a denied table, so it is never named).

// sessionCols are the session_v2 columns a session is read with (time_busy is NULL in a
// database from before OpenCode 2.2.2).
func (d dbSchema) sessionCols() string {
	busy := `s.time_busy`
	if !d.busy {
		busy = `NULL AS time_busy`
	}
	return `s.id, s.project_id, s.workspace_id, s.parent_id, s.fork_session_id, s.fork_boundary, s.slug, s.directory, s.path, s.title, s.version, ` +
		`s.metadata, s.cost, s.tokens_input, s.tokens_output, s.tokens_reasoning, s.tokens_cache_read, s.tokens_cache_write, s.revert, s.agent, s.model, ` +
		`s.time_created, s.time_updated, ` + busy + `, s.time_idle, s.time_viewed, s.idle_outcome, s.time_archived, s.time_suspended, s.time_compacting`
}

// listQuery is every session with what a listing shows: its settled-message and part
// counts (the listing cache's signature) and the first and last prompts.
func (d dbSchema) listQuery() string {
	parts := `(SELECT count(*) FROM session_part p WHERE p.session_id = s.id) AS parts, `
	if !d.parts {
		parts = `0 AS parts, `
	}
	return `SELECT ` + d.sessionCols() + `, ` +
		`(SELECT count(*) FROM session_message m WHERE m.session_id = s.id AND ` + settledSQL + `) AS settled, ` + parts +
		`(SELECT coalesce(json_extract(m.data, '$.metadata.displayText'), json_extract(m.data, '$.text')) FROM session_message m WHERE m.session_id = s.id AND m.type = 'user' ORDER BY m.seq DESC LIMIT 1) AS last_prompt, ` +
		`(SELECT coalesce(json_extract(m.data, '$.metadata.displayText'), json_extract(m.data, '$.text')) FROM session_message m WHERE m.session_id = s.id AND m.type = 'user' ORDER BY m.seq LIMIT 1) AS first_prompt ` +
		`FROM session_v2 s ORDER BY s.time_updated DESC`
}

func (d dbSchema) sessionQuery() string {
	return `SELECT ` + d.sessionCols() + ` FROM session_v2 s WHERE s.id = ?`
}

const messagesQuery = `SELECT id, type, seq, data FROM session_message WHERE session_id = ? ORDER BY seq`

const partsQuery = `SELECT message_id, ordinal, type, data FROM session_part WHERE session_id = ? ORDER BY message_id, ordinal`

// The newest messages of a session and their parts, newest first.
const tailQuery = `SELECT id, type, seq, data FROM session_message WHERE session_id = ? ORDER BY seq DESC LIMIT ?`

const tailPartsQuery = `SELECT message_id, ordinal, type, data FROM session_part WHERE session_id = ? AND message_id IN ` +
	`(SELECT id FROM session_message WHERE session_id = ? ORDER BY seq DESC LIMIT ?) ORDER BY message_id, ordinal`

const stampQuery = `SELECT s.time_updated, (SELECT count(*) FROM session_message m WHERE m.session_id = s.id AND ` + settledSQL + `) AS settled ` +
	`FROM session_v2 s WHERE s.id = ?`

const familyQuery = `SELECT id, parent_id FROM session_v2 WHERE parent_id IS NOT NULL`

func (d dbSchema) liveQuery() string {
	busy := `time_busy`
	if !d.busy {
		busy = `NULL AS time_busy`
	}
	return `SELECT id, ` + busy + `, time_idle, time_suspended, time_compacting FROM session_v2`
}

const maxVersionQuery = `SELECT version FROM session_v2`

const migrationQuery = `SELECT id FROM migration ORDER BY id DESC LIMIT 1`

// catalogQuery reads the models.dev catalog OpenCode caches, and only it, from kv: the
// default source's (models-dev:v2:catalog) or a custom one's (models-dev:v2:catalog:<sha1>),
// whichever OpenCode wrote last; never their ":meta" entries.
const catalogQuery = `SELECT value FROM kv WHERE (key = 'models-dev:v2:catalog' OR key LIKE 'models-dev:v2:catalog:%') AND key NOT LIKE '%:meta' ` +
	`ORDER BY time_updated DESC LIMIT 1`

// The model and usage of the newest completed assistant message: of one session, or of
// any (for a new session).
const lastAssistantQuery = `SELECT m.seq, json_extract(m.data, '$.model') AS model, json_extract(m.data, '$.tokens') AS tokens FROM session_message m ` +
	`WHERE m.session_id = ? AND m.type = 'assistant' AND json_extract(m.data, '$.time.completed') IS NOT NULL ORDER BY m.seq DESC LIMIT 1`

const anyAssistantQuery = `SELECT json_extract(m.data, '$.model') AS model FROM session_message m WHERE m.type = 'assistant' ` +
	`AND json_extract(m.data, '$.time.completed') IS NOT NULL ORDER BY m.time_created DESC LIMIT 1`

// The newest completed compaction of a session.
const lastCompactionQuery = `SELECT max(m.seq) AS seq FROM session_message m WHERE m.session_id = ? AND m.type = 'compaction' ` +
	`AND json_extract(m.data, '$.status') = 'completed'`

const afterQuery = `SELECT sum(length(m.data) + 32) AS n FROM session_message m WHERE m.session_id = ? AND m.seq > ?`

// sqlReader reads one install's database.
type sqlReader struct {
	sq     agent.SQL
	db     string
	schema dbSchema // what the gate found (open)
}

// newSQLReader is the install's database before the schema gate (openSQL runs it).
func newSQLReader(h agent.Host, in agent.Install) (sqlReader, bool) {
	sq, ok := agent.HostSQL(h)
	if !ok {
		return sqlReader{}, false
	}
	return sqlReader{sq: sq, db: dbFile(h, in)}, true
}

// sessionFromRow reads the sessionCols of a row.
func sessionFromRow(r agent.Row) session {
	s := session{ID: r.String("id"), ProjectID: r.String("project_id"), WorkspaceID: r.String("workspace_id"), ParentID: r.String("parent_id"),
		ForkSessionID: r.String("fork_session_id"), Slug: r.String("slug"), Directory: r.String("directory"), Path: r.String("path"),
		Title: r.String("title"), HasTitle: !r.Null("title"), Version: r.String("version"), Agent: r.String("agent"), Outcome: r.String("idle_outcome"),
		Cost: r.Float("cost"), Created: r.Int("time_created"), Updated: r.Int("time_updated"), Busy: r.Int("time_busy"), Idle: r.Int("time_idle"),
		Viewed: r.Int("time_viewed"), Archived: r.Int("time_archived"), Suspended: r.Int("time_suspended"), Compacting: r.Int("time_compacting")}
	s.Tokens.Input, s.Tokens.Output, s.Tokens.Reasoning = r.Int("tokens_input"), r.Int("tokens_output"), r.Int("tokens_reasoning")
	s.Tokens.Cache.Read, s.Tokens.Cache.Write = r.Int("tokens_cache_read"), r.Int("tokens_cache_write")
	s.ForkBoundary = jsonCol(r, "fork_boundary")
	s.Model = jsonCol(r, "model")
	s.Metadata = jsonCol(r, "metadata")
	s.Revert = jsonCol(r, "revert")
	return s
}

// jsonCol is a JSON text column, nil when NULL or not JSON.
func jsonCol(r agent.Row, col string) json.RawMessage {
	v := strings.TrimSpace(r.String(col))
	if v == "" || !json.Valid([]byte(v)) {
		return nil
	}
	return json.RawMessage(v)
}

// listed is a session row of the listing with its counts.
type listed struct {
	session
	Settled     int64
	Parts       int64
	LastPrompt  string
	FirstPrompt string
}

func (r sqlReader) list(ctx context.Context) ([]listed, error) {
	rows, err := r.sq.Query(ctx, r.db, r.schema.listQuery())
	if err != nil {
		return nil, err
	}
	out := make([]listed, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		out = append(out, listed{session: sessionFromRow(row), Settled: row.Int("settled"), Parts: row.Int("parts"),
			LastPrompt: row.String("last_prompt"), FirstPrompt: row.String("first_prompt")})
	}
	return out, nil
}

// transcript is a session with its settled messages, read in one snapshot.
func (r sqlReader) transcript(ctx context.Context, sid agent.SessionID) (session, []message, error) {
	stmts := []agent.Statement{
		{Query: r.schema.sessionQuery(), Args: []any{string(sid)}},
		{Query: messagesQuery, Args: []any{string(sid)}},
	}
	if r.schema.parts {
		stmts = append(stmts, agent.Statement{Query: partsQuery, Args: []any{string(sid)}})
	}
	rs, err := r.sq.Batch(ctx, r.db, stmts)
	if err != nil {
		return session{}, nil, err
	}
	rs = append(rs, agent.Rows{})
	if len(rs[0].Rows) == 0 {
		return session{}, nil, fmt.Errorf("%w: %s", agent.ErrNotFound, sid)
	}
	msgs, err := joinParts(rs[1], rs[2])
	return sessionFromRow(rs[0].Rows[0]), msgs, err
}

// tail is a session and its newest n messages (settled ones, oldest first), and whether
// it has older ones.
func (r sqlReader) tail(ctx context.Context, sid agent.SessionID, n int) (session, []message, bool, error) {
	stmts := []agent.Statement{
		{Query: r.schema.sessionQuery(), Args: []any{string(sid)}},
		{Query: tailQuery, Args: []any{string(sid), n + 1}},
	}
	if r.schema.parts {
		stmts = append(stmts, agent.Statement{Query: tailPartsQuery, Args: []any{string(sid), string(sid), n}})
	}
	rs, err := r.sq.Batch(ctx, r.db, stmts)
	if err != nil {
		return session{}, nil, false, err
	}
	rs = append(rs, agent.Rows{})
	if len(rs[0].Rows) == 0 {
		return session{}, nil, false, fmt.Errorf("%w: %s", agent.ErrNotFound, sid)
	}
	rows := rs[1]
	earlier := len(rows.Rows) > n
	if earlier {
		rows.Rows = rows.Rows[:n]
	}
	for i, j := 0, len(rows.Rows)-1; i < j; i, j = i+1, j-1 {
		rows.Rows[i], rows.Rows[j] = rows.Rows[j], rows.Rows[i]
	}
	msgs, err := joinParts(rows, rs[2])
	return sessionFromRow(rs[0].Rows[0]), msgs, earlier, err
}

// joinParts builds the settled messages of message rows and their part rows.
func joinParts(msgRows, partRows agent.Rows) ([]message, error) {
	parts := map[string][]json.RawMessage{}
	for _, p := range partRows.Rows {
		d, err := partData(p.String("type"), p.String("data"))
		if err != nil {
			return nil, fmt.Errorf("a part of message %s: %w", p.String("message_id"), err)
		}
		parts[p.String("message_id")] = append(parts[p.String("message_id")], d)
	}
	var out []message
	for _, row := range msgRows.Rows {
		m, err := rowMessage(row.String("id"), row.String("type"), row.Int("seq"), row.String("data"), parts[row.String("id")])
		if err != nil {
			return nil, err
		}
		if settled(m) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r sqlReader) session(ctx context.Context, sid agent.SessionID) (session, error) {
	rows, err := r.sq.Query(ctx, r.db, r.schema.sessionQuery(), string(sid))
	if err != nil {
		return session{}, err
	}
	if len(rows.Rows) == 0 {
		return session{}, fmt.Errorf("%w: %s", agent.ErrNotFound, sid)
	}
	return sessionFromRow(rows.Rows[0]), nil
}

func (r sqlReader) stamp(ctx context.Context, sid agent.SessionID) (agent.SessionStamp, error) {
	rows, err := r.sq.Query(ctx, r.db, stampQuery, string(sid))
	if err != nil {
		return "", err
	}
	if len(rows.Rows) == 0 {
		return "", fmt.Errorf("%w: %s", agent.ErrNotFound, sid)
	}
	return stampFor(rows.Rows[0].Int("settled"), rows.Rows[0].Int("time_updated")), nil
}

// stampFor is a session's stamp: its settled-message count and last update.
func stampFor(settled, updated int64) agent.SessionStamp {
	return agent.SessionStamp(fmt.Sprintf("v1:%d@%d", settled, updated))
}

// descendants are a session's child sessions, theirs, and so on, parents first.
func (r sqlReader) descendants(ctx context.Context, sid agent.SessionID) ([]agent.SessionID, error) {
	rows, err := r.sq.Query(ctx, r.db, familyQuery)
	if err != nil {
		return nil, err
	}
	children := map[string][]string{}
	for _, row := range rows.Rows {
		children[row.String("parent_id")] = append(children[row.String("parent_id")], row.String("id"))
	}
	return family(children, string(sid)), nil
}

// family walks children from root, parents first, each session once.
func family(children map[string][]string, root string) []agent.SessionID {
	var out []agent.SessionID
	seen := map[string]bool{root: true}
	queue := []string{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			if !seen[c] {
				seen[c] = true
				out = append(out, agent.SessionID(c))
				queue = append(queue, c)
			}
		}
	}
	return out
}

// maxVersion is the newest OpenCode version that wrote a session in the database.
func (r sqlReader) maxVersion(ctx context.Context) (string, error) {
	rows, err := r.sq.Query(ctx, r.db, maxVersionQuery)
	if err != nil {
		return "", err
	}
	best := ""
	for _, row := range rows.Rows {
		v := row.String("version")
		if _, ok := agent.CompareVersions(v, v); ok && (best == "" || agent.VersionLess(best, v)) {
			best = v
		}
	}
	return best, nil
}

// latestMigration is the id of the database's newest migration ("" when unknown).
func (r sqlReader) latestMigration(ctx context.Context) string {
	rows, err := r.sq.Query(ctx, r.db, migrationQuery)
	if err != nil || len(rows.Rows) == 0 {
		return ""
	}
	return rows.Rows[0].String("id")
}

// readUnsupported reports a read the machine's tools cannot do (another machine's WAL
// database that no program has open): the module then asks OpenCode itself.
func readUnsupported(err error) bool {
	var se *schemaError
	return errors.Is(err, agent.ErrUnsupported) && !errors.As(err, &se)
}
