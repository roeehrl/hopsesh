package agent

import (
	"errors"
	"testing"
)

func TestCheckSQLQuery(t *testing.T) {
	allow := []string{"session_v2", "session_message", "session_part", "project", "kv"}
	deny := []string{"credential", "account", "control_account"}
	ok := []string{
		"SELECT id, title FROM session_v2 WHERE parent_id IS NULL ORDER BY time_updated DESC",
		"select * from SESSION_V2 s join session_message m on m.session_id = s.id where s.id = ?",
		"SELECT s.id, (SELECT count(*) FROM session_message m WHERE m.session_id = s.id) FROM session_v2 AS s",
		"SELECT * FROM session_v2, project WHERE session_v2.project_id = project.id",
		"SELECT * FROM main.session_v2 LEFT OUTER JOIN project p USING (id)",
		"WITH recent(id) AS (SELECT id FROM session_v2 LIMIT 5) SELECT * FROM recent JOIN session_part ON session_part.session_id = recent.id",
		"SELECT value FROM kv WHERE key LIKE 'models-dev:%' -- the catalog",
		"SELECT j.value FROM session_message, json_each(session_message.data) AS j",
		"SELECT replace(title, 'a', 'b') FROM session_v2;",
		"SELECT * FROM (SELECT id FROM session_v2) x",
		"SELECT * FROM (session_v2 JOIN project ON 1)",
		"SELECT a IS NOT DISTINCT FROM b FROM session_v2",
		"SELECT 'credential is a word in a string' FROM session_v2",
		"VALUES (1), (2)",
		"SELECT \"title\" FROM \"session_v2\" /* quoted */",
	}
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
}
