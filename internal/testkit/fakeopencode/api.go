package fakeopencode

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// api plays `opencode api METHOD path [-d body]`: it prints the answer's body on stdout
// (nothing for 204), and for an error status also "HTTP <status>" on stderr, exiting 1.
func (r *run) api(args []string) int {
	if len(args) != 2 {
		return r.fail("the OpenCode stand-in plays `opencode api` only as METHOD path")
	}
	method, target := strings.ToUpper(args[0]), args[1]
	u, err := url.Parse(target)
	if err != nil {
		return r.status(400, nil)
	}
	r.server()
	db, err := r.open()
	if err != nil {
		return r.fail("Error: %v", err)
	}
	defer db.Close()
	var body *value
	if r.data != "" {
		if body, err = parseJSON([]byte(r.data)); err != nil || !body.isObj() {
			return r.invalid("The request body is not a JSON object")
		}
	} else {
		body = obj()
	}
	path := strings.Split(strings.Trim(u.Path, "/"), "/")
	q := u.Query()
	now := r.now()
	switch {
	case method == "GET" && u.Path == "/api/info":
		return r.ok(r.infoBody())
	case method == "GET" && u.Path == "/api/plugin":
		v := obj()
		loc := obj()
		loc.set("directory", str(r.workdir()))
		v.set("location", loc)
		v.set("data", arr())
		return r.ok(v)
	case method == "GET" && u.Path == "/api/session":
		return r.sessionPage(db, q)
	case method == "GET" && u.Path == "/api/session/active":
		v := obj()
		d := obj()
		for _, id := range r.active() {
			e := obj()
			e.set("type", str("running"))
			d.set(id, e)
		}
		v.set("data", d)
		return r.ok(v)
	case len(path) >= 3 && path[0] == "api" && path[1] == "session":
		sid := path[2]
		if !strings.HasPrefix(sid, "ses") {
			return r.invalidKind("Expected a string starting with \"ses\"\n  at [\"sessionID\"]", "Params")
		}
		rest := strings.Join(path[3:], "/")
		switch {
		case method == "GET" && rest == "":
			s, err := getRow(db, sid)
			if err != nil {
				return r.storeError(sid, err)
			}
			return r.ok(data(s.info()))
		case method == "PATCH" && rest == "":
			return r.empty(sid, update(db, sid, body, now))
		case method == "DELETE" && rest == "":
			return r.empty(sid, remove(db, sid))
		case method == "POST" && rest == "move":
			d := body.get("directory")
			if d == nil {
				return r.invalidKind("Missing key\n  at [\"directory\"]", "Payload")
			} else if d.kind != 's' {
				return r.invalidKind("Expected string, got "+string(d.compact())+"\n  at [\"directory\"]", "Payload")
			}
			return r.empty(sid, move(db, sid, d.s, r.paths().home, r.env, now))
		case method == "POST" && rest == "fork":
			before := body.get("before")
			if before != nil && (before.kind != 's' || !strings.HasPrefix(before.s, "msg_")) {
				return r.invalidKind("Expected a string starting with \"msg_\"\n  at [\"before\"]", "Payload")
			}
			id, err := fork(db, sid, body.text("before"), now)
			if err != nil {
				return r.storeError(sid, err)
			}
			s, err := getRow(db, id)
			if err != nil {
				return r.storeError(id, err)
			}
			return r.ok(data(s.info()))
		case method == "POST" && rest == "interrupt":
			if _, err := getRow(db, sid); err != nil {
				return r.storeError(sid, err)
			}
			done := false
			if r.isActive(sid) {
				if err := interrupt(db, sid, now); err != nil {
					return r.storeError(sid, err)
				}
				r.setInactive(sid)
				done = true
			}
			v := obj()
			v.set("interrupted", lit(strconv.FormatBool(done)))
			return r.ok(v)
		case method == "GET" && rest == "message":
			return r.messagePage(db, sid, q)
		}
	}
	return r.status(404, nil)
}

func data(v *value) *value {
	d := obj()
	d.set("data", v)
	return d
}

func (r *run) ok(v *value) int {
	r.Stdout.Write(append(v.compact(), '\n'))
	return 0
}

// empty answers 204 No Content, or the error.
func (r *run) empty(sid string, err error) int {
	if err != nil {
		return r.storeError(sid, err)
	}
	return 0
}

var statusText = map[int]string{400: "Bad Request", 404: "Not Found", 500: "Internal Server Error"}

func (r *run) status(code int, body *value) int {
	if body != nil {
		r.Stdout.Write(append(body.compact(), '\n'))
	}
	fmt.Fprintf(r.Stderr, "HTTP %d %s\n", code, statusText[code])
	return 1
}

func (r *run) invalid(msg string) int { return r.invalidKind(msg, "") }

// invalidKind is a 400 InvalidRequestError; kind says what failed to decode (Params,
// Query, Payload) or why (empty_session).
func (r *run) invalidKind(msg, kind string) int {
	v := obj()
	v.set("_tag", str("InvalidRequestError"))
	v.set("message", str(msg))
	if kind != "" {
		v.set("kind", str(kind))
	}
	return r.status(400, v)
}

func (r *run) invalidCursor(msg string) int {
	v := obj()
	v.set("_tag", str("InvalidCursorError"))
	v.set("message", str(msg))
	return r.status(400, v)
}

func (r *run) storeError(sid string, err error) int {
	var br *badRequest
	var nm *errMessageNotFound
	switch {
	case errors.Is(err, errNotFound):
		v := obj()
		v.set("_tag", str("SessionNotFoundError"))
		v.set("sessionID", str(sid))
		v.set("message", str("Session not found: "+sid))
		return r.status(404, v)
	case errors.As(err, &nm):
		v := obj()
		v.set("_tag", str("MessageNotFoundError"))
		v.set("sessionID", str(nm.sid))
		v.set("messageID", str(nm.mid))
		v.set("message", str("Message not found: "+nm.mid))
		return r.status(404, v)
	case errors.Is(err, errForkEmpty):
		return r.invalidKind("Cannot fork empty session: "+sid, "empty_session")
	case errors.As(err, &br):
		return r.invalidKind(br.msg, br.kind)
	}
	v := obj()
	v.set("_tag", str("UnknownError"))
	v.set("message", str(err.Error()))
	return r.status(500, v)
}

// infoBody is GET /api/info: the server's version and where it listens.
func (r *run) infoBody() *value {
	v := obj()
	v.set("version", str(r.serverVersion()))
	v.set("pid", num(int64(os.Getpid())))
	v.set("hostname", str("host"))
	u := r.serviceURL()
	if r.standalone || u == "" {
		u = "http://127.0.0.1:0"
	}
	v.set("urls", &value{kind: 'a', vals: []*value{str(u)}})
	p := obj()
	p.set("tmp", str(filepath.Join(tempDir(), "opencode")))
	v.set("paths", p)
	c := obj()
	c.set("persistentPty", lit("true"))
	v.set("capabilities", c)
	return v
}

// The sessions with a turn running in the background service: $FAKE_OPENCODE_ACTIVE,
// and the ids in the state folder's fake-active file, until an interrupt ends them. A
// private server (--standalone) runs none. Their rows claim the turn (time_suspended,
// time_busy), as the service's do while it runs one; see markTurns.

func (r *run) activeFile() string { return r.paths().state + string(os.PathSeparator) + "fake-active" }

func (r *run) active() []string {
	if r.standalone {
		return nil // a private server runs no turns
	}
	return r.turns()
}

// turns are the sessions the background service runs a turn of.
func (r *run) turns() []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		for _, id := range strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == '\n' || c == ' ' }) {
			if !seen[id] && !r.wasInterrupted(id) {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	add(r.getenv("FAKE_OPENCODE_ACTIVE"))
	if b, err := os.ReadFile(r.activeFile()); err == nil {
		add(string(b))
	}
	return out
}

func (r *run) isActive(sid string) bool {
	for _, id := range r.active() {
		if id == sid {
			return true
		}
	}
	return false
}

func (r *run) interruptedFile() string { return r.activeFile() + ".interrupted" }

func (r *run) wasInterrupted(sid string) bool {
	b, err := os.ReadFile(r.interruptedFile())
	return err == nil && strings.Contains("\n"+string(b), "\n"+sid+"\n")
}

func (r *run) setInactive(sid string) {
	_ = os.MkdirAll(r.paths().state, 0o700)
	f, err := os.OpenFile(r.interruptedFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		fmt.Fprintln(f, sid)
		f.Close()
	}
}

// sessionPage is GET /api/session (Session.list): sessions by last update, newest first
// unless ?order=asc, ?limit at a time (50 by default), each page with opaque cursors to
// the pages before and after it. A cursor carries the filters it was made with.
func (r *run) sessionPage(db *sql.DB, q url.Values) int {
	filter := obj()
	for _, k := range []string{"order", "search", "parentID", "archived", "directory", "project", "subpath"} {
		if v := q.Get(k); v != "" && (k != "subpath" || q.Get("project") != "") {
			filter.set(k, str(v))
		}
	}
	if o := filter.text("order"); o != "" && o != "asc" && o != "desc" {
		return r.invalidKind(`Expected "asc" | "desc", got "`+o+`"`+"\n  at [\"order\"]", "Query")
	}
	if a := filter.text("archived"); a != "" && a != "true" && a != "false" {
		return r.invalidKind(`Expected "true" | "false", got "`+a+`"`+"\n  at [\"archived\"]", "Query")
	}
	var anchor *value
	if c := q.Get("cursor"); c != "" {
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(c, "="))
		var dec *value
		if err == nil {
			dec, err = parseJSON(b)
		}
		if err != nil || !dec.isObj() || !dec.get("anchor").isObj() {
			return r.invalidCursor("Invalid cursor")
		}
		anchor = dec.get("anchor")
		dec.del("anchor")
		filter = dec
	}
	limit := 50
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil {
			return r.invalidKind(`Expected a finite number, got "`+l+`"`+"\n  at [\"limit\"]", "Query")
		}
		if n <= 0 {
			return r.invalidKind("Expected a value greater than 0\n  at [\"limit\"]", "Query")
		}
		limit = n
	}
	direction := "next"
	if anchor != nil {
		direction = anchor.text("direction")
	}
	order := filter.text("order")
	if order == "" {
		order = "desc"
	}
	if direction == "previous" {
		order = map[string]string{"asc": "desc", "desc": "asc"}[order]
	}
	var where []string
	var args []any
	if v := filter.text("directory"); v != "" {
		where, args = append(where, "directory = ?"), append(args, v)
	}
	if v := filter.text("project"); v != "" {
		where, args = append(where, "project_id = ?"), append(args, v)
		if sp := filter.get("subpath"); sp != nil {
			where, args = append(where, "path = ?"), append(args, sp.s)
		}
	}
	if v := filter.text("search"); v != "" {
		where, args = append(where, "title LIKE ?"), append(args, "%"+v+"%")
	}
	switch v := filter.text("parentID"); v {
	case "":
	case "null":
		where = append(where, "parent_id IS NULL")
	default:
		where, args = append(where, "parent_id = ?"), append(args, v)
	}
	switch filter.text("archived") {
	case "true":
		where = append(where, "time_archived IS NOT NULL")
	case "false":
		where = append(where, "time_archived IS NULL")
	}
	if anchor != nil {
		t, _ := anchor.int("time")
		cmp := "<"
		if order == "asc" {
			cmp = ">"
		}
		where = append(where, fmt.Sprintf("(time_updated %s ? OR (time_updated = ? AND id %s ?))", cmp, cmp))
		args = append(args, t, t, anchor.text("id"))
	}
	query := "SELECT " + rowColumns + " FROM session_v2"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	dir := map[string]string{"asc": "ASC", "desc": "DESC"}[order]
	query += fmt.Sprintf(" ORDER BY time_updated %s, id %s LIMIT %d", dir, dir, limit)
	rows, err := db.Query(query, args...)
	if err != nil {
		return r.storeError("", err)
	}
	var list []row
	for rows.Next() {
		s, err := scanRow(rows)
		if err != nil {
			rows.Close()
			return r.storeError("", err)
		}
		list = append(list, s)
	}
	rows.Close()
	if direction == "previous" {
		for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
			list[i], list[j] = list[j], list[i]
		}
	}
	out := arr()
	for _, s := range list {
		out.vals = append(out.vals, s.info())
	}
	mark := func(s row, d string) *value {
		c := filter.clone()
		a := obj()
		a.set("id", str(s.id))
		a.set("time", num(s.updated))
		a.set("direction", str(d))
		c.set("anchor", a)
		return str(base64.RawURLEncoding.EncodeToString(c.compact()))
	}
	v := obj()
	v.set("data", out)
	if len(list) > 0 {
		v.set("cursor", cursors(mark(list[0], "previous"), mark(list[len(list)-1], "next")))
	} else {
		v.set("cursor", cursors(nil, nil))
	}
	return r.ok(v)
}

// cursors is a page's cursor member: null where there is no page that way.
func cursors(previous, next *value) *value {
	c := obj()
	for _, x := range []struct {
		k string
		v *value
	}{{"previous", previous}, {"next", next}} {
		if x.v == nil {
			x.v = lit("null")
		}
		c.set(x.k, x.v)
	}
	return c
}

// messagePage is GET /api/session/{id}/message (packages/server/src/handlers/message.ts):
// a session's messages as OpenCode reads them (parts merged, unsettled ones too), newest
// first unless ?order=asc, ?limit at a time (50 by default), ?type only, paged by cursor.
// A cursor keeps its order and cannot be given one; a cursor naming a message the
// session does not have reads as an empty page.
func (r *run) messagePage(db *sql.DB, sid string, q url.Values) int {
	if q.Get("cursor") != "" && q.Get("order") != "" {
		return r.invalidCursor("Cursor cannot be combined with order")
	}
	order, direction, from := q.Get("order"), "next", ""
	if c := q.Get("cursor"); c != "" {
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(c, "="))
		var dec *value
		if err == nil {
			dec, err = parseJSON(b)
		}
		if err != nil || !dec.isObj() || !strings.HasPrefix(dec.text("id"), "msg_") ||
			(dec.text("order") != "asc" && dec.text("order") != "desc") ||
			(dec.text("direction") != "previous" && dec.text("direction") != "next") {
			return r.invalidCursor("Invalid cursor")
		}
		from, order, direction = dec.text("id"), dec.text("order"), dec.text("direction")
	}
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		return r.invalidKind(`Expected "asc" | "desc", got "`+order+`"`+"\n  at [\"order\"]", "Query")
	}
	limit := 50
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n <= 0 {
			return r.invalidKind("Expected a value greater than 0\n  at [\"limit\"]", "Query")
		}
		limit = n
	}
	if _, err := getRow(db, sid); err != nil {
		return r.storeError(sid, err)
	}
	all, err := messages(db, sid)
	if err != nil {
		return r.storeError(sid, err)
	}
	// Positions in all stand for seq.
	at := -1
	if from != "" {
		for i, m := range all {
			if m.text("id") == from {
				at = i
			}
		}
	}
	eff := order
	if direction == "previous" {
		eff = map[string]string{"asc": "desc", "desc": "asc"}[order]
	}
	var msgs []*value
	if from == "" || at >= 0 {
		for i := range all {
			j := i
			if eff == "desc" {
				j = len(all) - 1 - i
			}
			m := all[j]
			switch {
			case at >= 0 && eff == "asc" && j <= at, at >= 0 && eff == "desc" && j >= at:
				continue
			case q.Get("type") != "" && m.text("type") != q.Get("type"):
				continue
			}
			msgs = append(msgs, m)
			if len(msgs) == limit {
				break
			}
		}
	}
	if direction == "previous" {
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
	}
	out := arr()
	out.vals = msgs
	mark := func(id, d string) *value {
		c := obj()
		c.set("id", str(id))
		c.set("order", str(order))
		c.set("direction", str(d))
		return str(base64.RawURLEncoding.EncodeToString(c.compact()))
	}
	v := obj()
	v.set("data", out)
	if len(msgs) > 0 {
		v.set("cursor", cursors(mark(msgs[0].text("id"), "previous"), mark(msgs[len(msgs)-1].text("id"), "next")))
	} else {
		v.set("cursor", cursors(nil, nil))
	}
	return r.ok(v)
}

func msTime(ms int64) time.Time { return time.UnixMilli(ms) }
