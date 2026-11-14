package host

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestPickSQLTier(t *testing.T) {
	for out, want := range map[string]string{
		"sqlite3 3.54.0 2026-04-09 x\nsqlite3-blobs yes\n":                 tierSQLite3,
		"sqlite3 3.54.0 2026-04-09 x\nsqlite3-blobs yes\npython3 3.54.0\n": tierSQLite3 + "+python3",
		"sqlite3 3.43.2 2023-10-10 13:08:14 abc\npython3 3.43.2\n":         tierPython3 + "+python3", // BLOBs read as text
		"sqlite3 3.43.2 2023-10-10 13:08:14 abc\n":                         tierNone,                 // the same, and no python3
		"sqlite3 3.31.1 2020-01-27 19:55:54 abc\npython3 3.31.1\n":         tierPython3 + "+python3", // no -safe
		"python3 3.40.1\n":                   tierPython3 + "+python3",
		"sqlite3 \n":                         tierNone,
		"":                                   tierNone,
		"sqlite3 4.0.0\nsqlite3-blobs yes\n": tierSQLite3,
	} {
		tier, python := pickSQLTier(out)
		if python {
			tier += "+python3"
		}
		if tier != want {
			t.Errorf("%q: %s, want %s", out, tier, want)
		}
	}
}

func TestBindSQL(t *testing.T) {
	for _, c := range []struct {
		q    string
		args []any
		want string
	}{
		{"SELECT a FROM t WHERE id = ? AND n > ?", []any{"it's", 3}, "SELECT a FROM t WHERE id = 'it''s' AND n > 3"},
		{"SELECT '?', \"a?\", [b?] FROM t WHERE x = ? -- ?\n", []any{nil}, "SELECT '?', \"a?\", [b?] FROM t WHERE x = NULL -- ?\n"},
		{"SELECT x /* ? */ FROM t WHERE y = ?", []any{[]byte{0, 255}}, "SELECT x /* ? */ FROM t WHERE y = X'00ff'"},
		{"SELECT x FROM t WHERE y -?", []any{-5}, "SELECT x FROM t WHERE y -(-5)"},
		{"SELECT ?, ?", []any{1.0, true}, "SELECT 1.0, 1"},
		{"SELECT json_extract(data, '$.a') FROM t WHERE id = ?", []any{agent.SessionID("ses_1")}, "SELECT json_extract(data, '$.a') FROM t WHERE id = 'ses_1'"},
	} {
		got, err := bindSQL(c.q, c.args)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v", c.q, got, err)
		}
	}
	for _, c := range []struct {
		q    string
		args []any
	}{
		{"SELECT ?", nil},
		{"SELECT 1", []any{1}},
		{"SELECT ?1", []any{1}},
		{"SELECT :name", nil},
		{"SELECT @name", nil},
		{"SELECT 'open", nil},
		{"SELECT ?", []any{"nul\x00"}},
		{"SELECT ?", []any{struct{}{}}},
		{"SELECT /* open", nil},
	} {
		if got, err := bindSQL(c.q, c.args); err == nil {
			t.Errorf("%s %v: bound as %q", c.q, c.args, got)
		}
	}
}

// fakeTools stands in for a machine reached over SSH: it answers the tools probe as told
// and records every command, so tier selection is checked without a network.
type fakeTools struct {
	mu    sync.Mutex
	probe string
	calls [][]string
	stdin [][]byte
	reply func(argv []string) agent.Result
}

func (f *fakeTools) Run(_ context.Context, argv []string, o agent.RunOptions) (agent.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, argv)
	f.stdin = append(f.stdin, o.Stdin)
	f.mu.Unlock()
	if len(argv) == 3 && argv[2] == sqlProbeScript {
		return agent.Result{Stdout: []byte(f.probe)}, nil
	}
	return f.reply(argv), nil
}

func (f *fakeTools) ran(tool string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if len(c) > 5 && c[5] == tool {
			n++
		}
	}
	return n
}

func remoteMachine(os string, exec agent.Exec) *Machine {
	return &Machine{Name: "there", Facts: Facts{OS: os, Home: "/home/u"}, sqlExec: exec}
}

// A machine reached over SSH reads with the best tool it has, probed once and kept; with
// neither, or on Windows, it has no reader and the module falls back.
func TestRemoteSQLTierSelection(t *testing.T) {
	ctx := context.Background()
	db := "/home/u/.local/share/agent/agent.db"
	t.Run("sqlite3", func(t *testing.T) {
		end := `[{"~hopsesh-end~":"hopsesh"}]` + "\n"
		f := &fakeTools{probe: "sqlite3 3.54.0 2026-04-09 x\nsqlite3-blobs yes\n", reply: func([]string) agent.Result {
			return agent.Result{Stdout: []byte(`[{"~hopsesh-views~":null}]` + "\n" + end + `[{"~hopsesh-cols~":1,"id":null,"n":null}]` + "\n" + end +
				`[{"id":"ses_a","n":2,"b":x'00ff41'}]` + "\n" + end)}
		}}
		m := remoteMachine("linux", f)
		r := m.SQL().(agent.SQLTableLimiter).Only([]string{"session"}, []string{"credential"})
		got, err := r.Query(ctx, db, "SELECT id, count(*) AS n FROM session WHERE id = ?", "ses_a")
		if err != nil || got.Rows[0].String("id") != "ses_a" || got.Rows[0].Int("n") != 2 || strings.Join(got.Columns, ",") != "id,n,b" {
			t.Fatalf("%+v %v", got, err)
		}
		if b, ok := got.Rows[0]["b"].([]byte); !ok || string(b) != "\x00\xffA" {
			t.Fatalf("a BLOB the shell marked: %#v", got.Rows[0]["b"])
		}
		if _, err := m.SQL().(agent.SQLTableLimiter).Only([]string{"session"}, nil).Query(ctx, db, "SELECT 1 AS x FROM session"); err != nil {
			t.Fatal(err)
		}
		if f.ran("sqlite3") != 2 || len(f.calls) != 3 {
			t.Fatalf("calls: %v", f.calls)
		}
		last := f.calls[1]
		if !strings.Contains(last[len(last)-1], "WHERE id = 'ses_a'") || !strings.Contains(last[len(last)-2], "~hopsesh-views~") || last[len(last)-3] != "file:"+db+"?mode=ro" {
			t.Fatalf("the shell's arguments: %q", last)
		}
		for _, flag := range []string{"-readonly", "-safe", "-bail", "-json", "--blob-quote sql"} {
			if !strings.Contains(strings.Join(last, " "), flag) {
				t.Errorf("no %s in %q", flag, last)
			}
		}
		// The text is checked again once bound, before anything is sent.
		if _, err := r.Query(ctx, db, "SELECT value FROM credential"); !errors.Is(err, agent.ErrDenied) {
			t.Fatalf("a denied table: %v", err)
		}
		if len(f.calls) != 3 {
			t.Fatal("a refused query was sent")
		}
		// Unlimited, a remote reader reads nothing: the shell has no authorizer.
		if _, err := m.SQL().Query(ctx, db, "SELECT id FROM session"); !errors.Is(err, agent.ErrDenied) {
			t.Fatalf("an unlimited remote read: %v", err)
		}
	})
	t.Run("python3", func(t *testing.T) {
		f := &fakeTools{probe: "sqlite3 3.31.1 2020-01-27 x\npython3 3.31.1\n", reply: func([]string) agent.Result {
			return agent.Result{Stdout: []byte(`{"results":[{"columns":["id","data"],"rows":[["ses_a",{"b64":"AP9B"}]]}]}`)}
		}}
		m := remoteMachine("darwin", f)
		r := m.SQL().(agent.SQLTableLimiter).Only([]string{"session"}, []string{"credential"})
		got, err := r.Query(ctx, db, "SELECT id, data FROM session WHERE id = ?", "ses_a")
		if err != nil || string(got.Rows[0]["data"].([]byte)) != "\x00\xffA" {
			t.Fatalf("%+v %v", got, err)
		}
		if f.ran("python3") != 1 {
			t.Fatalf("calls: %v", f.calls)
		}
		var req struct {
			Stmts   []string
			Allow   []string
			Deny    []string
			Limited bool
			URI     string
		}
		if err := json.Unmarshal(f.stdin[1], &req); err != nil {
			t.Fatal(err)
		}
		if !req.Limited || req.Allow[0] != "session" || req.Deny[0] != "credential" || req.Stmts[0] != "SELECT id, data FROM session WHERE id = 'ses_a'" || req.URI != "file:"+db+"?mode=ro" {
			t.Fatalf("the script's request: %+v", req)
		}
	})
	t.Run("none", func(t *testing.T) {
		f := &fakeTools{probe: "", reply: func([]string) agent.Result { t.Fatal("ran a reader"); return agent.Result{} }}
		m := remoteMachine("linux", f)
		first, again := m.SQL(), m.SQL() // asked twice, probed once
		if first != nil || again != nil || len(f.calls) != 1 {
			t.Fatalf("a machine without tools has a reader (%d probes)", len(f.calls))
		}
	})
	t.Run("windows", func(t *testing.T) {
		f := &fakeTools{probe: "sqlite3 3.45.1\n"}
		if remoteMachine("windows", f).SQL() != nil || len(f.calls) != 0 {
			t.Fatal("Windows over SSH reads with the API, never a shell")
		}
	})
}

// toolsOnly is a folder with only these tools (and sh and head, which the readers use).
func toolsOnly(t *testing.T, tools ...string) string {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range append([]string{"sh", "head"}, tools...) {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not on this machine", tool)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// The real tools read a real database over a stand-in ssh that runs the command line here,
// with only that tool on PATH: rows, types, empty results, the batch split, and the guards
// (a denied table, a view over one, a value over its limit, a missing file). The database
// is in WAL mode and open in a program, as an agent's is while it runs; once that program
// closed it, the tools cannot read it read only, and the module is told to use its agent.
func TestRemoteSQLTiersOnRealTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the remote tiers run on POSIX machines")
	}
	ctx := context.Background()
	for _, tier := range []string{tierSQLite3, tierPython3} {
		t.Run(tier, func(t *testing.T) {
			db := sqlDB(t, true)
			agentConn := openWAL(t, db)
			tools := toolsOnly(t, tier)
			conn := fakeSSH(t)
			shim, _, _ := strings.Cut(os.Getenv("PATH"), string(os.PathListSeparator))
			t.Setenv("PATH", shim+string(os.PathListSeparator)+tools)
			// The stand-in ssh has no SFTP: the machine's files are this machine's.
			m := &Machine{Name: "box", Conn: conn, Facts: Facts{OS: "linux"}, fs: localFS{}}
			defer m.Close()
			if m.SQL() == nil || m.sqlTier != tier {
				t.Skipf("the tier picked is %q", m.sqlTier)
			}
			r := m.SQL().(agent.SQLTableLimiter).Only([]string{"session", "message"}, sqlDeny)
			out, err := r.Batch(ctx, db, []agent.Statement{
				{Query: "SELECT id, title, 2 AS n, 1.5 AS f, NULL AS z FROM session WHERE id = ?", Args: []any{"ses_a"}},
				{Query: "SELECT id FROM message WHERE seq > ?", Args: []any{9}},
				{Query: "SELECT id, text FROM message ORDER BY seq"},
			})
			if err != nil {
				t.Fatal(err)
			}
			row := out[0].Rows[0]
			if row.String("title") != "Fix the build" || row["n"] != int64(2) || row["f"] != 1.5 || !row.Null("z") {
				t.Fatalf("row %#v", row)
			}
			if strings.Join(out[0].Columns, ",") != "id,title,n,f,z" || len(out[1].Rows) != 0 || len(out[2].Rows) != 2 || out[2].Rows[1].String("text") != "A missing import." {
				t.Fatalf("results %+v", out)
			}
			if _, err := r.Query(ctx, db, "SELECT s.id, c.value FROM session s JOIN credential c"); !errors.Is(err, agent.ErrDenied) {
				t.Fatalf("a denied table: %v", err)
			}
			viewed := m.SQL().(agent.SQLTableLimiter).Only(sqlAllow, sqlDeny)
			if _, err := viewed.Query(ctx, db, "SELECT * FROM leaked"); !errors.Is(err, agent.ErrDenied) {
				t.Fatalf("a view over a denied table: %v", err)
			}
			if _, err := r.Query(ctx, db, "SELECT a.id, a.id FROM session a"); err == nil {
				t.Fatal("two columns of one name were read")
			}
			if _, err := r.Batch(ctx, db, []agent.Statement{{Query: "SELECT text FROM message", Limits: agent.SQLLimits{MaxBytes: 8}}}); !errors.Is(err, agent.ErrTooLarge) {
				t.Fatalf("a value over its limit: %v", err)
			}
			// A missing database is no agent's database here, never a missing session.
			if _, err := r.Query(ctx, filepath.Join(filepath.Dir(db), "missing.db"), "SELECT id FROM session"); !errors.Is(err, agent.ErrNotInstalled) || errors.Is(err, agent.ErrNotFound) {
				t.Fatalf("a missing database: %v", err)
			}
			cols, err := r.Columns(ctx, db, "message")
			if err != nil || strings.Join(cols, ",") != "id,session_id,seq,text" {
				t.Fatalf("columns %v %v", cols, err)
			}
			// A comment at the end of a query, which the text check allows, hides nothing.
			if got, err := r.Query(ctx, db, "SELECT id FROM session -- trailing note"); err != nil || len(got.Rows) != 1 {
				t.Fatalf("a query ending in a comment: %+v %v", got, err)
			}
			// No rows still names the columns, in the query's order.
			if got, err := r.Query(ctx, db, "SELECT title, id FROM session WHERE 0"); err != nil || len(got.Rows) != 0 || strings.Join(got.Columns, ",") != "title,id" {
				t.Fatalf("an empty result: %+v %v", got, err)
			}
			// Text in other scripts comes back as it is.
			if got, err := r.Query(ctx, db, "SELECT 'héllo, 日本' AS t FROM session"); err != nil || got.Rows[0].String("t") != "héllo, 日本" {
				t.Fatalf("text: %+v %v", got, err)
			}
			// A BLOB comes back as its bytes.
			got, err := r.Query(ctx, db, "SELECT data FROM session")
			if b, ok := got.Rows[0]["data"].([]byte); err != nil || !ok || string(b) != "\x00\xffA" {
				t.Fatalf("a BLOB: %#v %v", got.Rows[0]["data"], err)
			}
			// Closed: never read as immutable, which takes no lock.
			agentConn()
			for _, f := range []string{db + "-wal", db + "-shm"} {
				os.Remove(f)
			}
			_, err = r.Query(ctx, db, "SELECT id FROM session")
			if err != nil && !errors.Is(err, agent.ErrUnsupported) {
				t.Fatalf("a closed WAL database: %v", err)
			}
			if _, e := os.Stat(db + "-wal"); err != nil && e == nil {
				t.Fatal("a failed read left a -wal file")
			}
		})
	}
}

// openWAL opens a WAL database as its agent does while it runs (its -wal and -shm files
// are there until it closes); the function returned closes it.
func openWAL(t *testing.T, db string) func() {
	t.Helper()
	d, err := sql.Open("sqlite", "file:"+uriPath(db, false))
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	if _, err := d.Exec("UPDATE session SET title = title"); err != nil {
		t.Fatal(err)
	}
	closed := false
	done := func() {
		if !closed {
			closed = true
			d.Close()
		}
	}
	t.Cleanup(done)
	return done
}

// A reader refuses output over its cap before decoding it, on both tiers.
func TestRemoteSQLOutputCap(t *testing.T) {
	big := []byte(strings.Repeat("x", 4<<20))
	for _, probe := range []string{"sqlite3 3.54.0 x\nsqlite3-blobs yes\n", "python3 3.12.1\n"} {
		f := &fakeTools{probe: probe, reply: func([]string) agent.Result { return agent.Result{Stdout: big} }}
		r := remoteMachine("linux", f).SQL().(agent.SQLTableLimiter).Only([]string{"session"}, nil)
		_, err := r.Batch(context.Background(), "/db", []agent.Statement{{Query: "SELECT id FROM session", Limits: agent.SQLLimits{MaxBytes: 16}}})
		if !errors.Is(err, agent.ErrTooLarge) {
			t.Errorf("%q: %v", probe, err)
		}
	}
}

// A batch too long for the sqlite3 shell's command line (Linux caps one argument at 128
// KiB) is read with python3, whose request goes on stdin; without python3 it is refused
// before anything is sent.
func TestRemoteSQLLongBatch(t *testing.T) {
	ids := make([]any, 0, 9000)
	marks := make([]string, 0, 9000)
	for i := range 9000 {
		ids = append(ids, fmt.Sprintf("ses_%08d", i))
		marks = append(marks, "?")
	}
	long := "SELECT id FROM session WHERE id IN (" + strings.Join(marks, ", ") + ")"
	for probe, want := range map[string]string{
		"sqlite3 3.54.0 x\nsqlite3-blobs yes\npython3 3.54.0\n": tierPython3,
		"sqlite3 3.54.0 x\nsqlite3-blobs yes\n":                 "",
	} {
		f := &fakeTools{probe: probe, reply: func([]string) agent.Result {
			return agent.Result{Stdout: []byte(`{"results":[{"columns":["id"],"rows":[]}]}`)}
		}}
		r := remoteMachine("linux", f).SQL().(agent.SQLTableLimiter).Only([]string{"session"}, nil)
		_, err := r.Query(context.Background(), "/db", long, ids...)
		switch {
		case want == "" && !errors.Is(err, agent.ErrTooLarge):
			t.Errorf("%q: %v", probe, err)
		case want == "" && len(f.calls) != 1:
			t.Errorf("%q: a refused batch was sent: %d calls", probe, len(f.calls))
		case want != "" && (err != nil || f.ran(want) != 1 || f.ran(tierSQLite3) != 0):
			t.Errorf("%q: %v, calls %d", probe, err, len(f.calls))
		}
	}
	// A short one still goes to the shell.
	f := &fakeTools{probe: "sqlite3 3.54.0 x\nsqlite3-blobs yes\npython3 3.54.0\n", reply: func([]string) agent.Result {
		end := `[{"~hopsesh-end~":"hopsesh"}]` + "\n"
		return agent.Result{Stdout: []byte(`[{"~hopsesh-views~":null}]` + "\n" + end + `[{"~hopsesh-cols~":1,"id":null}]` + "\n" + end + end)}
	}}
	r := remoteMachine("linux", f).SQL().(agent.SQLTableLimiter).Only([]string{"session"}, nil)
	if _, err := r.Query(context.Background(), "/db", "SELECT id FROM session"); err != nil || f.ran(tierSQLite3) != 1 {
		t.Fatalf("%v %v", err, f.calls)
	}
}

// A machine whose probe failed has no reader for a while, and is not probed again by
// every reader meanwhile.
func TestRemoteSQLProbeFailureIsKept(t *testing.T) {
	f := &failingProbe{}
	m := remoteMachine("linux", f)
	first, again := m.SQL(), m.SQL() // asked twice, probed once
	if first != nil || again != nil || f.calls != 1 {
		t.Fatalf("%d probes", f.calls)
	}
	m.sqlFailed = time.Now().Add(-sqlProbeRetry - time.Second)
	if m.SQL() != nil || f.calls != 2 {
		t.Fatalf("not probed again after %s: %d probes", sqlProbeRetry, f.calls)
	}
}

type failingProbe struct{ calls int }

func (f *failingProbe) Run(context.Context, []string, agent.RunOptions) (agent.Result, error) {
	f.calls++
	return agent.Result{}, errors.New("connection refused")
}

func TestMarkedBlobs(t *testing.T) {
	in := `[{"a":"x'00'","b":x'00ff41',"c":"q\"x'1'","d":x''}]`
	want := `[{"a":"x'00'","b":{"hex":"00ff41"},"c":"q\"x'1'","d":{"hex":""}}]`
	if got := string(markedBlobs([]byte(in))); got != want {
		t.Fatalf("%s", got)
	}
	if got := unwrappedColumns([]string{"id", "id:1", "a:2", "n"}); strings.Join(got, ",") != "id,id,a:2,n" {
		t.Fatalf("%v", got)
	}
}
