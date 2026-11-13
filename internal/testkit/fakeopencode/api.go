package fakeopencode

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
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
			return r.invalid("Invalid session id: " + sid)
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
			dir := body.text("directory")
			if dir == "" {
				return r.invalid("directory is required")
			}
			return r.empty(sid, move(db, sid, dir, now))
		case method == "POST" && rest == "fork":
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

func (r *run) invalid(msg string) int {
	v := obj()
	v.set("_tag", str("InvalidRequestError"))
	v.set("message", str(msg))
	return r.status(400, v)
}

func (r *run) storeError(sid string, err error) int {
	var br *badRequest
	switch {
	case errors.Is(err, errNotFound):
		v := obj()
		v.set("_tag", str("SessionNotFoundError"))
		v.set("sessionID", str(sid))
		v.set("message", str("Session not found: "+sid))
		return r.status(404, v)
	case errors.As(err, &br):
		return r.invalid(br.msg)
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
	p.set("tmp", str(strings.TrimRight(os.TempDir(), "/\\")+string(os.PathSeparator)+"opencode"))
	v.set("paths", p)
	c := obj()
	c.set("persistentPty", lit("true"))
	v.set("capabilities", c)
	return v
}

// The sessions with a turn running: $FAKE_OPENCODE_ACTIVE, and the ids in the state
// folder's fake-active file (which an interrupt takes them out of).

func (r *run) activeFile() string { return r.paths().state + string(os.PathSeparator) + "fake-active" }

func (r *run) active() []string {
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
// the pages before and after it.
func (r *run) sessionPage(db *sql.DB, q url.Values) int {
	filter := obj()
	for _, k := range []string{"order", "search", "parentID", "archived", "directory"} {
		if v := q.Get(k); v != "" {
			filter.set(k, str(v))
		}
	}
	var anchor *value
	if c := q.Get("cursor"); c != "" {
		b, err := base64.RawURLEncoding.DecodeString(c)
		var dec *value
		if err == nil {
			dec, err = parseJSON(b)
		}
		if err != nil || !dec.isObj() || !dec.get("anchor").isObj() {
			return r.invalid("Invalid cursor")
		}
		anchor = dec.get("anchor")
		dec.del("anchor")
		filter = dec
	}
	limit := 50
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n <= 0 {
			return r.invalid("Invalid limit")
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
	cursor := obj()
	mark := func(s row, d string) *value {
		c := filter.clone()
		a := obj()
		a.set("id", str(s.id))
		a.set("time", num(s.updated))
		a.set("direction", str(d))
		c.set("anchor", a)
		return str(base64.RawURLEncoding.EncodeToString(c.compact()))
	}
	if len(list) > 0 {
		cursor.set("previous", mark(list[0], "previous"))
		cursor.set("next", mark(list[len(list)-1], "next"))
	}
	v := obj()
	v.set("data", out)
	v.set("cursor", cursor)
	return r.ok(v)
}

// messagePage is GET /api/session/{id}/message: a session's messages as OpenCode reads
// them (parts merged, unsettled ones too), newest first unless ?order=asc, paged by
// cursor.
func (r *run) messagePage(db *sql.DB, sid string, q url.Values) int {
	if _, err := getRow(db, sid); err != nil {
		return r.storeError(sid, err)
	}
	msgs, err := messages(db, sid)
	if err != nil {
		return r.storeError(sid, err)
	}
	order, direction, from := q.Get("order"), "next", ""
	if c := q.Get("cursor"); c != "" {
		b, err := base64.RawURLEncoding.DecodeString(c)
		var dec *value
		if err == nil {
			dec, err = parseJSON(b)
		}
		if err != nil || !dec.isObj() {
			return r.invalid("Invalid cursor")
		}
		from, order, direction = dec.text("id"), dec.text("order"), dec.text("direction")
	}
	if order == "" {
		order = "desc"
	}
	if order == "desc" {
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
	}
	if from != "" {
		i := indexOf(msgs, from)
		if i < 0 {
			return r.invalid("Invalid cursor")
		}
		if direction == "previous" {
			msgs = msgs[:i]
		} else {
			msgs = msgs[i+1:]
		}
	}
	limit := len(msgs)
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n <= 0 {
			return r.invalid("Invalid limit")
		}
		limit = n
	}
	if len(msgs) > limit {
		if direction == "previous" {
			msgs = msgs[len(msgs)-limit:]
		} else {
			msgs = msgs[:limit]
		}
	}
	out := arr()
	out.vals = msgs
	cursor := obj()
	mark := func(id, d string) *value {
		c := obj()
		c.set("id", str(id))
		c.set("order", str(order))
		c.set("direction", str(d))
		return str(base64.RawURLEncoding.EncodeToString(c.compact()))
	}
	if len(msgs) > 0 {
		cursor.set("previous", mark(msgs[0].text("id"), "previous"))
		cursor.set("next", mark(msgs[len(msgs)-1].text("id"), "next"))
	}
	v := obj()
	v.set("data", out)
	v.set("cursor", cursor)
	return r.ok(v)
}

func msTime(ms int64) time.Time { return time.UnixMilli(ms) }
