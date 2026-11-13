package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

// SQLHost is a Host that can read SQLite databases on its machine, for an agent that keeps
// its sessions in one (OpenCode). Other hosts do not implement it; modules find it with
// HostSQL.
type SQLHost interface {
	// SQL returns the machine's reader, or nil when it has none.
	SQL() SQL
}

// SQL reads a SQLite database on the machine, read only: no query writes, checkpoints or
// vacuums. db is the database file's absolute path. The confined Host a module gets lets it
// read only its Spec's SQLTables, from a database under its roots.
type SQL interface {
	// Query runs one SELECT and returns its rows, refusing more than the default limits.
	Query(ctx context.Context, db, q string, args ...any) (Rows, error)
	// Batch runs several SELECTs in one read transaction, so they see one snapshot of the
	// database (messages and their parts), and returns their rows in order.
	Batch(ctx context.Context, db string, stmts []Statement) ([]Rows, error)
	// Columns lists a table's columns, for a module's check of the schema it reads; a
	// missing table has none.
	Columns(ctx context.Context, db, table string) ([]string, error)
}

// SQLTableLimiter is an SQL that refuses reads of tables itself, below the query text
// (SQLite's authorizer): Confine hands it the module's tables.
type SQLTableLimiter interface {
	// Only returns an SQL that reads only the tables in allow and never those in deny.
	Only(allow, deny []string) SQL
}

// HostSQL returns h's SQL reader, when it has one.
func HostSQL(h Host) (SQL, bool) {
	sh, ok := h.(SQLHost)
	if !ok {
		return nil, false
	}
	s := sh.SQL()
	return s, s != nil
}

// Statement is one query of a Batch.
type Statement struct {
	Query string
	Args  []any
	// Limits are this query's guards; zero fields take the defaults.
	Limits SQLLimits
}

// SQLLimits guard how much of a database one query may bring into memory.
type SQLLimits struct {
	MaxRows  int
	MaxBytes int64 // the sum of every value's size: strings and blobs by length, others 8
}

// Default SQL limits: a long session's messages fit, a runaway query does not.
const (
	DefaultSQLMaxRows  = 100_000
	DefaultSQLMaxBytes = 64 << 20
)

// WithDefaults fills zero limits with the defaults.
func (l SQLLimits) WithDefaults() SQLLimits {
	if l.MaxRows <= 0 {
		l.MaxRows = DefaultSQLMaxRows
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultSQLMaxBytes
	}
	return l
}

// ErrTooLarge is a query whose rows went over its limits.
var ErrTooLarge = errors.New("more data than hopsesh reads at once")

// Rows are a query's rows, read into memory. Values are int64 (INTEGER), float64 (REAL),
// string (TEXT), []byte (BLOB) or nil (NULL), keyed by column name.
type Rows struct {
	Columns []string `json:"columns"`
	Rows    []Row    `json:"rows"`
}

// Row is one row of Rows: its values by column name.
type Row map[string]any

// String is a TEXT value ("" when NULL or not text; a BLOB is read as text).
func (r Row) String(col string) string {
	switch v := r[col].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	}
	return ""
}

// Int is an INTEGER value (0 when NULL; a whole REAL or a decimal TEXT is read too, as
// a remote reader's JSON gives them).
func (r Row) Int(col string) int64 {
	switch v := r[col].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
			return int64(v)
		}
	case string:
		var n int64
		if _, err := fmt.Sscan(v, &n); err == nil {
			return n
		}
	}
	return 0
}

// Float is a REAL value (0 when NULL).
func (r Row) Float(col string) float64 {
	switch v := r[col].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

// Null reports whether a column is NULL or missing.
func (r Row) Null(col string) bool { return r[col] == nil }

// ScanRows reads database/sql rows into Rows within limits, for a Host's SQL. It closes
// rs. Going over a limit is ErrTooLarge.
func ScanRows(rs *sql.Rows, lim SQLLimits) (Rows, error) {
	defer rs.Close()
	lim = lim.WithDefaults()
	cols, err := rs.Columns()
	if err != nil {
		return Rows{}, err
	}
	out := Rows{Columns: cols, Rows: []Row{}}
	var size int64
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rs.Next() {
		if len(out.Rows) >= lim.MaxRows {
			return Rows{}, fmt.Errorf("%w: over %d rows", ErrTooLarge, lim.MaxRows)
		}
		if err := rs.Scan(ptrs...); err != nil {
			return Rows{}, err
		}
		row := make(Row, len(cols))
		for i, c := range cols {
			v := normalSQLValue(vals[i])
			size += sqlValueSize(v)
			row[c] = v
		}
		if size > lim.MaxBytes {
			return Rows{}, fmt.Errorf("%w: over %d bytes", ErrTooLarge, lim.MaxBytes)
		}
		out.Rows = append(out.Rows, row)
	}
	return out, rs.Err()
}

// CheckRows applies limits to rows read another way (a remote reader's JSON).
func CheckRows(r Rows, lim SQLLimits) error {
	lim = lim.WithDefaults()
	if len(r.Rows) > lim.MaxRows {
		return fmt.Errorf("%w: over %d rows", ErrTooLarge, lim.MaxRows)
	}
	var size int64
	for _, row := range r.Rows {
		for _, v := range row {
			size += sqlValueSize(v)
		}
	}
	if size > lim.MaxBytes {
		return fmt.Errorf("%w: over %d bytes", ErrTooLarge, lim.MaxBytes)
	}
	return nil
}

func normalSQLValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return append([]byte(nil), x...)
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case float32:
		return float64(x)
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	return v
}

func sqlValueSize(v any) int64 {
	switch x := v.(type) {
	case string:
		return int64(len(x))
	case []byte:
		return int64(len(x))
	case nil:
		return 0
	}
	return 8
}
