package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/testkit"
)

// pull --to on a session already on this machine, in its own agent, changes its folder as
// hopsesh cd does: the same session (its id), not a copy.
func TestPullToOnThisMachineChangesTheFolder(t *testing.T) {
	home := t.TempDir()
	for k, v := range testkit.Env(home) {
		t.Setenv(k, v)
	}
	if err := testkit.DemoHome(home); err != nil {
		t.Fatal(err)
	}
	to, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	to = filepath.Join(to, "elsewhere")
	if err := os.MkdirAll(to, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) []byte {
		t.Helper()
		var out bytes.Buffer
		cmd := NewRoot(&out, all.Registry())
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("hopsesh %s: %v\n%s", strings.Join(args, " "), err, out.String())
		}
		return out.Bytes()
	}
	var plan struct {
		Plan struct {
			To string `json:"to"`
		} `json:"plan"`
		Entry struct {
			Session struct {
				Key struct {
					Session string `json:"session"`
				} `json:"key"`
			} `json:"session"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(run("pull", demoRef, "--to", to, "--dry-run", "--json"), &plan); err != nil || plan.Plan.To != to {
		t.Fatalf("pull --to here plans a folder change: %+v %v", plan, err)
	}
	var done struct {
		Session string `json:"session"`
		CWD     string `json:"cwd"`
		Journal string `json:"journal"`
	}
	if err := json.Unmarshal(run("pull", demoRef, "--to", to, "--yes", "--json"), &done); err != nil {
		t.Fatal(err)
	}
	if done.CWD != to || done.Journal == "" || !strings.HasSuffix(done.Session, plan.Entry.Session.Key.Session) {
		t.Fatalf("the same session, in the new folder: %+v", done)
	}
	quoted, _ := json.Marshal(to) // Windows paths come back with their backslashes escaped
	if ls := run("ls", "--agent", "claude", "--no-git", "--json"); !bytes.Contains(ls, append([]byte(`"cwd": `), quoted...)) {
		t.Fatalf("ls does not show the session in %s:\n%s", to, ls)
	}
}
