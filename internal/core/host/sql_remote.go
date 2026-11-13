package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Remote SQL tiers: how a machine reached over SSH reads a SQLite database, chosen once
// per machine (the first time a module asks) and kept.
const (
	tierSQLite3 = "sqlite3" // the sqlite3 shell, 3.37 or newer (-safe)
	tierPython3 = "python3" // python3 -I with a fixed script, with SQLite's authorizer
	tierNone    = "none"    // neither: the module falls back to its agent's own program
)

// sqlProbeScript says which of the tools a remote read can use are there, and their
// versions.
const sqlProbeScript = `if command -v sqlite3 >/dev/null 2>&1; then printf 'sqlite3 %s\n' "$(sqlite3 -version 2>/dev/null)"; fi
if command -v python3 >/dev/null 2>&1; then python3 -I -c 'import sqlite3; print("python3 " + sqlite3.sqlite_version)' 2>/dev/null; fi
exit 0`

// capScript runs the rest of its arguments with at most $1 bytes of their output passed
// on, and exits with their exit code (141 when cut short).
const capScript = `lim=$1; shift
exec 4>&1
code=$({ { "$@"; echo $? >&5; } | head -c "$lim" >&4; } 5>&1)
exit "${code:-1}"`

// SQL returns the machine's database reader, or nil when the machine has none: a
// snapshot, Windows over SSH, or a machine with neither sqlite3 3.37+ nor python3.
// agent.Confine limits it to a module's tables (Only); on another machine only a limited
// reader reads.
func (m *Machine) SQL() agent.SQL {
	switch {
	case m.snap != nil:
		return nil // a peer's snapshot holds files only; modules read them (their file mode)
	case m.Local:
		return localSQL{}
	case m.Facts.OS == "windows":
		return nil
	}
	if m.sqlTierOf() == tierNone {
		return nil
	}
	return remoteSQL{m: m}
}

// sqlTierOf probes the machine for a remote tier the first time it is asked.
func (m *Machine) sqlTierOf() string {
	m.sqlMu.Lock()
	defer m.sqlMu.Unlock()
	if m.sqlTier != "" {
		return m.sqlTier
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := m.sqlRunner().Run(ctx, []string{"sh", "-c", sqlProbeScript}, agent.RunOptions{})
	if err != nil {
		if m.Log != nil {
			m.Log.Warn("probing SQL tools", "machine", m.Name, "err", err)
		}
		return tierNone // not kept: the next reader asks again
	}
	m.sqlTier = pickSQLTier(string(res.Stdout))
	return m.sqlTier
}

// sqlRunner runs the remote readers' programs: the machine's own Exec, or a stand-in.
func (m *Machine) sqlRunner() agent.Exec {
	if m.sqlExec != nil {
		return m.sqlExec
	}
	return m.Exec()
}

// pickSQLTier reads sqlProbeScript's output: the sqlite3 shell when it has -safe and
// -json, else python3, else none.
func pickSQLTier(out string) string {
	python := false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "sqlite3":
			if versionAtLeast(f[1], 3, 37) {
				return tierSQLite3
			}
		case "python3":
			python = true
		}
	}
	if python {
		return tierPython3
	}
	return tierNone
}

func versionAtLeast(v string, major, minor int) bool {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false
	}
	a, err1 := strconv.Atoi(parts[0])
	b, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return a > major || a == major && b >= minor
}

// remoteSQL reads a database on a machine reached over SSH with the tier it has. Each
// query's arguments are put into its text as literals (bindSQL), and the final text is
// checked again before it leaves; the sqlite3 shell opens the database read only and
// safe, the python3 script also with SQLite's authorizer. Output is cut at a cap before
// it is decoded.
type remoteSQL struct {
	m           *Machine
	limited     bool
	allow, deny []string
}

func (s remoteSQL) Only(allow, deny []string) agent.SQL {
	return remoteSQL{m: s.m, limited: true, allow: allow, deny: deny}
}

func (s remoteSQL) Query(ctx context.Context, db, q string, args ...any) (agent.Rows, error) {
	out, err := s.Batch(ctx, db, []agent.Statement{{Query: q, Args: args}})
	if err != nil {
		return agent.Rows{}, err
	}
	return out[0], nil
}

func (s remoteSQL) Batch(ctx context.Context, db string, stmts []agent.Statement) ([]agent.Rows, error) {
	if !strings.HasPrefix(db, "/") {
		return nil, fmt.Errorf("%w: %q is not a database path", agent.ErrDenied, db)
	}
	texts := make([]string, len(stmts))
	for i, st := range stmts {
		q, err := bindSQL(strings.TrimSpace(st.Query), st.Args)
		if err != nil {
			return nil, err
		}
		if err := s.check(q); err != nil {
			return nil, err
		}
		texts[i] = strings.TrimSuffix(strings.TrimSpace(q), ";")
	}
	var out []agent.Rows
	err := s.read(ctx, db, func(uri string) error {
		var err error
		switch s.m.sqlTierOf() {
		case tierSQLite3:
			out, err = s.sqlite3(ctx, db, uri, stmts, texts)
		case tierPython3:
			out, err = s.python3(ctx, db, uri, stmts, texts)
		default:
			err = fmt.Errorf("%w: %s has neither sqlite3 nor python3 to read the database", agent.ErrUnsupported, s.m.Name)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	budget := agent.NewSQLBudget()
	for i, r := range out {
		if err := budget.CheckRows(r, stmts[i].Limits); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// check is the text check every query passes before it is sent: one SELECT; once
// limited, only the module's tables and never a denied one.
func (s remoteSQL) check(q string) error {
	if !s.limited {
		// The sqlite3 shell has no authorizer: a remote read always names its tables.
		return fmt.Errorf("%w: a read on another machine names the tables it reads", agent.ErrDenied)
	}
	return agent.CheckSQLQuery(q, s.allow, s.deny)
}

func (s remoteSQL) Columns(ctx context.Context, db, table string) ([]string, error) {
	if s.limited && (!containsFold(s.allow, table) || containsFold(s.deny, table)) {
		return nil, fmt.Errorf("%w: the table %s is not read", agent.ErrDenied, table)
	}
	if !strings.HasPrefix(db, "/") {
		return nil, fmt.Errorf("%w: %q is not a database path", agent.ErrDenied, db)
	}
	lit, err := sqlLiteral(table)
	if err != nil {
		return nil, err
	}
	texts := []string{"SELECT name FROM pragma_table_info(" + lit + ") ORDER BY cid"}
	stmts := []agent.Statement{{}}
	var out []agent.Rows
	err = s.read(ctx, db, func(uri string) error {
		var err error
		switch s.m.sqlTierOf() {
		case tierSQLite3:
			out, err = s.sqlite3(ctx, db, uri, stmts, texts)
		case tierPython3:
			// pragma_table_info is SQLite's own: read past the module's table list.
			out, err = remoteSQL{m: s.m}.python3(ctx, db, uri, stmts, texts)
		default:
			err = fmt.Errorf("%w: %s has neither sqlite3 nor python3 to read the database", agent.ErrUnsupported, s.m.Name)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	var cols []string
	for _, r := range out[0].Rows {
		cols = append(cols, r.String("name"))
	}
	return cols, nil
}

// read runs a read with the database's URI, again while it is busy, and as immutable
// while no program has it open (whenClosed), as the local reader does. The machine's
// files are checked over SFTP.
func (s remoteSQL) read(ctx context.Context, db string, run func(uri string) error) error {
	stat := func(p string) (fileState, error) {
		fsys, err := s.m.FS(ctx)
		if err != nil {
			return fileState{}, err
		}
		fi, err := fsys.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return fileState{missing: true}, nil
		}
		if err != nil {
			return fileState{}, err
		}
		return fileState{size: fi.Size(), mod: fi.ModTime()}, nil
	}
	if s.m.Conn == nil && s.m.fs == nil {
		stat = func(string) (fileState, error) { return fileState{}, errors.New("no filesystem") }
	}
	uri := "file:" + uriPath(db) + "?mode=ro"
	err := retryBusy(ctx, func() error {
		return whenClosed(db, stat, nil, func(immutable bool) error {
			if immutable {
				return run(uri + "&immutable=1")
			}
			return run(uri)
		})
	})
	if errors.Is(err, errCantOpen) {
		if st, e := stat(db); e == nil && st.missing {
			return fmt.Errorf("%w: no database at %s", agent.ErrNotFound, db)
		}
	}
	return err
}

// outputCap is how many bytes of a batch's output are read before it is refused.
func outputCap(stmts []agent.Statement) int64 {
	var n int64
	for _, st := range stmts {
		n += st.Limits.WithDefaults().MaxBytes
	}
	// The encoding repeats column names per row: leave room, within reason.
	return min(2*n+1<<20, 2*agent.DefaultSQLBatchMaxBytes)
}

// sqlSeparator ends each statement's rows in the sqlite3 shell's output, which prints
// nothing for a statement without rows.
const sqlSeparator = `SELECT 'hopsesh' AS "~hopsesh-end~"`

func (s remoteSQL) sqlite3(ctx context.Context, db, uri string, stmts []agent.Statement, texts []string) ([]agent.Rows, error) {
	var script strings.Builder
	script.WriteString("BEGIN;\n")
	if s.limited {
		// The shell has no authorizer to see through a view: the views come first, and a
		// read is refused when the module's tables include one.
		script.WriteString(sqlViews + ";\n" + sqlSeparator + ";\n")
	}
	for _, q := range texts {
		script.WriteString(q + ";\n" + sqlSeparator + ";\n")
	}
	script.WriteString("COMMIT;\n")
	limit := outputCap(stmts)
	argv := []string{"sh", "-c", capScript, "sh", strconv.FormatInt(limit+1, 10),
		"sqlite3", "-readonly", "-safe", "-bail", "-init", "/dev/null", "-json",
		"-cmd", ".timeout 2000", "-cmd", ".limit length " + strconv.FormatInt(min(longestValue(stmts), 1<<30), 10),
		uri, script.String()}
	res, err := s.m.sqlRunner().Run(ctx, argv, agent.RunOptions{})
	if err != nil {
		return nil, err
	}
	if int64(len(res.Stdout)) > limit {
		return nil, fmt.Errorf("%w: the output is over %d bytes", agent.ErrTooLarge, limit)
	}
	if res.Code != 0 {
		return nil, remoteSQLError(db, string(res.Stderr), res.Code)
	}
	n := len(texts)
	if s.limited {
		n++
	}
	out, err := parseShellJSON(res.Stdout, n)
	if err != nil || !s.limited {
		return out, err
	}
	for _, v := range out[0].Rows {
		if containsFold(s.allow, v.String("name")) {
			return nil, fmt.Errorf("%w: %s is a view, read only where SQLite's authorizer checks it", agent.ErrDenied, v.String("name"))
		}
	}
	return out[1:], nil
}

// sqlViews lists the database's views.
const sqlViews = `SELECT name FROM sqlite_schema WHERE type = 'view'`

// parseShellJSON splits the sqlite3 shell's -json output into each statement's rows: a
// JSON array per statement that has rows, each statement ended by sqlSeparator's.
// Columns keep the order the shell printed them in, repeats included.
func parseShellJSON(out []byte, n int) ([]agent.Rows, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	results := make([]agent.Rows, 0, n)
	cur := agent.Rows{Rows: []agent.Row{}}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading the sqlite3 output: %w", err)
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return nil, fmt.Errorf("reading the sqlite3 output: unexpected %v", tok)
		}
		var rows []agent.Row
		var cols []string
		separator := false
		for dec.More() {
			names, row, err := readJSONObject(dec)
			if err != nil {
				return nil, fmt.Errorf("reading the sqlite3 output: %w", err)
			}
			if len(names) == 1 && names[0] == "~hopsesh-end~" {
				separator = true
				continue
			}
			if cols == nil {
				cols = names
			}
			rows = append(rows, row)
		}
		if _, err := dec.Token(); err != nil { // ]
			return nil, fmt.Errorf("reading the sqlite3 output: %w", err)
		}
		if separator {
			results = append(results, cur)
			cur = agent.Rows{Rows: []agent.Row{}}
			continue
		}
		cur.Columns = cols
		cur.Rows = append(cur.Rows, rows...)
	}
	if len(results) != n {
		return nil, fmt.Errorf("reading the sqlite3 output: %d result(s) for %d statement(s)", len(results), n)
	}
	return results, nil
}

// readJSONObject reads one {…} of the decoder's stream: its keys in order and its values.
func readJSONObject(dec *json.Decoder) ([]string, agent.Row, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, fmt.Errorf("unexpected %v", tok)
	}
	var names []string
	row := agent.Row{}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		name, _ := k.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, nil, err
		}
		v, err := jsonValue(raw)
		if err != nil {
			return nil, nil, err
		}
		names = append(names, name)
		row[name] = v
	}
	_, err = dec.Token() // }
	return names, row, err
}

// jsonValue is a JSON value as a Row value: whole numbers int64, other numbers float64,
// strings, null nil, and {"b64": …} bytes (the python3 script's BLOBs).
func jsonValue(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	switch x := v.(type) {
	case nil, string:
		return x, nil
	case json.Number:
		if !strings.ContainsAny(x.String(), ".eE") {
			if n, err := x.Int64(); err == nil {
				return n, nil
			}
		}
		return x.Float64()
	case map[string]any:
		if s, ok := x["b64"].(string); ok && len(x) == 1 {
			return base64.StdEncoding.DecodeString(s)
		}
	}
	return nil, fmt.Errorf("unexpected value %s", raw)
}

// remoteSQLError names a remote reader's failure the way the SDK does.
func remoteSQLError(db, stderr string, code int) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", code)
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not authorized"), strings.Contains(lower, "prohibited"):
		return fmt.Errorf("%w: %s", agent.ErrDenied, msg)
	case strings.Contains(lower, "too big"):
		return fmt.Errorf("%w: %s", agent.ErrTooLarge, msg)
	case strings.Contains(lower, "database is locked"), strings.Contains(lower, "database is busy"):
		return fmt.Errorf("%w: %s", errBusy, msg)
	case strings.Contains(lower, "unable to open database"):
		return fmt.Errorf("%w: %s (%s)", errCantOpen, db, msg)
	}
	return fmt.Errorf("reading %s: %s", db, msg)
}

// pythonSQLScript reads a database read only with Python's sqlite3 module, with SQLite's
// authorizer set to the same rules as the local reader (sqlAuthorized). Its request comes
// on stdin; it prints {"results": [{"columns": [...], "rows": [[...]]}]} or exits 2 after
// printing the error on stderr.
const pythonSQLScript = `import base64, json, sqlite3, sys
req = json.load(sys.stdin)
fold = lambda xs: set(x.lower() for x in xs)
allow, deny, funcs = fold(req["allow"]), fold(req["deny"]), fold(req["funcs"])
limited = req["limited"]
banned = {"load_extension", "readfile", "writefile", "edit", "fts3_tokenizer"}
try:
    conn = sqlite3.connect(req["uri"], uri=True, timeout=2.0, isolation_level=None)
    stored = set(r[0].lower() for r in conn.execute("SELECT name FROM sqlite_master WHERE type IN ('table', 'view')"))
except sqlite3.Error as e:
    sys.stderr.write(str(e)); sys.exit(2)
OK, DENY = 0, 1
def auth(action, a1, a2, db, src):
    if action in (21, 22, 33):
        return OK
    if action == 31:
        return DENY if (a2 or "").lower() in banned else OK
    if action == 19:
        return OK if (a1 or "").lower() == "table_info" else DENY
    if action == 20:
        t = (a1 or "").lower()
        if not a2 and t not in stored and not t.startswith("sqlite_") and not t.startswith("pragma_"):
            return OK
        if t in deny:
            return DENY
        return OK if not limited or t in allow or t in funcs else DENY
    return DENY
def enc(v):
    if isinstance(v, (bytes, bytearray, memoryview)):
        return {"b64": base64.b64encode(bytes(v)).decode("ascii")}
    return v
try:
    conn.set_authorizer(auth)
    if hasattr(conn, "setlimit"):
        conn.setlimit(sqlite3.SQLITE_LIMIT_LENGTH, req["maxlen"])
    conn.execute("BEGIN")
    out = []
    for q in req["stmts"]:
        cur = conn.execute(q)
        cols = [d[0] for d in (cur.description or [])]
        out.append({"columns": cols, "rows": [[enc(v) for v in r] for r in cur]})
    conn.execute("COMMIT")
    sys.stdout.write(json.dumps({"results": out}, allow_nan=False))
except (sqlite3.Error, ValueError, OverflowError) as e:
    sys.stderr.write(str(e)); sys.exit(2)
`

func (s remoteSQL) python3(ctx context.Context, db, uri string, stmts []agent.Statement, texts []string) ([]agent.Rows, error) {
	allow, deny := s.allow, s.deny
	if allow == nil {
		allow = []string{}
	}
	if deny == nil {
		deny = []string{}
	}
	req, err := json.Marshal(map[string]any{"uri": uri, "stmts": texts, "allow": allow, "deny": deny,
		"funcs": agent.SQLTableFuncs(), "limited": s.limited, "maxlen": min(longestValue(stmts), 1<<30)})
	if err != nil {
		return nil, err
	}
	limit := outputCap(stmts)
	argv := []string{"sh", "-c", capScript, "sh", strconv.FormatInt(limit+1, 10), "python3", "-I", "-c", pythonSQLScript}
	res, err := s.m.sqlRunner().Run(ctx, argv, agent.RunOptions{Stdin: req})
	if err != nil {
		return nil, err
	}
	if int64(len(res.Stdout)) > limit {
		return nil, fmt.Errorf("%w: the output is over %d bytes", agent.ErrTooLarge, limit)
	}
	if res.Code != 0 {
		return nil, remoteSQLError(db, string(res.Stderr), res.Code)
	}
	return parsePythonJSON(res.Stdout, len(texts))
}

// parsePythonJSON reads the python3 script's output.
func parsePythonJSON(out []byte, n int) ([]agent.Rows, error) {
	var doc struct {
		Results []struct {
			Columns []string            `json:"columns"`
			Rows    [][]json.RawMessage `json:"rows"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("reading the python3 output: %w", err)
	}
	if len(doc.Results) != n {
		return nil, errors.New("reading the python3 output: a result is missing")
	}
	results := make([]agent.Rows, 0, n)
	for _, r := range doc.Results {
		rows := agent.Rows{Columns: r.Columns, Rows: make([]agent.Row, 0, len(r.Rows))}
		for _, vals := range r.Rows {
			if len(vals) != len(r.Columns) {
				return nil, errors.New("reading the python3 output: a row and its columns differ")
			}
			row := agent.Row{}
			for i, raw := range vals {
				v, err := jsonValue(raw)
				if err != nil {
					return nil, fmt.Errorf("reading the python3 output: %w", err)
				}
				row[r.Columns[i]] = v
			}
			rows.Rows = append(rows.Rows, row)
		}
		results = append(results, rows)
	}
	return results, nil
}
