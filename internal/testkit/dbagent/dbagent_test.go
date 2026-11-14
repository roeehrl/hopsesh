package dbagent

import (
	"path"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

// The stand-in the core's tests use keeps the SDK's contract: the conformance suite runs it
// on a fake machine, its program writing that machine's database.
func TestConformance(t *testing.T) {
	m := New()
	agenttest.Run(t, m, func(t *testing.T) *agenttest.FakeHost {
		fh := agenttest.NewFakeHost("/home/u")
		fh.AddBinary("dbagent", "dbagent 1.0.0")
		// The fake machine's paths are slashed on every OS (on Windows too).
		data := "/home/u/.local/share/dbagent"
		fh.LoadSQL(t, path.Join(data, "db.sqlite"))
		real, _ := fh.SQLFile(path.Join(data, "db.sqlite"))
		was := dbPath
		dbPath = func(d string) string {
			if path.Clean(filepath.ToSlash(d)) == data {
				return real
			}
			t.Fatalf("the program wrote the database of %s", d)
			return ""
		}
		t.Cleanup(func() { dbPath = was })
		if err := Seed(data, "ses_a", "/home/u/git/demo", "Fix the build",
			Message{ID: "msg_1", Role: "user", Text: "Why does the build fail?"},
			Message{ID: "msg_2", Role: "assistant", Text: "A missing import."}); err != nil {
			t.Fatal(err)
		}
		return fh
	})
}
