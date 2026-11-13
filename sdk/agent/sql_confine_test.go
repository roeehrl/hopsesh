package agent_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

const dbSchema = `
CREATE TABLE session_v2 (id TEXT PRIMARY KEY, title TEXT, time_updated INTEGER);
CREATE TABLE session_message (session_id TEXT, seq INTEGER, data TEXT);
CREATE TABLE credential (id TEXT, value TEXT);
CREATE VIEW leak AS SELECT value AS title FROM credential;
INSERT INTO session_v2 VALUES ('ses_1', 'one', 1), ('ses_2', 'two', 2);
INSERT INTO session_message VALUES ('ses_1', 1, '{"type":"user"}'), ('ses_1', 2, '{"type":"assistant"}');
INSERT INTO credential VALUES ('openai', 'sk-never-read');
`

// customDB is a database a variable put outside the agent's roots (OPENCODE_DB).
const customDB = "/srv/oc/custom.db"

func sqlHost(t *testing.T, tables ...string) (*agenttest.FakeHost, agent.Host, string) {
	t.Helper()
	fh := agenttest.NewFakeHost("/home/u")
	db := "/home/u/.local/share/oc/oc.db"
	for _, p := range []string{db, customDB, "/home/u/.local/share/oc/drafts.sqlite"} {
		fh.LoadSQL(t, p)
		if err := fh.ExecSQL(p, dbSchema); err != nil {
			t.Fatal(err)
		}
	}
	fh.Put(db+"-wal", []byte("wal"), time.Now())
	spec := agent.Spec{ID: "oc", Name: "OC", Roots: []agent.Root{{Name: "data", Default: map[string]string{"*": "~/.local/share/oc"}}},
		SQLFiles: []string{"{data}/oc*.db", "{db}", "{absent}/x.db"}, SQLTables: tables, SQLDenied: []string{"credential"}}
	in := agent.DefaultInstall(spec, fh)
	in.Roots["db"] = customDB
	return fh, agent.Confine(fh, spec, in), db
}

func TestConfinedSQL(t *testing.T) {
	ctx := context.Background()
	fh, h, db := sqlHost(t, "session_v2", "session_message", "leak")
	sq, ok := agent.HostSQL(h)
	if !ok {
		t.Fatal("a confined FakeHost has SQL")
	}
	r, err := sq.Query(ctx, db, "SELECT id, title, time_updated FROM session_v2 WHERE time_updated > ? ORDER BY id", 0)
	if err != nil || len(r.Rows) != 2 || r.Rows[0].String("id") != "ses_1" || r.Rows[1].Int("time_updated") != 2 {
		t.Fatalf("Query: %+v, %v", r, err)
	}
	if !slices.Equal(r.Columns, []string{"id", "title", "time_updated"}) {
		t.Fatalf("columns %v", r.Columns)
	}
	// The database files are secrets: the module cannot open, copy or package them.
	for _, p := range []string{db, db + "-wal", customDB} {
		if _, err := h.FS().ReadFile(p, 1<<20); !errors.Is(err, agent.ErrDenied) {
			t.Fatalf("reading %s must be denied, got %v", p, err)
		}
	}
	// A database a variable put elsewhere is read through SQL.
	if r, err := sq.Query(ctx, customDB, "SELECT id FROM session_v2"); err != nil || len(r.Rows) != 2 {
		t.Fatalf("the custom database: %+v, %v", r, err)
	}
	for _, q := range []string{
		"SELECT * FROM credential",           // denied by name
		"SELECT * FROM project",              // not listed
		"DELETE FROM session_v2",             // a write
		"SELECT * FROM session_v2; SELECT 1", // two statements
	} {
		if _, err := sq.Query(ctx, db, q); !errors.Is(err, agent.ErrDenied) {
			t.Errorf("%s must be denied, got %v", q, err)
		}
	}
	// A view of a denied table passes the text check; SQLite's authorizer refuses the read.
	if r, err := sq.Query(ctx, db, "SELECT title FROM leak"); err == nil {
		t.Fatalf("a view read a denied table: %v", r.Rows)
	}
	// A database SQLFiles does not name is refused, whatever the query: one elsewhere,
	// and another SQLite file in the agent's own folder (a desktop app's drafts).
	for _, other := range []string{"/home/u/other.db", "/home/u/.local/share/oc/drafts.sqlite", "/home/u/.local/share/oc/sub/oc.db", "oc.db"} {
		if _, err := sq.Query(ctx, other, "SELECT * FROM session_v2"); !errors.Is(err, agent.ErrDenied) {
			t.Fatalf("%s must be denied, got %v", other, err)
		}
	}
	// Batch: several queries in one transaction, each with its own guards.
	rs, err := sq.Batch(ctx, db, []agent.Statement{
		{Query: "SELECT id FROM session_v2"},
		{Query: "SELECT seq, data FROM session_message WHERE session_id = ? ORDER BY seq", Args: []any{"ses_1"}},
	})
	if err != nil || len(rs) != 2 || len(rs[1].Rows) != 2 || rs[1].Rows[1].Int("seq") != 2 {
		t.Fatalf("Batch: %+v, %v", rs, err)
	}
	if _, err := sq.Batch(ctx, db, []agent.Statement{{Query: "SELECT id FROM session_v2"}, {Query: "SELECT * FROM credential"}}); !errors.Is(err, agent.ErrDenied) {
		t.Fatalf("one denied query refuses the batch, got %v", err)
	}
	if _, err := sq.Batch(ctx, db, []agent.Statement{{Query: "SELECT id FROM session_v2", Limits: agent.SQLLimits{MaxRows: 1}}}); !errors.Is(err, agent.ErrTooLarge) {
		t.Fatalf("rows over the limit: %v", err)
	}
	if _, err := sq.Batch(ctx, db, []agent.Statement{{Query: "SELECT data FROM session_message", Limits: agent.SQLLimits{MaxBytes: 20}}}); !errors.Is(err, agent.ErrTooLarge) {
		t.Fatalf("bytes over the limit: %v", err)
	}
	// Two columns of one name would lose a value in a Row.
	if _, err := sq.Query(ctx, db, "SELECT s.id, m.session_id AS id FROM session_v2 s JOIN session_message m ON m.session_id = s.id"); err == nil {
		t.Fatal("two columns named id must be refused")
	}
	// A value longer than the statement keeps fails in SQLite, before it is read whole.
	if _, err := sq.Batch(ctx, db, []agent.Statement{{Query: "SELECT randomblob(100000) AS b", Limits: agent.SQLLimits{MaxBytes: 1000}}}); !errors.Is(err, agent.ErrTooLarge) {
		t.Fatalf("a long value: %v", err)
	}
	// A query that never ends stops with its context.
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = sq.Query(short, db, "WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n) SELECT count(*) FROM n")
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("an endless query: %v after %v", err, time.Since(start))
	}
	t.Logf("an endless query stopped after %v: %v", time.Since(start).Round(time.Millisecond), err)
	cols, err := sq.Columns(ctx, db, "session_message")
	if err != nil || !slices.Equal(cols, []string{"session_id", "seq", "data"}) {
		t.Fatalf("Columns: %v, %v", cols, err)
	}
	if _, err := sq.Columns(ctx, db, "credential"); !errors.Is(err, agent.ErrDenied) {
		t.Fatalf("the columns of a denied table: %v", err)
	}
	for _, read := range fh.SQLReads() {
		if read != "session_v2" && read != "session_message" {
			t.Errorf("read %s", read)
		}
	}
}

// Below the text check, the host's own limiter refuses tables (the core's authorizer).
func TestFakeHostAuthorizer(t *testing.T) {
	ctx := context.Background()
	fh, _, db := sqlHost(t, "session_v2")
	limited := fh.SQL().(agent.SQLTableLimiter).Only([]string{"session_v2", "leak"}, []string{"credential"})
	if _, err := limited.Query(ctx, db, "SELECT * FROM session_v2"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"SELECT * FROM credential", "SELECT * FROM leak", "SELECT * FROM session_message",
		"SELECT * FROM 'credential'", "SELECT * FROM sqlite_master", "SELECT 'x' IN credential"} {
		if r, err := limited.Query(ctx, db, q); err == nil {
			t.Errorf("%s read %v", q, r.Rows)
		}
	}
	// Read only: even an unlimited reader writes nothing.
	if _, err := fh.SQL().Query(ctx, db, "DELETE FROM session_v2"); err == nil {
		t.Fatal("the reader wrote")
	}
	if r, _ := fh.SQL().Query(ctx, db, "SELECT count(*) AS n FROM session_v2"); r.Rows[0].Int("n") != 2 {
		t.Fatalf("rows changed: %+v", r)
	}
}

// A module without tables, or a host without SQL, has no reader.
func TestNoSQL(t *testing.T) {
	_, h, _ := sqlHost(t)
	if _, ok := agent.HostSQL(h); ok {
		t.Fatal("a Spec without SQLTables must get no SQL")
	}
}
