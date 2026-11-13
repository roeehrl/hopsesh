package fakeopencode

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The session store: what OpenCode 2.3.0's server does with session_v2, session_message
// and session_part for the commands the stand-in plays. It writes no event rows, blobs,
// instruction state or jobs.

var (
	errNotFound   = errors.New("not found")
	errExists     = errors.New("session already exists")
	errBadRequest = errors.New("bad request")
	// errNoFolder is an import into a folder that is not there.
	errNoFolder = errors.New("no such folder")
)

// badRequest is an input OpenCode's server refuses (HTTP 400), with its reason.
type badRequest struct{ msg string }

func (e *badRequest) Error() string   { return e.msg }
func (e *badRequest) Is(t error) bool { return t == errBadRequest }

// row is a session_v2 row.
type row struct {
	id, projectID, slug, directory, version          string
	workspaceID, parentID, forkSession, forkBoundary sql.NullString
	path, title, metadata, permission, revert        sql.NullString
	agent, model, outcome                            sql.NullString
	cost                                             float64
	input, output, reasoning, cacheRead, cacheWrite  int64
	created, updated                                 int64
	busy, idle, viewed, archived, suspended          sql.NullInt64
}

const rowColumns = `id, project_id, slug, directory, version, workspace_id, parent_id, fork_session_id, fork_boundary,
	path, title, metadata, permission, revert, agent, model, idle_outcome, cost, tokens_input, tokens_output,
	tokens_reasoning, tokens_cache_read, tokens_cache_write, time_created, time_updated, time_busy, time_idle,
	time_viewed, time_archived, time_suspended`

type scanner interface{ Scan(...any) error }

func scanRow(s scanner) (row, error) {
	var r row
	err := s.Scan(&r.id, &r.projectID, &r.slug, &r.directory, &r.version, &r.workspaceID, &r.parentID, &r.forkSession,
		&r.forkBoundary, &r.path, &r.title, &r.metadata, &r.permission, &r.revert, &r.agent, &r.model, &r.outcome,
		&r.cost, &r.input, &r.output, &r.reasoning, &r.cacheRead, &r.cacheWrite, &r.created, &r.updated, &r.busy,
		&r.idle, &r.viewed, &r.archived, &r.suspended)
	return r, err
}

type querier interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

func getRow(q querier, id string) (row, error) {
	r, err := scanRow(q.QueryRow("SELECT "+rowColumns+" FROM session_v2 WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, errNotFound
	}
	return r, err
}

// jsonColumn is a column holding JSON, as a value (nil when it is NULL or not JSON).
func jsonColumn(s sql.NullString) *value {
	if !s.Valid {
		return nil
	}
	v, err := parseJSON([]byte(s.String))
	if err != nil {
		return nil
	}
	return v
}

// info is a session's Session.Info, as OpenCode prints it (packages/core/src/session/info.ts).
func (r row) info() *value {
	v := obj()
	v.set("id", str(r.id))
	if r.parentID.Valid {
		v.set("parentID", str(r.parentID.String))
	}
	if b := jsonColumn(r.forkBoundary); r.forkSession.Valid && b != nil {
		b.reorder("type", "messageID")
		f := obj()
		f.set("sessionID", str(r.forkSession.String))
		f.set("boundary", b)
		v.set("fork", f)
	}
	v.set("projectID", str(r.projectID))
	if r.agent.Valid {
		v.set("agent", str(r.agent.String))
	}
	if m := jsonColumn(r.model); m.isObj() {
		if m.get("variant") == nil {
			m.set("variant", str("default"))
		}
		orderModel(m)
		v.set("model", m)
	}
	v.set("cost", lit(jsNumber(r.cost)))
	t := obj()
	t.set("input", num(r.input))
	t.set("output", num(r.output))
	t.set("reasoning", num(r.reasoning))
	c := obj()
	c.set("read", num(r.cacheRead))
	c.set("write", num(r.cacheWrite))
	t.set("cache", c)
	v.set("tokens", t)
	if r.outcome.Valid {
		v.set("outcome", str(r.outcome.String))
	}
	tm := obj()
	tm.set("created", num(r.created))
	tm.set("updated", num(r.updated))
	for _, x := range []struct {
		k string
		v sql.NullInt64
	}{{"busy", r.busy}, {"idle", r.idle}, {"viewed", r.viewed}, {"archived", r.archived}} {
		if x.v.Valid {
			tm.set(x.k, num(x.v.Int64))
		}
	}
	v.set("time", tm)
	if r.title.Valid {
		v.set("title", str(r.title.String))
	}
	loc := obj()
	loc.set("directory", str(r.directory))
	if r.workspaceID.Valid {
		loc.set("workspaceID", str(r.workspaceID.String))
	}
	v.set("location", loc)
	if r.path.Valid && r.path.String != "" {
		v.set("subpath", str(r.path.String))
	}
	if m := jsonColumn(r.metadata); m != nil {
		v.set("metadata", m)
	}
	if p := jsonColumn(r.permission); p != nil {
		v.set("permissions", p)
	}
	if rv := jsonColumn(r.revert); rv != nil {
		v.set("revert", rv)
	}
	return v
}

// messages are a session's messages in order, as OpenCode reads them: an assistant
// message's session_part rows first (by ordinal), then its inline content.
func messages(q querier, sid string) ([]*value, error) {
	rows, err := q.Query("SELECT id, type, data FROM session_message WHERE session_id = ? ORDER BY seq", sid)
	if err != nil {
		return nil, err
	}
	type stored struct{ id, typ, data string }
	var all []stored
	for rows.Next() {
		var s stored
		if err := rows.Scan(&s.id, &s.typ, &s.data); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	parts := map[string][]*value{}
	prow, err := q.Query("SELECT message_id, data FROM session_part WHERE session_id = ? ORDER BY message_id, ordinal", sid)
	if err != nil {
		return nil, err
	}
	for prow.Next() {
		var mid, data string
		if err := prow.Scan(&mid, &data); err != nil {
			prow.Close()
			return nil, err
		}
		v, err := parseJSON([]byte(data))
		if err != nil {
			prow.Close()
			return nil, fmt.Errorf("part of %s: %w", mid, err)
		}
		parts[mid] = append(parts[mid], v)
	}
	prow.Close()
	var out []*value
	for _, s := range all {
		data, err := parseJSON([]byte(s.data))
		if err != nil || !data.isObj() {
			return nil, fmt.Errorf("message %s does not decode", s.id)
		}
		m := obj()
		m.set("id", str(s.id))
		for i, k := range data.keys {
			m.set(k, data.vals[i])
		}
		m.set("type", str(s.typ))
		if p := parts[s.id]; len(p) > 0 && s.typ == "assistant" {
			content := arr()
			content.vals = append(content.vals, p...)
			if inline := m.get("content"); inline != nil && inline.kind == 'a' {
				content.vals = append(content.vals, inline.vals...)
			}
			m.set("content", content)
		}
		orderMessage(m)
		out = append(out, m)
	}
	return out, nil
}

// export is `session export`: the session's info and its settled messages.
func export(q querier, sid string) (*value, error) {
	r, err := getRow(q, sid)
	if err != nil {
		return nil, err
	}
	msgs, err := messages(q, sid)
	if err != nil {
		return nil, err
	}
	list := arr()
	for _, m := range msgs {
		if settled(m) {
			list.vals = append(list.vals, m)
		}
	}
	doc := obj()
	doc.set("info", r.info())
	doc.set("messages", list)
	return doc, nil
}

// Export is `opencode session export <id>` on the database at file: the session as
// OpenCode 2.3.0 prints it.
func Export(file, sid string) ([]byte, error) {
	db, err := Open(file)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	doc, err := export(db, sid)
	if err != nil {
		return nil, err
	}
	return append(doc.pretty(), '\n'), nil
}

// Import imports a transfer (OpenCode's export: {info, messages}) into the database at
// file, in dir, as `opencode session import <file> --directory <dir>` does with OpenCode
// 2.3.0, and returns the session's id. demoseed and the test kit use it.
func Import(file string, transfer []byte, dir string, now time.Time) (string, error) {
	db, err := Open(file)
	if err != nil {
		return "", err
	}
	defer db.Close()
	doc, err := parseJSON(transfer)
	if err != nil {
		return "", err
	}
	return importDoc(db, doc, dir, now, Version)
}

// importDoc is SessionTransfer.import (packages/core/src/session/transfer.ts): the id is
// kept, the project, folder, slug and version come from here, settled messages are
// numbered from 1, and it all happens in one transaction.
func importDoc(db *sql.DB, doc *value, dir string, now time.Time, version string) (string, error) {
	info, msgs, err := checkTransfer(doc)
	if err != nil {
		return "", err
	}
	sid := info.text("id")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", errNoFolder
	}
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := getRow(tx, sid); err == nil {
		return "", errExists
	} else if !errors.Is(err, errNotFound) {
		return "", err
	}
	parent := info.text("parentID")
	if parent != "" {
		if _, err := getRow(tx, parent); err != nil {
			return "", err
		}
	}
	p := resolveProject(dir)
	ms := now.UnixMilli()
	if err := upsertProject(tx, p, ms); err != nil {
		return "", err
	}
	sub := subpath(p.worktree, dir)
	jsonOf := func(k string) any {
		if x := info.get(k); x != nil {
			return string(x.compact())
		}
		return nil
	}
	nullable := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	tok := info.get("tokens")
	tin, _ := tok.int("input")
	tout, _ := tok.int("output")
	treason, _ := tok.int("reasoning")
	cread, _ := tok.get("cache").int("read")
	cwrite, _ := tok.get("cache").int("write")
	cost, _ := info.number("cost")
	t := info.get("time")
	created, _ := t.int("created")
	var busy, idle, viewed, archived, outcome any
	if i, ok := t.int("idle"); ok {
		idle = i
		if b, ok := t.int("busy"); ok {
			busy = min(i, b)
		}
		if v, ok := t.int("viewed"); ok {
			viewed = min(i, v)
		}
		outcome = nullable(info.text("outcome"))
	}
	if a, ok := t.int("archived"); ok {
		archived = a
	}
	var model any
	if m := info.get("model"); m.isObj() {
		model = string(m.compact())
	}
	_, err = tx.Exec(`INSERT INTO session_v2 (id, project_id, parent_id, slug, directory, path, title, version, metadata,
		permission, agent, model, cost, tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write,
		time_created, time_updated, time_busy, time_idle, time_viewed, idle_outcome, time_archived)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sid, p.id, nullable(parent), slug(), dir, sub, nullable(info.text("title")), version, jsonOf("metadata"),
		jsonOf("permissions"), nullable(info.text("agent")), model, cost, tin, tout, treason, cread, cwrite,
		created, ms, busy, idle, viewed, outcome, archived)
	if err != nil {
		return "", err
	}
	seq := 0
	for _, m := range msgs {
		if !settled(m) {
			continue
		}
		seq++
		if err := insertMessage(tx, sid, seq, m); err != nil {
			// A message id is a key of the whole database: OpenCode fails the import
			// as an internal error.
			return "", fmt.Errorf("message %s: %w", m.text("id"), err)
		}
	}
	if _, err := tx.Exec("INSERT INTO event_sequence (aggregate_id, seq) VALUES (?, ?)", sid, seq+1); err != nil {
		return "", err
	}
	return sid, tx.Commit()
}

// insertMessage stores a message as OpenCode does: its schema-encoded data without its id
// and type, which are columns.
func insertMessage(tx *sql.Tx, sid string, seq int, m *value) error {
	m = m.clone()
	orderMessage(m)
	id, typ := m.text("id"), m.text("type")
	created, _ := m.get("time").int("created")
	m.del("id")
	m.del("type")
	_, err := tx.Exec("INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?, ?)",
		id, sid, typ, seq, created, created, string(m.compact()))
	return err
}

func subpath(worktree, dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	rel, err := filepath.Rel(filepath.FromSlash(worktree), abs)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// checkTransfer checks a transfer as OpenCode's schema does what hopsesh relies on: the
// info's required members and each message's id, type and creation time.
func checkTransfer(doc *value) (*value, []*value, error) {
	bad := func(f string, a ...any) error { return &badRequest{fmt.Sprintf(f, a...)} }
	info := doc.get("info")
	if !info.isObj() {
		return nil, nil, bad("info is missing")
	}
	if !strings.HasPrefix(info.text("id"), "ses") {
		return nil, nil, bad("info.id is not a session id")
	}
	if info.get("projectID") == nil || info.get("projectID").kind != 's' {
		return nil, nil, bad("info.projectID is missing")
	}
	if _, ok := info.number("cost"); !ok {
		return nil, nil, bad("info.cost is missing")
	}
	tok := info.get("tokens")
	for _, k := range []string{"input", "output", "reasoning"} {
		if _, ok := tok.number(k); !ok {
			return nil, nil, bad("info.tokens.%s is missing", k)
		}
	}
	for _, k := range []string{"read", "write"} {
		if _, ok := tok.get("cache").number(k); !ok {
			return nil, nil, bad("info.tokens.cache.%s is missing", k)
		}
	}
	for _, k := range []string{"created", "updated"} {
		if _, ok := info.get("time").int(k); !ok {
			return nil, nil, bad("info.time.%s is missing", k)
		}
	}
	if info.get("location").text("directory") == "" {
		return nil, nil, bad("info.location.directory is missing")
	}
	if m := info.get("model"); m != nil && (m.text("id") == "" || m.text("providerID") == "") {
		return nil, nil, bad("info.model is not a model")
	}
	list := doc.get("messages")
	if list == nil || list.kind != 'a' {
		return nil, nil, bad("messages is missing")
	}
	for i, m := range list.vals {
		switch {
		case !m.isObj():
			return nil, nil, bad("messages[%d] is not an object", i)
		case !strings.HasPrefix(m.text("id"), "msg_"):
			return nil, nil, bad("messages[%d].id is not a message id", i)
		case !known(m.text("type")):
			return nil, nil, bad("messages[%d].type %q is not a message type", i, m.text("type"))
		}
		if _, ok := m.get("time").int("created"); !ok {
			return nil, nil, bad("messages[%d].time.created is missing", i)
		}
	}
	return info, list.vals, nil
}

// descendants are a session's child sessions, theirs, and so on.
func descendants(q querier, sid string) ([]string, error) {
	var out []string
	queue := []string{sid}
	for len(queue) > 0 {
		rows, err := q.Query("SELECT id FROM session_v2 WHERE parent_id = ? ORDER BY id", queue[0])
		if err != nil {
			return nil, err
		}
		queue = queue[1:]
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, id)
			queue = append(queue, id)
		}
		rows.Close()
	}
	return out, nil
}

// remove deletes a session and its child sessions.
func remove(db *sql.DB, sid string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := getRow(tx, sid); err != nil {
		return err
	}
	kids, err := descendants(tx, sid)
	if err != nil {
		return err
	}
	for _, id := range append([]string{sid}, kids...) {
		for _, q := range []string{
			"DELETE FROM session_part WHERE session_id = ?", "DELETE FROM session_message WHERE session_id = ?",
			"DELETE FROM event_sequence WHERE aggregate_id = ?", "DELETE FROM session_v2 WHERE id = ?",
		} {
			if _, err := tx.Exec(q, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// update sets a session's title or metadata (PATCH /api/session/{id}).
func update(db *sql.DB, sid string, body *value, now time.Time) error {
	if _, err := getRow(db, sid); err != nil {
		return err
	}
	if t := body.get("title"); t != nil {
		var title any
		if t.kind == 's' {
			title = t.s
		} else if t.s != "null" {
			return &badRequest{"title is not a string"}
		}
		if _, err := db.Exec("UPDATE session_v2 SET title = ?, time_updated = ? WHERE id = ?", title, now.UnixMilli(), sid); err != nil {
			return err
		}
	}
	if m := body.get("metadata"); m != nil {
		var meta any
		if m.isObj() {
			meta = string(m.compact())
		}
		if _, err := db.Exec("UPDATE session_v2 SET metadata = ?, time_updated = ? WHERE id = ?", meta, now.UnixMilli(), sid); err != nil {
			return err
		}
	}
	return nil
}

// appendMessage adds a message at the end of a session.
func appendMessage(tx *sql.Tx, sid string, m *value) error {
	var seq int
	if err := tx.QueryRow("SELECT coalesce(max(seq), 0) FROM session_message WHERE session_id = ?", sid).Scan(&seq); err != nil {
		return err
	}
	return insertMessage(tx, sid, seq+1, m)
}

// move changes a session's folder (POST /api/session/{id}/move). OpenCode queues the
// move and applies it at the next step boundary; the stand-in applies it at once, with
// the location-switched message OpenCode writes then.
func move(db *sql.DB, sid, dir string, now time.Time) error {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return &badRequest{"Directory does not exist: " + dir}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := getRow(tx, sid)
	if err != nil {
		return err
	}
	p := resolveProject(dir)
	ms := now.UnixMilli()
	if err := upsertProject(tx, p, ms); err != nil {
		return err
	}
	sub := subpath(p.worktree, dir)
	m := obj()
	m.set("id", str(newID("msg_", false, now)))
	t := obj()
	t.set("created", num(ms))
	m.set("time", t)
	m.set("type", str("location-switched"))
	loc := obj()
	loc.set("directory", str(dir))
	m.set("location", loc)
	m.set("projectID", str(p.id))
	m.set("subpath", str(sub))
	prev := obj()
	ploc := obj()
	ploc.set("directory", str(r.directory))
	prev.set("location", ploc)
	prev.set("projectID", str(r.projectID))
	prev.set("subpath", str(r.path.String))
	m.set("previous", prev)
	if err := appendMessage(tx, sid, m); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE session_v2 SET directory = ?, project_id = ?, path = ?, time_updated = ? WHERE id = ?", dir, p.id, sub, ms, sid); err != nil {
		return err
	}
	return tx.Commit()
}

// fork copies a session (POST /api/session/{id}/fork): its messages before the message
// `before`, else all of them, under new ids (<a new message id>_<n>), titled
// "<title> (fork #<n>)".
func fork(db *sql.DB, sid, before string, now time.Time) (string, error) {
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	r, err := getRow(tx, sid)
	if err != nil {
		return "", err
	}
	msgs, err := messages(tx, sid)
	if err != nil {
		return "", err
	}
	var settledMsgs []*value
	for _, m := range msgs {
		if settled(m) {
			settledMsgs = append(settledMsgs, m)
		}
	}
	boundary := obj()
	switch {
	case before != "":
		i := indexOf(settledMsgs, before)
		if i < 0 {
			return "", &badRequest{"Message not found: " + before}
		}
		settledMsgs = settledMsgs[:i]
		boundary.set("type", str("before"))
		boundary.set("messageID", str(before))
	case len(settledMsgs) > 0:
		boundary.set("type", str("through"))
		boundary.set("messageID", str(settledMsgs[len(settledMsgs)-1].text("id")))
	default:
		return "", &badRequest{"Session has no messages to fork"}
	}
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM session_v2 WHERE fork_session_id = ?", sid).Scan(&n); err != nil {
		return "", err
	}
	title := r.title.String
	if title == "" {
		title = "Untitled"
	}
	id := newID("ses_", true, now)
	ms := now.UnixMilli()
	_, err = tx.Exec(`INSERT INTO session_v2 (id, project_id, workspace_id, fork_session_id, fork_boundary, slug, directory, path, title,
		version, agent, model, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, r.projectID, r.workspaceID, sid, string(boundary.compact()), slug(), r.directory, r.path, title+" (fork #"+strconv.Itoa(n+1)+")",
		r.version, r.agent, r.model, ms, ms)
	if err != nil {
		return "", err
	}
	base := newID("msg_", false, now)
	for i, m := range settledMsgs {
		c := m.clone()
		c.set("id", str(base+"_"+strconv.Itoa(i+1)))
		if err := insertMessage(tx, id, i+1, c); err != nil {
			return "", err
		}
	}
	if _, err := tx.Exec("INSERT INTO event_sequence (aggregate_id, seq) VALUES (?, ?)", id, len(settledMsgs)+1); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func indexOf(msgs []*value, id string) int {
	for i, m := range msgs {
		if m.text("id") == id {
			return i
		}
	}
	return -1
}

// interrupt ends a running turn: the stand-in's turns are the sessions it lists as
// active; ending one records the idle message OpenCode writes for an interrupted turn.
func interrupt(db *sql.DB, sid string, now time.Time) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := getRow(tx, sid); err != nil {
		return err
	}
	m := obj()
	m.set("id", str(newID("msg_", false, now)))
	t := obj()
	t.set("created", num(now.UnixMilli()))
	m.set("time", t)
	m.set("type", str("idle"))
	m.set("outcome", str("interrupted"))
	if err := appendMessage(tx, sid, m); err != nil {
		return err
	}
	ms := now.UnixMilli()
	if _, err := tx.Exec("UPDATE session_v2 SET time_suspended = NULL, time_idle = ?, idle_outcome = 'interrupted', time_updated = ? WHERE id = ?", ms, ms, sid); err != nil {
		return err
	}
	return tx.Commit()
}
