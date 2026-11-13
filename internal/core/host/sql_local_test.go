package host

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

const sqlFixture = `
CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, title TEXT, data BLOB);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, seq INTEGER, text TEXT);
CREATE TABLE project (id TEXT PRIMARY KEY);
CREATE TABLE credential (id TEXT, value TEXT);
CREATE VIEW recent AS SELECT id, title FROM session;
CREATE VIEW leaked AS SELECT id, value AS title FROM credential;
INSERT INTO session VALUES ('ses_a', '/home/u/demo', 'Fix the build', X'00ff41');
INSERT INTO message VALUES ('msg_1', 'ses_a', 1, 'Why does it fail?'), ('msg_2', 'ses_a', 2, 'A missing import.');
INSERT INTO credential VALUES ('provider', 'sk-never-read');
`

// sqlDB makes a database in a folder whose name has the characters a URI gives a meaning.
func sqlDB(t *testing.T, wal bool) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "odd ?#% dir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "agent.db")
	d, err := sql.Open("sqlite", "file:"+uriPath(p))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if wal {
		if _, err := d.Exec("PRAGMA journal_mode=WAL"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(sqlFixture); err != nil {
		t.Fatal(err)
	}
	return p
}

var (
	sqlAllow = []string{"session", "message", "recent", "leaked"}
	sqlDeny  = []string{"credential"}
)

// The authorizer is the guard below the text check: a denied table is never read, however
// the query reaches it, and a table outside the list is refused.
func TestLocalSQLAuthorizerRefusesDeniedTables(t *testing.T) {
	ctx := context.Background()
	db := sqlDB(t, false)
	r := localSQL{}.Only(sqlAllow, sqlDeny)
	for _, q := range []string{
		"SELECT s.id, c.value FROM session s JOIN credential c",
		"SELECT id FROM session WHERE title = (SELECT value FROM credential)",
		"SELECT id FROM session WHERE EXISTS (SELECT 1 FROM credential)",
		"SELECT id FROM session WHERE id IN credential",
		"SELECT * FROM leaked",            // a view named in the list, over a denied table
		"SELECT count(*) FROM credential", // no column
		"WITH c AS (SELECT * FROM credential) SELECT * FROM c",
		"SELECT id FROM project", // not in the list
		"INSERT INTO session (id) VALUES ('ses_x')",
		"DELETE FROM session",
	} {
		if _, err := r.Query(ctx, db, q); !errors.Is(err, agent.ErrDenied) {
			t.Errorf("%s: want ErrDenied, got %v", q, err)
		}
	}
	for _, q := range []string{
		"SELECT s.id, m.text FROM session s JOIN message m ON m.session_id = s.id",
		"SELECT id FROM session WHERE id IN (SELECT session_id FROM message)",
		"SELECT * FROM recent",
		"WITH last AS (SELECT max(seq) AS n FROM message) SELECT n FROM last",
		"WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 3) SELECT count(*) AS n FROM c",
		"SELECT value FROM json_each('[1,2]')",
	} {
		if _, err := r.Query(ctx, db, q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
	if _, err := r.Query(ctx, db, "SELECT readfile('/etc/passwd')"); err == nil {
		t.Error("readfile() ran")
	}
	// Nothing was written.
	got, err := localSQL{}.Query(ctx, db, "SELECT count(*) AS n FROM session")
	if err != nil || got.Rows[0].Int("n") != 1 {
		t.Fatalf("the session table changed: %+v %v", got, err)
	}
}

func TestLocalSQLReadsTypesAndColumns(t *testing.T) {
	ctx := context.Background()
	db := sqlDB(t, false)
	r := localSQL{}.Only(sqlAllow, sqlDeny)
	out, err := r.Batch(ctx, db, []agent.Statement{
		{Query: "SELECT id, data, 1.5 AS f, NULL AS z FROM session WHERE id = ?", Args: []any{"ses_a"}},
		{Query: "SELECT id FROM message WHERE seq > ? ORDER BY seq", Args: []any{5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	row := out[0].Rows[0]
	if row.String("id") != "ses_a" || string(row["data"].([]byte)) != "\x00\xffA" || row.Float("f") != 1.5 || !row.Null("z") {
		t.Fatalf("row: %#v", row)
	}
	if strings.Join(out[0].Columns, ",") != "id,data,f,z" || len(out[1].Rows) != 0 {
		t.Fatalf("columns %v, rows %v", out[0].Columns, out[1].Rows)
	}
	cols, err := r.Columns(ctx, db, "message")
	if err != nil || strings.Join(cols, ",") != "id,session_id,seq,text" {
		t.Fatalf("columns: %v %v", cols, err)
	}
	if _, err := r.Columns(ctx, db, "credential"); !errors.Is(err, agent.ErrDenied) {
		t.Fatalf("the columns of a denied table: %v", err)
	}
	if _, err := r.Query(ctx, filepath.Join(filepath.Dir(db), "missing.db"), "SELECT 1"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("a missing database: %v", err)
	}
	if _, err := r.Query(ctx, "relative.db", "SELECT 1"); !errors.Is(err, agent.ErrDenied) {
		t.Fatalf("a relative path: %v", err)
	}
}

// A value longer than a statement may keep fails inside SQLite, before it is read whole.
func TestLocalSQLRefusesTooLargeValues(t *testing.T) {
	db := sqlDB(t, false)
	_, err := localSQL{}.Batch(context.Background(), db, []agent.Statement{{Query: "SELECT text FROM message", Limits: agent.SQLLimits{MaxBytes: 8}}})
	if !errors.Is(err, agent.ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	_, err = localSQL{}.Batch(context.Background(), db, []agent.Statement{{Query: "SELECT id FROM message", Limits: agent.SQLLimits{MaxRows: 1}}})
	if !errors.Is(err, agent.ErrTooLarge) {
		t.Fatalf("want ErrTooLarge for rows, got %v", err)
	}
}

// The agent keeps writing while hopsesh reads: the reader sees its WAL and never
// checkpoints it (only the agent's own connection does).
func TestLocalSQLReadsALiveWALWithoutCheckpointing(t *testing.T) {
	ctx := context.Background()
	db := sqlDB(t, true)
	agentConn, err := sql.Open("sqlite", "file:"+uriPath(db)+"?_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer agentConn.Close()
	if _, err := agentConn.Exec("INSERT INTO message VALUES ('msg_3', 'ses_a', 3, 'And now?')"); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(db + "-wal")
	if err != nil || before.Size() == 0 {
		t.Fatalf("the agent's WAL: %v", err)
	}
	for range 3 {
		got, err := localSQL{}.Query(ctx, db, "SELECT count(*) AS n FROM message")
		if err != nil || got.Rows[0].Int("n") != 3 {
			t.Fatalf("read %+v, %v", got, err)
		}
	}
	after, err := os.Stat(db + "-wal")
	if err != nil || after.Size() != before.Size() {
		t.Fatalf("the WAL changed under a reader: %d -> %v (%v)", before.Size(), after, err)
	}
}

func TestRetryBusy(t *testing.T) {
	saved := busyBackoff
	busyBackoff = []time.Duration{0, 0, 0}
	defer func() { busyBackoff = saved }()
	n := 0
	err := retryBusy(context.Background(), func() error {
		n++
		if n < 3 {
			return errBusy
		}
		return nil
	})
	if err != nil || n != 3 {
		t.Fatalf("%d tries, %v", n, err)
	}
	n = 0
	err = retryBusy(context.Background(), func() error { n++; return errBusy })
	if !errors.Is(err, errBusy) || n != 4 {
		t.Fatalf("a database busy every time: %d tries, %v", n, err)
	}
	n = 0
	err = retryBusy(context.Background(), func() error { n++; return agent.ErrDenied })
	if !errors.Is(err, agent.ErrDenied) || n != 1 {
		t.Fatalf("another error is not retried: %d tries, %v", n, err)
	}
}

// A module's confined host reads with the machine's reader, limited to its tables, and only
// the database files its Spec names; a snapshot has no reader.
func TestModuleHostSQLIsConfined(t *testing.T) {
	ctx := context.Background()
	db := sqlDB(t, false)
	root := filepath.Dir(db)
	spec := agent.Spec{ID: "dbagent", Name: "DB Agent", Roots: []agent.Root{{Name: "data", Default: map[string]string{"*": root}}},
		SQLFiles: []string{"{data}/agent.db"}, SQLTables: []string{"session", "message"}, SQLDenied: []string{"credential"}}
	in := agent.Install{Agent: "dbagent", Roots: map[string]string{"data": root}, MainRoot: "data"}
	m := &Machine{Name: "here", Local: true, Facts: Facts{OS: "darwin", Home: t.TempDir()}}
	h, err := m.For(ctx, spec, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := agent.HostSQL(h)
	if !ok {
		t.Fatal("a module that declares tables has no reader")
	}
	if got, err := s.Query(ctx, db, "SELECT title FROM session"); err != nil || got.Rows[0].String("title") != "Fix the build" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.Query(ctx, db, "SELECT * FROM recent"); !errors.Is(err, agent.ErrDenied) {
		t.Fatalf("a view outside the list: %v", err)
	}
	if _, err := h.FS().ReadFile(db, 1<<20); !errors.Is(err, agent.ErrDenied) {
		t.Fatalf("the database file must stay a secret: %v", err)
	}
	spec.SQLTables = nil
	h, _ = m.For(ctx, spec, in, nil)
	if _, ok := agent.HostSQL(h); ok {
		t.Fatal("a module without tables has a reader")
	}
	snap := NewSnapshot("there", Facts{OS: "linux", Home: "/home/u"}, nil)
	if snap.SQL() != nil {
		t.Fatal("a snapshot has a reader")
	}
	if _, ok := agent.HostSQL(FilesOnly(h)); ok {
		t.Fatal("FilesOnly has a reader")
	}
}

// A WAL database no program has open has no -shm file: SQLite cannot open it read only, so
// it is read as immutable while it stays that way, and read again when it changed.
func TestLocalSQLReadsAClosedWALDatabase(t *testing.T) {
	db := sqlDB(t, true)
	if _, err := os.Stat(db + "-shm"); err == nil {
		t.Skip("the -shm file outlived its connection")
	}
	got, err := localSQL{}.Query(context.Background(), db, "SELECT count(*) AS n FROM message")
	if err != nil || got.Rows[0].Int("n") != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, f := range []string{db + "-wal", db + "-shm"} {
		if _, err := os.Stat(f); err == nil {
			t.Fatalf("the read made %s", f)
		}
	}
	// A program opened it during the read: that read does not count.
	calls := 0
	err = whenClosed(db, localStat, nil, func(immutable bool) error {
		calls++
		if !immutable {
			return errCantOpen
		}
		return os.WriteFile(db+"-shm", nil, 0o600)
	})
	if !errors.Is(err, errBusy) || calls != 2 {
		t.Fatalf("a database opened during an immutable read: %v after %d reads", err, calls)
	}
	// An open WAL database is never read as immutable.
	err = whenClosed(db, localStat, localWAL, func(immutable bool) error {
		if immutable {
			t.Fatal("read as immutable while a -shm file is there")
		}
		return errCantOpen
	})
	if !errors.Is(err, errCantOpen) {
		t.Fatal(err)
	}
}
