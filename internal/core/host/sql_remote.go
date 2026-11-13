package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
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

// sqlProbeScript says which of the tools a remote read can use are there: the sqlite3
// shell's version and whether its JSON marks BLOBs (--blob-quote, which an older shell
// prints as text), then, unless that shell does all a read needs, python3's.
const sqlProbeScript = `if command -v sqlite3 >/dev/null 2>&1; then
printf 'sqlite3 %s\n' "$(sqlite3 -version 2>/dev/null)"
case "$(sqlite3 -init /dev/null -cmd '.mode json --blob-quote sql' :memory: "SELECT x'01' AS b" 2>/dev/null)" in *"x'01'"*) echo 'sqlite3-blobs yes'; exit 0;; esac
fi
if command -v python3 >/dev/null 2>&1; then python3 -I -c 'import sqlite3; print("python3 " + sqlite3.sqlite_version)' 2>/dev/null; fi
exit 0`

// sqlProbeRetry is how long a machine whose probe failed has no reader before it is
// probed again.
const sqlProbeRetry = 30 * time.Second

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
	if tier, _ := m.sqlTierOf(); tier == tierNone {
		return nil
	}
	return remoteSQL{m: m}
}

// sqlTierOf probes the machine for a remote tier the first time it is asked, and says
// whether its sqlite3 shell marks BLOBs. A failed probe is kept for sqlProbeRetry, so a
// machine out of reach is not probed again by every reader meanwhile.
func (m *Machine) sqlTierOf() (string, bool) {
	m.sqlMu.Lock()
	defer m.sqlMu.Unlock()
	if m.sqlTier != "" {
		return m.sqlTier, m.sqlBlobs
	}
	if !m.sqlFailed.IsZero() && time.Since(m.sqlFailed) < sqlProbeRetry {
		return tierNone, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := m.sqlRunner().Run(ctx, []string{"sh", "-c", sqlProbeScript}, agent.RunOptions{})
	if err != nil {
		if m.Log != nil {
			m.Log.Warn("probing SQL tools", "machine", m.Name, "err", err)
		}
		m.sqlFailed = time.Now()
		return tierNone, false
	}
	m.sqlFailed = time.Time{}
	m.sqlTier, m.sqlBlobs = pickSQLTier(string(res.Stdout))
	return m.sqlTier, m.sqlBlobs
}

// sqlRunner runs the remote readers' programs: the machine's own Exec, or a stand-in.
func (m *Machine) sqlRunner() agent.Exec {
	if m.sqlExec != nil {
		return m.sqlExec
	}
	return m.Exec()
}

// pickSQLTier reads sqlProbeScript's output: the sqlite3 shell when it has -safe and
// -json and marks BLOBs; else python3, which sets SQLite's authorizer and returns BLOBs as
// bytes; else an older sqlite3 shell (3.37+), which returns a BLOB as text; else none.
// blobs says whether the shell picked marks BLOBs.
func pickSQLTier(out string) (tier string, blobs bool) {
	shell, marks, python := false, false, false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "sqlite3":
			shell = versionAtLeast(f[1], 3, 37)
		case "sqlite3-blobs":
			marks = true
		case "python3":
			python = true
		}
	}
	switch {
	case shell && marks:
		return tierSQLite3, true
	case python:
		return tierPython3, false
	case shell:
		return tierSQLite3, false
	}
	return tierNone, false
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
		switch tier, _ := s.m.sqlTierOf(); tier {
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
		switch tier, _ := s.m.sqlTierOf(); tier {
		case tierSQLite3:
			out, err = s.columnsSQLite3(ctx, db, uri, texts[0])
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
// files are checked over SFTP, and a WAL database told from its header there, so a closed
// one is never opened through a WAL (which some SQLite builds do by making its files).
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
	isWAL := func(p string) bool {
		fsys, err := s.m.FS(ctx)
		if err != nil {
			return false
		}
		f, err := fsys.Open(p)
		if err != nil {
			return false
		}
		defer f.Close()
		var h [20]byte
		if _, err := io.ReadFull(f, h[:]); err != nil {
			return false
		}
		return walHeader(h)
	}
	if s.m.Conn == nil && s.m.fs == nil {
		stat = func(string) (fileState, error) { return fileState{}, errors.New("no filesystem") }
		isWAL = nil
	}
	// A remote database path is always a POSIX one (Windows has no reader).
	uri := "file:" + uriPath(db, false) + "?mode=ro"
	err := retryBusy(ctx, func() error {
		return whenClosed(db, stat, isWAL, func(immutable bool) error {
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

// sqlColumnsOf is one row naming q's columns (all NULL), even when q has no rows: q runs
// with LIMIT 0, so SQLite stops before it reads anything.
func sqlColumnsOf(q string) string {
	return `SELECT * FROM (SELECT 1 AS "~hopsesh-cols~") LEFT JOIN (SELECT * FROM (` + q + "\n) LIMIT 0) ON 1"
}

// sqlViewGuard fails (an integer overflow) when the module's tables include a view: the
// shell has no authorizer to see through one. It runs as its own argument, so the shell
// stops there, before any statement of the next one runs.
func sqlViewGuard(allow []string) (string, error) {
	lits := make([]string, 0, len(allow))
	for _, t := range allow {
		lit, err := sqlLiteral(strings.ToLower(t))
		if err != nil {
			return "", err
		}
		lits = append(lits, lit)
	}
	return `SELECT CASE WHEN EXISTS (SELECT 1 FROM sqlite_schema WHERE type = 'view' AND lower(name) IN (` + strings.Join(lits, ", ") +
		`)) THEN abs(-9223372036854775808) END AS "~hopsesh-views~"`, nil
}

// sqlite3 runs a batch in the sqlite3 shell, in one transaction: the view guard (its own
// argument), then for each statement a row naming its columns and its rows, each ended by
// the separator. A statement goes on its own line, so a comment at its end cannot hide
// what follows.
func (s remoteSQL) sqlite3(ctx context.Context, db, uri string, stmts []agent.Statement, texts []string) ([]agent.Rows, error) {
	guard, err := sqlViewGuard(s.allow)
	if err != nil {
		return nil, err
	}
	var script strings.Builder
	script.WriteString(sqlSeparator + ";\n") // ends the guard's result
	for _, q := range texts {
		script.WriteString(sqlColumnsOf(q) + ";\n" + sqlSeparator + ";\n")
		script.WriteString(q + "\n;\n" + sqlSeparator + ";\n")
	}
	script.WriteString("COMMIT;\n")
	out, err := s.runShell(ctx, db, uri, stmts, 1+2*len(texts), "BEGIN;\n"+guard+";\n", script.String())
	if err != nil {
		return nil, err
	}
	results := make([]agent.Rows, 0, len(texts))
	for i := range texts {
		cols, rows := out[1+2*i], out[2+2*i]
		if len(rows.Rows) == 0 && len(cols.Rows) == 1 && len(cols.Columns) > 0 {
			rows.Columns = unwrappedColumns(cols.Columns[1:])
		}
		results = append(results, rows)
	}
	return results, nil
}

// columnsSQLite3 lists a table's columns in the sqlite3 shell (pragma_table_info is
// SQLite's own).
func (s remoteSQL) columnsSQLite3(ctx context.Context, db, uri, q string) ([]agent.Rows, error) {
	return s.runShell(ctx, db, uri, []agent.Statement{{}}, 1, "BEGIN;\n"+q+";\n"+sqlSeparator+";\nCOMMIT;\n")
}

// runShell runs scripts in the sqlite3 shell, read only and safe, one argument each (the
// shell stops at the first that fails), with its output cut at the batch's cap, and
// splits the output into n results.
func (s remoteSQL) runShell(ctx context.Context, db, uri string, stmts []agent.Statement, n int, scripts ...string) ([]agent.Rows, error) {
	limit := outputCap(stmts)
	argv := []string{"sh", "-c", capScript, "sh", strconv.FormatInt(limit+1, 10),
		"sqlite3", "-readonly", "-safe", "-bail", "-init", "/dev/null", "-json"}
	if _, blobs := s.m.sqlTierOf(); blobs {
		argv = append(argv, "-cmd", ".mode json --blob-quote sql")
	}
	argv = append(argv, "-cmd", ".timeout 2000", "-cmd", ".limit length "+strconv.FormatInt(min(longestValue(stmts), 1<<30), 10), uri)
	argv = append(argv, scripts...)
	res, err := s.m.sqlRunner().Run(ctx, argv, agent.RunOptions{})
	if err != nil {
		return nil, err
	}
	if int64(len(res.Stdout)) > limit {
		return nil, fmt.Errorf("%w: the output is over %d bytes", agent.ErrTooLarge, limit)
	}
	if res.Code != 0 {
		if strings.Contains(string(res.Stderr), "integer overflow") && !bytes.Contains(res.Stdout, []byte("~hopsesh-end~")) {
			return nil, fmt.Errorf("%w: the module's tables include a view, read only where SQLite's authorizer checks it", agent.ErrDenied)
		}
		return nil, remoteSQLError(db, string(res.Stderr), res.Code)
	}
	return parseShellJSON(markedBlobs(res.Stdout), n)
}

// unwrappedColumns undoes SQLite's renaming of a repeated column in a subquery ("id:1"),
// so a query's columns read the same with and without rows.
func unwrappedColumns(cols []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c
		if j := strings.LastIndexByte(c, ':'); j > 0 {
			if _, err := strconv.Atoi(c[j+1:]); err == nil && slices.Contains(out[:i], c[:j]) {
				out[i] = c[:j]
			}
		}
	}
	return out
}

// markedBlobs turns the BLOBs the shell marks (x'00ff', outside any string) into JSON:
// {"hex":"00ff"}.
func markedBlobs(out []byte) []byte {
	if !bytes.Contains(out, []byte("x'")) {
		return out
	}
	var b bytes.Buffer
	inString, escaped := false, false
	for i := 0; i < len(out); i++ {
		c := out[i]
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
		case c == 'x' && i+1 < len(out) && out[i+1] == '\'':
			end := bytes.IndexByte(out[i+2:], '\'')
			if end < 0 {
				return out // not the shell's: the decoder reports it
			}
			b.WriteString(`{"hex":"`)
			b.Write(out[i+2 : i+2+end])
			b.WriteString(`"}`)
			i += 2 + end
			continue
		}
		b.WriteByte(c)
	}
	return b.Bytes()
}

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
// strings, null nil, and bytes for {"b64": …} (the python3 script's BLOBs) and {"hex": …}
// (the sqlite3 shell's, markedBlobs).
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
		if s, ok := x["hex"].(string); ok && len(x) == 1 {
			return hex.DecodeString(s)
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
// printing the error on stderr. Python before 3.11 has no setlimit: a long value is then
// caught by the output cap alone.
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
        if t in deny:
            return DENY
        if not a2 and t not in stored and not t.startswith("sqlite_") and not t.startswith("pragma_"):
            return OK
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
    sys.stdout.buffer.write(json.dumps({"results": out}, allow_nan=False, ensure_ascii=False).encode("utf-8"))
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
