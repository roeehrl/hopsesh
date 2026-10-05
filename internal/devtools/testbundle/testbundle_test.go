package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const repo = "../../.."

// The repository's own contributions and fixtures pass.
func TestRepositoryChecks(t *testing.T) {
	files, errs := check(repo)
	for _, e := range errs {
		t.Error(e)
	}
	kinds := map[string]bool{}
	for _, f := range files {
		kinds[f.kind] = true
	}
	for _, k := range []string{"doc", "schema", "fixture"} {
		if !kinds[k] {
			t.Errorf("no %s file in the bundle", k)
		}
	}
	if !slices.ContainsFunc(files, func(e entry) bool {
		return e.path == "claude/2.1.284/home/sessions/4242.json" && e.schema == "schemas/claude-registry.schema.json"
	}) {
		t.Error("Claude Code's registry fixture is not checked against the registry schema")
	}
}

// root makes a repository with the real schemas and README, and the given contributions
// under testbundle/.
func root(t *testing.T, files map[string]string) string {
	t.Helper()
	r := t.TempDir()
	src := filepath.Join(repo, "testbundle")
	ss, err := os.ReadDir(filepath.Join(src, "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	put := func(p string, b []byte) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range ss {
		b, err := os.ReadFile(filepath.Join(src, "schemas", s.Name()))
		if err != nil {
			t.Fatal(err)
		}
		put(filepath.Join(r, "testbundle", "schemas", s.Name()), b)
	}
	put(filepath.Join(r, "testbundle", "README.md"), []byte("readme"))
	for p, c := range files {
		put(filepath.Join(r, "testbundle", filepath.FromSlash(p)), []byte(c))
	}
	return r
}

const (
	hook = `{"session_id": "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01", "transcript_path": "/Users/alice/.claude/projects/-Users-alice-git-demo/0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01.jsonl",
	  "cwd": "/Users/alice/git/demo", "hook_event_name": "Notification", "message": "Claude needs your permission to use Bash"}`
	waiting = `{"pid": 4243, "sessionId": "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a03", "cwd": "/home/bob/git/demo", "status": "busy", "waitingFor": "permission"}`
	notify  = `{"type": "agent-turn-complete", "thread-id": "01a0fe1c-0000-7000-8000-000000000001", "cwd": "/home/carol/demo", "input-messages": ["hi"], "last-assistant-message": "done"}`
)

func TestContributions(t *testing.T) {
	good := map[string]string{
		"claude/2.1.284/hooks/Notification/permission-prompt.json": hook,
		"claude/2.1.284/registry/waiting-for-permission.json":      waiting,
		"claude/2.1.300/registry/plain.json":                       `{"pid": 77, "sessionId": "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a04", "status": "idle"}`,
		"codex/0.160.0/notify/turn-complete.json":                  notify,
	}
	files, errs := check(root(t, good))
	if len(errs) > 0 {
		t.Fatalf("good contributions refused: %q", errs)
	}
	for p := range good {
		if !slices.ContainsFunc(files, func(e entry) bool { return e.path == p && e.schema != "" }) {
			t.Errorf("%s is not in the bundle with its schema", p)
		}
	}

	for name, c := range map[string]struct {
		path, doc, want string
	}{
		"wrong event folder": {"claude/2.1.284/hooks/Stop/a.json", hook, "but the file is in hooks/Stop/"},
		"not a uuid":         {"claude/2.1.284/hooks/Notification/a.json", strings.Replace(hook, "0b6c6a8e-1d2f", "0b6c6a8e1d2f", 1), "session_id"},
		"real home":          {"claude/2.1.284/hooks/Notification/a.json", strings.ReplaceAll(hook, "alice", "jsmith"), `home folder of "jsmith"`},
		"windows home":       {"codex/0.160.0/notify/a.json", strings.Replace(notify, `/home/carol/demo`, `C:\\Users\\jsmith\\demo`, 1), `home folder of "jsmith"`},
		"e-mail":             {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"done"`, `"mail jane@corp.io"`, 1), "e-mail address at corp.io"},
		"token":              {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"done"`, `"sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAA"`, 1), "an API key"},
		"github token":       {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"done"`, `"ghp_abcdefghijklmnopqrstuvwxyz0123"`, 1), "a GitHub token"},
		"real address":       {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"done"`, `"ssh 100.91.4.7"`, 1), "the address 100.91.4.7"},
		"tailnet":            {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"done"`, `"on box.tail1234.ts.net"`, 1), "a tailnet name"},
		"home in a key":      {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"cwd"`, `"/Users/jsmith"`, 1), `home folder of "jsmith"`},
		"no pid":             {"claude/2.1.284/registry/a.json", `{"sessionId": "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a03"}`, "pid is required"},
		"file name":          {"claude/2.1.284/registry/Waiting.json", waiting, "a contribution is a <name>.json file"},
		"not json":           {"claude/2.1.284/registry/a.json", `{"pid": 4243,`, "not JSON"},
		"unknown slot":       {"claude/2.1.284/transcripts/a.json", waiting, "not a contribution folder for claude (hooks, registry)"},
		"slot of another":    {"codex/0.160.0/registry/a.json", waiting, "not a contribution folder for codex (notify)"},
		"unknown agent":      {"cursor/1.0.0/hooks/Stop/a.json", hook, "not an agent module's id"},
		"version folder":     {"claude/latest/registry/a.json", waiting, "not an agent version folder"},
		"stray file":         {"claude/2.1.284/hooks/Notification/.DS_Store", "x", "a contribution is a <name>.json file"},
		"too large":          {"claude/2.1.284/registry/a.json", strings.Replace(waiting, `"busy"`, `"`+strings.Repeat("x", maxPayload)+`"`, 1), "larger than"},
	} {
		_, errs := check(root(t, map[string]string{c.path: c.doc}))
		if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, c.want) }) {
			t.Errorf("%s: %q, want an error with %q", name, errs, c.want)
		}
	}
}

// A registry entry must read in hopsesh as its contributor meant.
func TestClaudeReads(t *testing.T) {
	if err := claudeReads([]byte(waiting)); err != nil {
		t.Fatal(err)
	}
	// hopsesh ignores an entry without a session id: a contribution that relied on it
	// being listed fails.
	if err := claudeReads([]byte(`{"pid": 4243, "sessionId": "", "status": "busy"}`)); err == nil {
		t.Error("an entry hopsesh skips was accepted")
	}
}

func TestScrubAllows(t *testing.T) {
	for _, s := range []string{
		"/home/u/git/demo", "/Users/alice/x", `C:\Users\bob\demo`, "alice@example.com", "x@sub.example.com",
		"127.0.0.1:8080", "100.64.0.2", "192.0.2.10", "version 2.1.284", "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01", "sk-short",
	} {
		if got := scrubString(s); len(got) > 0 {
			t.Errorf("%q: %q", s, got)
		}
	}
}

func TestTarball(t *testing.T) {
	when := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	files := []file{{path: "b/x.json", data: []byte("{}")}, {path: "bin/p/fakeagent", data: []byte("bin"), exec: true}, {path: "a.md", data: []byte("a")}}
	one, err := tarball("top", slices.Clone(files), when)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(files)
	two, err := tarball("top", files, when)
	if err != nil || !bytes.Equal(one, two) {
		t.Fatalf("not reproducible: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(one))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
		if !h.ModTime.Equal(when) || h.Uid != 0 || h.Uname != "" {
			t.Errorf("%s: %v %d %q", h.Name, h.ModTime, h.Uid, h.Uname)
		}
		if h.Name == "top/bin/p/fakeagent" && h.Mode != 0o755 {
			t.Errorf("the program is not executable: %o", h.Mode)
		}
	}
	want := []string{"top/", "top/a.md", "top/b/", "top/b/x.json", "top/bin/", "top/bin/p/", "top/bin/p/fakeagent"}
	if !slices.Equal(names, want) {
		t.Errorf("entries %q, want %q", names, want)
	}
}
