package host

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// localSQL reads SQLite databases on this machine for agents that keep their sessions in
// one (OpenCode). It opens a database read only (immutable only while it has no WAL at
// all, see whenClosed), reads one batch in one transaction and closes the connection after
// every call, so no reader of hopsesh's holds a checkpoint back. It never writes,
// checkpoints or vacuums. Once limited (Only), SQLite's authorizer refuses reads of any
// table outside the module's list, and every read of a denied one, below the text check.
type localSQL struct {
	limited     bool
	allow, deny []string
}

func (s localSQL) Only(allow, deny []string) agent.SQL {
	return localSQL{limited: true, allow: allow, deny: deny}
}

func (s localSQL) Query(ctx context.Context, db, q string, args ...any) (agent.Rows, error) {
	out, err := s.Batch(ctx, db, []agent.Statement{{Query: q, Args: args}})
	if err != nil {
		return agent.Rows{}, err
	}
	return out[0], nil
}

func (s localSQL) Batch(ctx context.Context, db string, stmts []agent.Statement) ([]agent.Rows, error) {
	var out []agent.Rows
	err := retryBusy(ctx, func() error {
		return whenClosed(db, localStat, localWAL, func(immutable bool) error {
			var err error
			out, err = s.batch(ctx, db, stmts, immutable)
			return err
		})
	})
	return out, err
}

func (s localSQL) batch(ctx context.Context, db string, stmts []agent.Statement, immutable bool) ([]agent.Rows, error) {
	conn, done, err := openReadOnly(ctx, db, immutable)
	if err != nil {
		return nil, err
	}
	defer done()
	stored, err := storedNames(ctx, conn)
	if err != nil {
		return nil, err
	}
	if err := conn.Raw(func(dc any) error {
		r, ok := dc.(sqlite.AuthorizerRegisterer)
		if !ok {
			return errors.New("the SQLite driver has no authorizer")
		}
		return r.RegisterAuthorizer(func(action sqlite.AuthorizerActionCode, arg1, arg2, _, _ string) sqlite.AuthorizerReturnCode {
			if sqlAuthorized(int(action), arg1, arg2, stored, s.limited, s.allow, s.deny) {
				return sqlite.AuthorizerOK
			}
			return sqlite.AuthorizerDeny
		})
	}); err != nil {
		return nil, err
	}
	// No value longer than a statement may keep: SQLite refuses it before it is read whole.
	if _, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, int(min(longestValue(stmts), 1<<30))); err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, sqlError(err)
	}
	defer tx.Rollback()
	budget := agent.NewSQLBudget()
	out := make([]agent.Rows, 0, len(stmts))
	for _, st := range stmts {
		rs, err := tx.QueryContext(ctx, st.Query, st.Args...)
		if err != nil {
			return nil, sqlError(err)
		}
		r, err := budget.ScanRows(rs, st.Limits)
		if err != nil {
			return nil, sqlError(err)
		}
		out = append(out, r)
	}
	return out, sqlError(tx.Commit())
}

func (s localSQL) Columns(ctx context.Context, db, table string) ([]string, error) {
	if s.limited && (!containsFold(s.allow, table) || containsFold(s.deny, table)) {
		return nil, fmt.Errorf("%w: the table %s is not read", agent.ErrDenied, table)
	}
	var cols []string
	err := retryBusy(ctx, func() error {
		return whenClosed(db, localStat, localWAL, func(immutable bool) error {
			conn, done, err := openReadOnly(ctx, db, immutable)
			if err != nil {
				return err
			}
			defer done()
			rs, err := conn.QueryContext(ctx, "SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
			if err != nil {
				return sqlError(err)
			}
			r, err := agent.ScanRows(rs, agent.SQLLimits{})
			if err != nil {
				return sqlError(err)
			}
			cols = cols[:0]
			for _, row := range r.Rows {
				cols = append(cols, row.String("name"))
			}
			return nil
		})
	})
	return cols, err
}

// openReadOnly opens one connection to the database at db, read only (immutable: see
// whenClosed). done closes it.
func openReadOnly(ctx context.Context, db string, immutable bool) (*sql.Conn, func(), error) {
	if !filepath.IsAbs(db) {
		return nil, nil, fmt.Errorf("%w: %q is not a database path", agent.ErrDenied, db)
	}
	fi, err := os.Stat(db)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("%w: no database at %s", agent.ErrNotFound, db)
	}
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%w: %s is not a database file", agent.ErrDenied, db)
	}
	dsn := readOnlyDSN(db)
	if immutable {
		dsn += "&immutable=1"
	}
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, err
	}
	d.SetMaxOpenConns(1)
	conn, err := d.Conn(ctx)
	if err != nil {
		d.Close()
		return nil, nil, sqlError(err)
	}
	return conn, func() { conn.Close(); d.Close() }, nil
}

// readOnlyDSN is the database's URI, read only, with SQLite's own wait for a lock. The
// characters a URI gives a meaning to are escaped in the path.
func readOnlyDSN(p string) string {
	return "file:" + uriPath(p) + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)"
}

// uriPath is a file path as the path of a SQLite file: URI.
func uriPath(p string) string {
	u := strings.ReplaceAll(p, `\`, "/")
	if len(u) >= 2 && u[1] == ':' {
		u = "/" + u // file:/C:/x.db
	}
	return strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(u)
}

// storedNames are the database's own tables and views, read before the authorizer is in
// place: a read of any other name with no column is a WITH name.
func storedNames(ctx context.Context, conn *sql.Conn) ([]string, error) {
	rs, err := conn.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type IN ('table', 'view')")
	if err != nil {
		return nil, sqlError(err)
	}
	r, err := agent.ScanRows(rs, agent.SQLLimits{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		names = append(names, row.String("name"))
	}
	return names, nil
}

// SQLite's authorizer action codes, as every SQLite numbers them (sqlite3.h).
const (
	sqlRead        = 20
	sqlSelect      = 21
	sqlTransaction = 22
	sqlFunction    = 31
	sqlRecursive   = 33
)

// sqlAuthorized is the authorizer's answer: reading is allowed, nothing else; once
// limited, only reads of the allowed tables, the table-valued functions and WITH names,
// and never a denied table. Functions that reach files or load code are refused.
func sqlAuthorized(action int, arg1, arg2 string, stored []string, limited bool, allow, deny []string) bool {
	switch action {
	case sqlSelect, sqlTransaction, sqlRecursive:
		return true
	case sqlFunction:
		switch strings.ToLower(arg2) {
		case "load_extension", "readfile", "writefile", "edit", "fts3_tokenizer":
			return false
		}
		return true
	case sqlRead:
		if withName(stored, arg1, arg2) {
			return true // no table of the database
		}
		if containsFold(deny, arg1) {
			return false
		}
		return !limited || containsFold(allow, arg1) || containsFold(agent.SQLTableFuncs(), arg1)
	}
	return false
}

// withName reports whether a read is of a WITH name: no column, of a name that is no table
// or view of the database and not one of SQLite's own (sqlite_schema, pragma_*). A read of
// a real table with no column (SELECT count(*) FROM t) names a stored table.
func withName(stored []string, table, column string) bool {
	lower := strings.ToLower(table)
	return column == "" && !containsFold(stored, table) && !strings.HasPrefix(lower, "sqlite_") && !strings.HasPrefix(lower, "pragma_")
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// longestValue is the longest value any of the statements may keep.
func longestValue(stmts []agent.Statement) int64 {
	var n int64
	for _, st := range stmts {
		n = max(n, st.Limits.WithDefaults().MaxBytes)
	}
	return n
}

// errBusy is a database another program holds locked past SQLite's own wait.
var errBusy = errors.New("the database is busy")

// errCantOpen is a database SQLite cannot open read only: one in WAL mode whose -shm file
// is gone because no program has it open (the agent closed it cleanly).
var errCantOpen = errors.New("the database cannot be opened read only")

// fileState is what whenClosed compares: a file's size and time, or that it is missing.
type fileState struct {
	size    int64
	mod     time.Time
	missing bool
}

func localStat(p string) (fileState, error) {
	fi, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return fileState{missing: true}, nil
	}
	if err != nil {
		return fileState{}, err
	}
	return fileState{size: fi.Size(), mod: fi.ModTime()}, nil
}

// whenClosed reads the database read only. A WAL database that no program has open has no
// -shm file, and SQLite cannot then open it read only. With no -wal and no -shm, the
// database file holds everything, so it is read as immutable, never through a WAL; if any
// of the three changed by the end of the read (a program opened it meanwhile), the read
// counts as busy and is done again.
// isWAL, when given, tells a WAL database from its header without opening it, so a closed
// one is read as immutable from the start: some SQLite builds open it read only by making
// empty -wal and -shm files, which hopsesh leaves to the agent.
func whenClosed(db string, stat func(string) (fileState, error), isWAL func(string) bool, read func(immutable bool) error) error {
	before, ok := closedState(db, stat)
	if !ok || isWAL == nil || !isWAL(db) {
		err := read(false)
		if !errors.Is(err, errCantOpen) {
			return err
		}
		if before, ok = closedState(db, stat); !ok {
			return err
		}
	}
	if err := read(true); err != nil {
		return err
	}
	if after, ok := closedState(db, stat); !ok || after != before {
		return fmt.Errorf("%w: it was opened while hopsesh read it", errBusy)
	}
	return nil
}

// localWAL reads a database file's header: format versions 2 are WAL mode.
func localWAL(db string) bool {
	f, err := os.Open(db)
	if err != nil {
		return false
	}
	defer f.Close()
	var h [20]byte
	if _, err := f.ReadAt(h[:], 0); err != nil {
		return false
	}
	return string(h[:16]) == "SQLite format 3\x00" && h[18] == 2 && h[19] == 2
}

// closedState is the database file's state while it has neither a -wal nor a -shm file.
func closedState(db string, stat func(string) (fileState, error)) (fileState, bool) {
	for _, side := range []string{db + "-wal", db + "-shm"} {
		if st, err := stat(side); err != nil || !st.missing {
			return fileState{}, false
		}
	}
	st, err := stat(db)
	return st, err == nil && !st.missing
}

// sqlError names SQLite's refusals the way the SDK does: a read the authorizer refused
// is ErrDenied, a value over the length limit ErrTooLarge, a lock errBusy.
func sqlError(err error) error {
	if err == nil {
		return nil
	}
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() & 0xff {
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
			return fmt.Errorf("%w: %v", errBusy, err)
		case sqlite3.SQLITE_AUTH:
			return fmt.Errorf("%w: %v", agent.ErrDenied, err)
		case sqlite3.SQLITE_TOOBIG:
			return fmt.Errorf("%w: %v", agent.ErrTooLarge, err)
		case sqlite3.SQLITE_CANTOPEN:
			return fmt.Errorf("%w: %v", errCantOpen, err)
		}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not authorized"):
		return fmt.Errorf("%w: %v", agent.ErrDenied, err)
	case strings.Contains(msg, "too big"):
		return fmt.Errorf("%w: %v", agent.ErrTooLarge, err)
	case strings.Contains(msg, "database is locked"):
		return fmt.Errorf("%w: %v", errBusy, err)
	case strings.Contains(msg, "unable to open database file"):
		return fmt.Errorf("%w: %v", errCantOpen, err)
	}
	return err
}

// busyBackoff are the waits before each retry of a read that found the database busy.
var busyBackoff = []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond}

// retryBusy runs read again, at most three more times, while the database is busy.
func retryBusy(ctx context.Context, read func() error) error {
	err := read()
	for _, wait := range busyBackoff {
		if !errors.Is(err, errBusy) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		err = read()
	}
	return err
}
