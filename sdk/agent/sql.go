package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
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
// read only its Spec's SQLTables, from the files its SQLFiles name, and gives each call
// at most DefaultSQLTimeout (less when ctx ends sooner).
//
// A database file that is not there is ErrNotInstalled: ErrNotFound is left to the module
// for a session the database does not hold, since the core takes it to mean the session
// was deleted. A read the machine's tools cannot do (another machine's WAL database that
// no program has open, which its SQLite cannot open read only) is ErrUnsupported: the
// module then reads with its agent's own program.
type SQL interface {
	// Query runs one SELECT and returns its rows, refusing more than the default limits.
	Query(ctx context.Context, db, q string, args ...any) (Rows, error)
	// Batch runs several SELECTs in one read transaction, so they see one snapshot of the
	// database (messages and their parts), and returns their rows in order. Each
	// statement keeps to its Limits, and the whole batch to DefaultSQLBatchMaxBytes
	// (SQLBudget).
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

// SQLLimits guard how much of a database one query may bring into memory. MaxBytes
// counts the values kept; a Host's reader also caps one value's length (SQLite's
// SQLITE_LIMIT_LENGTH) so a single huge value fails before it is read whole.
type SQLLimits struct {
	MaxRows  int
	MaxBytes int64 // the sum of every value's size: strings and blobs by length, others 8
}

// Default SQL limits: a long session's messages fit, a runaway query does not.
const (
	DefaultSQLMaxRows       = 100_000
	DefaultSQLMaxBytes      = 64 << 20
	DefaultSQLBatchMaxBytes = 128 << 20        // every statement of one Batch together
	DefaultSQLTimeout       = 30 * time.Second // one Query, Batch or Columns call
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
// a remote reader's JSON gives them, and any other text is 0).
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
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// Float is a REAL value (0 when NULL; an INTEGER or a decimal TEXT is read too, as Int
// does).
func (r Row) Float(col string) float64 {
	switch v := r[col].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return f
		}
	}
	return 0
}

// Null reports whether a column is NULL or missing.
func (r Row) Null(col string) bool { return r[col] == nil }

// ScanRows reads database/sql rows into Rows within limits, for a Host's SQL. It closes
// rs. Going over a limit is ErrTooLarge; two columns of one name (SELECT a.*, b.*) are an
// error, since a Row keeps one value per name.
func ScanRows(rs *sql.Rows, lim SQLLimits) (Rows, error) {
	return (&SQLBudget{left: math.MaxInt64}).ScanRows(rs, lim)
}

// CheckRows applies limits to rows read another way (a remote reader's JSON), whose
// Columns the reader fills in the query's order, repeats included.
func CheckRows(r Rows, lim SQLLimits) error {
	return (&SQLBudget{left: math.MaxInt64}).CheckRows(r, lim)
}

// SQLBudget caps the bytes one Batch brings into memory across its statements, on top of
// each statement's own limits. A Host's SQL makes one per Batch.
type SQLBudget struct{ left int64 }

// NewSQLBudget is a budget of DefaultSQLBatchMaxBytes.
func NewSQLBudget() *SQLBudget { return &SQLBudget{left: DefaultSQLBatchMaxBytes} }

// ScanRows is ScanRows, also spending the budget.
func (b *SQLBudget) ScanRows(rs *sql.Rows, lim SQLLimits) (Rows, error) {
	defer rs.Close()
	lim = lim.WithDefaults()
	cols, err := rs.Columns()
	if err != nil {
		return Rows{}, err
	}
	if err := uniqueSQLColumns(cols); err != nil {
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
			n := sqlValueSize(v)
			if err := b.spend(&size, n, lim); err != nil {
				return Rows{}, err
			}
			row[c] = v
		}
		out.Rows = append(out.Rows, row)
	}
	return out, rs.Err()
}

// CheckRows is CheckRows, also spending the budget.
func (b *SQLBudget) CheckRows(r Rows, lim SQLLimits) error {
	lim = lim.WithDefaults()
	if err := uniqueSQLColumns(r.Columns); err != nil {
		return err
	}
	if len(r.Rows) > lim.MaxRows {
		return fmt.Errorf("%w: over %d rows", ErrTooLarge, lim.MaxRows)
	}
	var size int64
	for _, row := range r.Rows {
		for _, v := range row {
			if err := b.spend(&size, sqlValueSize(v), lim); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *SQLBudget) spend(size *int64, n int64, lim SQLLimits) error {
	*size += n
	b.left -= n
	if *size > lim.MaxBytes {
		return fmt.Errorf("%w: over %d bytes", ErrTooLarge, lim.MaxBytes)
	}
	if b.left < 0 {
		return fmt.Errorf("%w: a batch over %d bytes", ErrTooLarge, DefaultSQLBatchMaxBytes)
	}
	return nil
}

func uniqueSQLColumns(cols []string) error {
	seen := make(map[string]bool, len(cols))
	for _, c := range cols {
		if seen[c] {
			return fmt.Errorf("the query returns two columns named %q; name each one (AS)", c)
		}
		seen[c] = true
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
