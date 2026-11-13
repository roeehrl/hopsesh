package agent

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestCheckSQLQuery(t *testing.T) {
	allow := []string{"session_v2", "session_message", "session_part", "project", "kv"}
	deny := []string{"credential", "account", "control_account"}
	ok := sqlTestQueries(t, "testdata/sql_ok.txt")
	for _, q := range ok {
		if err := CheckSQLQuery(q, allow, deny); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
	refused := []string{
		"",
		"DELETE FROM session_v2",
		"INSERT INTO session_v2 VALUES (1)",
		"UPDATE session_v2 SET title = 'x'",
		"REPLACE INTO session_v2 VALUES (1)",
		"WITH x AS (SELECT 1) DELETE FROM session_v2",
		"PRAGMA table_info(credential)",
		"SELECT * FROM session_v2; DROP TABLE session_v2",
		"ATTACH DATABASE '/tmp/x' AS other",
		"SELECT * FROM credential",
		"SELECT * FROM \"credential\"",
		"SELECT * FROM [Account]",
		"SELECT * FROM session_v2 WHERE id IN (SELECT id FROM account)",
		"SELECT * FROM session_v2, credential",
		"SELECT * FROM session_v2 JOIN control_account ON 1",
		"SELECT * FROM job",
		"SELECT * FROM session_v2 s, job j",
		"SELECT * FROM (job)",
		"SELECT * FROM other.session_v2",
		"SELECT * FROM pragma_table_info('session_v2')",
		"SELECT * FROM sqlite_master",
		"SELECT load_extension('x')",
		"SELECT readfile('/etc/passwd')",
		"SELECT * FROM session_v2 WHERE title = 'unterminated",
		"SELECT * FROM session_v2 /* unterminated",
		"BEGIN",
		// SQLite reads a quoted string where a table belongs as that table.
		"SELECT * FROM 'credential'",
		"SELECT * FROM session_v2, 'credential'",
		"SELECT * FROM 'sqlite_master'",
		"SELECT * FROM 'job'",
		"SELECT * FROM main.'job'",
		"SELECT * FROM session_v2 JOIN 'job' ON 1",
		// A WITH inside the query, whose name would let the real table through.
		"SELECT * FROM (WITH job AS (SELECT 1) SELECT * FROM job) x, job",
		"SELECT * FROM session_v2 WHERE id IN (WITH job AS (SELECT 1) SELECT 1)",
		// expr IN table reads that table.
		"SELECT * FROM session_v2 WHERE id IN sqlite_sequence",
		"SELECT * FROM session_v2 WHERE id IN main.job",
		"SELECT * FROM session_v2 WHERE id NOT IN 'credential'",
		"SELECT * FROM session_v2 WHERE id IN 'job'",
		"SELECT * FROM session_v2 WHERE id IN pragma_table_info('kv')",
		"SELECT * FROM session_v2 WHERE id IN other.session_v2",
	}
	for _, q := range refused {
		if err := CheckSQLQuery(q, allow, deny); !errors.Is(err, ErrDenied) {
			t.Errorf("%q must be refused, got %v", q, err)
		}
	}
}

func TestRowValues(t *testing.T) {
	r := Row{"n": int64(7), "f": 2.0, "s": "x", "b": []byte("y"), "z": nil, "j": "12"}
	if r.Int("n") != 7 || r.Int("f") != 2 || r.Int("j") != 12 || r.Int("s") != 0 || r.Int("z") != 0 {
		t.Fatalf("Int: %d %d %d", r.Int("n"), r.Int("f"), r.Int("j"))
	}
	if r.String("s") != "x" || r.String("b") != "y" || r.String("n") != "" || r.Float("n") != 7 || !r.Null("z") || !r.Null("missing") {
		t.Fatal("String, Float or Null")
	}
	rows := Rows{Rows: []Row{{"a": "12345"}, {"a": "678"}}}
	if err := CheckRows(rows, SQLLimits{MaxRows: 2, MaxBytes: 8}); err != nil {
		t.Fatal(err)
	}
	if err := CheckRows(rows, SQLLimits{MaxRows: 1}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("rows over the limit: %v", err)
	}
	if err := CheckRows(rows, SQLLimits{MaxBytes: 7}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("bytes over the limit: %v", err)
	}
	// Text is a number only when all of it is one.
	odd := Row{"a": "12abc", "b": "0x10", "c": " 7", "d": "2.5", "e": "-3", "f": "1e400"}
	if odd.Int("a") != 0 || odd.Int("b") != 0 || odd.Int("c") != 0 || odd.Int("d") != 0 || odd.Int("e") != -3 {
		t.Fatalf("Int of text: %d %d %d %d %d", odd.Int("a"), odd.Int("b"), odd.Int("c"), odd.Int("d"), odd.Int("e"))
	}
	if odd.Float("d") != 2.5 || odd.Float("e") != -3 || odd.Float("a") != 0 || odd.Float("f") != 0 {
		t.Fatalf("Float of text: %v %v %v %v", odd.Float("d"), odd.Float("e"), odd.Float("a"), odd.Float("f"))
	}
	// A Row keeps one value per name, so repeated columns are refused.
	if err := CheckRows(Rows{Columns: []string{"id", "title", "id"}}, SQLLimits{}); err == nil {
		t.Fatal("two columns named id must be refused")
	}
	// One budget across a batch's statements.
	budget := &SQLBudget{left: 8}
	if err := budget.CheckRows(rows, SQLLimits{}); err != nil {
		t.Fatal(err)
	}
	if err := budget.CheckRows(rows, SQLLimits{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a batch over its budget: %v", err)
	}
}

// sqlTestQueries reads a file of queries, one per line ('#' starts a comment line).
func sqlTestQueries(t *testing.T, file string) []string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}
