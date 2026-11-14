package agenttest_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

const agreeSchema = `
CREATE TABLE session_v2 (id TEXT PRIMARY KEY, title TEXT, parent_id TEXT, project_id TEXT, time_updated INTEGER, a INTEGER, b INTEGER);
CREATE TABLE session_message (id TEXT, session_id TEXT, data TEXT);
CREATE TABLE session_part (id TEXT, session_id TEXT);
CREATE TABLE project (id TEXT);
CREATE TABLE kv (key TEXT, value TEXT);
CREATE TABLE job (id TEXT);
CREATE TABLE credential (id TEXT, value TEXT);
INSERT INTO session_v2 VALUES ('ses_1', 'one', NULL, 'p1', 1, 1, 1);
INSERT INTO session_message VALUES ('msg_1', 'ses_1', '{"type":"text","text":"hi"}');
INSERT INTO session_part VALUES ('prt_1', 'ses_1');
INSERT INTO project VALUES ('p1');
INSERT INTO kv VALUES ('models-dev:x', '{}');
INSERT INTO job VALUES ('job-row');
INSERT INTO credential VALUES ('openai', 'sk-never-read');
`

// Every query the text check passes (sdk/agent/testdata/sql_ok.txt) also runs on the
// FakeHost's limited reader, so a module that passes its unit tests does not fail at
// runtime on the authorizer.
func TestSQLCheckAgreesWithAuthorizer(t *testing.T) {
	ctx := context.Background()
	fh := agenttest.NewFakeHost("/home/u")
	db := "/home/u/.local/share/oc/oc.db"
	fh.LoadSQL(t, db)
	if err := fh.ExecSQL(db, agreeSchema); err != nil {
		t.Fatal(err)
	}
	spec := agent.Spec{ID: "oc", Name: "OC", Roots: []agent.Root{{Name: "data", Default: map[string]string{"*": "~/.local/share/oc"}}},
		SQLFiles: []string{"{data}/oc.db"}, SQLTables: []string{"session_v2", "session_message", "session_part", "project", "kv"},
		SQLDenied: []string{"credential", "account", "control_account"}}
	h := agent.Confine(fh, spec, agent.DefaultInstall(spec, fh))
	sq, ok := agent.HostSQL(h)
	if !ok {
		t.Fatal("no SQL")
	}
	b, err := os.ReadFile("../testdata/sql_ok.txt")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, q := range strings.Split(string(b), "\n") {
		if q = strings.TrimSpace(q); q == "" || strings.HasPrefix(q, "#") {
			continue
		}
		n++
		args := make([]any, strings.Count(q, "?"))
		if _, err := sq.Query(ctx, db, q, args...); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
	if n < 10 {
		t.Fatalf("only %d queries in the list", n)
	}
	for _, read := range fh.SQLReads() {
		if read == "credential" || read == "job" {
			t.Fatalf("read %s", read)
		}
	}
}
