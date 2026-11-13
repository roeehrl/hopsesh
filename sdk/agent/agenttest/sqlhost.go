package agenttest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// LoadSQL makes a SQLite database at p on the fake machine from SQL files run in order
// (a schema, then rows): a real database in a temporary folder, which the Host's SQL reads
// and ExecSQL changes. p itself holds a placeholder in the fake filesystem, so it exists.
func (h *FakeHost) LoadSQL(t testing.TB, p string, files ...string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "fake.db")
	h.mu.Lock()
	if h.dbs == nil {
		h.dbs = map[string]string{}
	}
	h.dbs[path.Clean(p)] = file
	h.mu.Unlock()
	h.Put(p, []byte("SQLite format 3\x00"), time.Now())
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.ExecSQL(p, string(b)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
}

// ExecSQL changes a database made by LoadSQL, as the agent's own program does: Programs
// call it so List sees what a fake import wrote.
func (h *FakeHost) ExecSQL(p, q string, args ...any) error {
	file, ok := h.dbFile(p)
	if !ok {
		return fmt.Errorf("agenttest: no database at %s", p)
	}
	db, err := sql.Open("sqlite", "file:"+file+"?_pragma=busy_timeout(2000)")
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(q, args...)
	return err
}

// SQLReads are the tables the Host's SQL has read, in order (one entry per column read;
// refused reads are left out): a spy for tests that a table is never read.
func (h *FakeHost) SQLReads() []string {
	h.sqlMu.Lock()
	defer h.sqlMu.Unlock()
	return append([]string(nil), h.sqlReads...)
}

func (h *FakeHost) dbFile(p string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.dbs[path.Clean(p)]
	return f, ok
}

// SQL makes FakeHost an agent.SQLHost. Like the core's local reader it opens the database
// read only, runs a batch in one transaction and, once limited (SQLTableLimiter), refuses
// reads of other tables in SQLite's authorizer.
func (h *FakeHost) SQL() agent.SQL { return fakeSQL{h: h} }

type fakeSQL struct {
	h           *FakeHost
	limited     bool
	allow, deny []string
}

func (s fakeSQL) Only(allow, deny []string) agent.SQL {
	return fakeSQL{h: s.h, limited: true, allow: allow, deny: deny}
}

func (s fakeSQL) Query(ctx context.Context, db, q string, args ...any) (agent.Rows, error) {
	out, err := s.Batch(ctx, db, []agent.Statement{{Query: q, Args: args}})
	if err != nil {
		return agent.Rows{}, err
	}
	return out[0], nil
}

func (s fakeSQL) Batch(ctx context.Context, db string, stmts []agent.Statement) ([]agent.Rows, error) {
	conn, done, err := s.open(ctx, db)
	if err != nil {
		return nil, err
	}
	defer done()
	if err := conn.Raw(func(dc any) error {
		r, ok := dc.(sqlite.AuthorizerRegisterer)
		if !ok {
			return fmt.Errorf("agenttest: the SQLite driver has no authorizer")
		}
		return r.RegisterAuthorizer(s.authorize)
	}); err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var out []agent.Rows
	for _, st := range stmts {
		rs, err := tx.QueryContext(ctx, st.Query, st.Args...)
		if err != nil {
			return nil, err
		}
		r, err := agent.ScanRows(rs, st.Limits)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, tx.Commit()
}

func (s fakeSQL) Columns(ctx context.Context, db, table string) ([]string, error) {
	if s.limited && (!containsFold(s.allow, table) || containsFold(s.deny, table)) {
		return nil, fmt.Errorf("%w: the table %s is not read", agent.ErrDenied, table)
	}
	conn, done, err := s.open(ctx, db)
	if err != nil {
		return nil, err
	}
	defer done()
	rs, err := conn.QueryContext(ctx, "SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		return nil, err
	}
	r, err := agent.ScanRows(rs, agent.SQLLimits{})
	if err != nil {
		return nil, err
	}
	var cols []string
	for _, row := range r.Rows {
		cols = append(cols, row.String("name"))
	}
	return cols, nil
}

func (s fakeSQL) open(ctx context.Context, db string) (*sql.Conn, func(), error) {
	file, ok := s.h.dbFile(db)
	if !ok {
		return nil, nil, fmt.Errorf("%w: no database at %s", agent.ErrNotFound, db)
	}
	d, err := sql.Open("sqlite", "file:"+file+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, nil, err
	}
	d.SetMaxOpenConns(1)
	conn, err := d.Conn(ctx)
	if err != nil {
		d.Close()
		return nil, nil, err
	}
	return conn, func() { conn.Close(); d.Close() }, nil
}

// authorize allows reading, and only the allowed tables once limited.
func (s fakeSQL) authorize(action sqlite.AuthorizerActionCode, arg1, arg2, _, _ string) sqlite.AuthorizerReturnCode {
	switch action {
	case sqlite.AuthSelect, sqlite.AuthTransaction, sqlite.AuthRecursive:
		return sqlite.AuthorizerOK
	case sqlite.AuthFunction:
		switch strings.ToLower(arg2) {
		case "load_extension", "readfile", "writefile", "edit", "fts3_tokenizer":
			return sqlite.AuthorizerDeny
		}
		return sqlite.AuthorizerOK
	case sqlite.AuthRead:
		if s.limited && (!containsFold(s.allow, arg1) || containsFold(s.deny, arg1)) {
			return sqlite.AuthorizerDeny
		}
		s.h.sqlMu.Lock()
		s.h.sqlReads = append(s.h.sqlReads, arg1)
		s.h.sqlMu.Unlock()
		return sqlite.AuthorizerOK
	}
	return sqlite.AuthorizerDeny
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
