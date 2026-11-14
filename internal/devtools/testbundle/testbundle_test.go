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
		"codex/0.160.1/hooks/Notification/permission-prompt.json":  hook,
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
		"codex wrong event folder": {"codex/0.160.1/hooks/Stop/a.json", hook, "but the file is in hooks/Stop/"},
		"codex invalid session":    {"codex/0.160.1/hooks/Notification/a.json", strings.Replace(hook, "0b6c6a8e-1d2f", "0b6c6a8e1d2f", 1), "session_id"},
		"wrong event folder":       {"claude/2.1.284/hooks/Stop/a.json", hook, "but the file is in hooks/Stop/"},
		"not a uuid":               {"claude/2.1.284/hooks/Notification/a.json", strings.Replace(hook, "0b6c6a8e-1d2f", "0b6c6a8e1d2f", 1), "session_id"},
		"real home":                {"claude/2.1.284/hooks/Notification/a.json", strings.ReplaceAll(hook, "alice", "jsmith"), `home folder of "jsmith"`},
		"windows home":             {"codex/0.160.0/notify/a.json", strings.Replace(notify, `/home/carol/demo`, `C:\\Users\\jsmith\\demo`, 1), `home folder of "jsmith"`},
		"e-mail":                   {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"done"`, `"mail jane@corp.io"`, 1), "e-mail address at corp.io"},
		"token":                    {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"done"`, `"sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAA"`, 1), "an API key"},
		"github token":             {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"done"`, `"ghp_abcdefghijklmnopqrstuvwxyz0123"`, 1), "a GitHub token"},
		"real address":             {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"done"`, `"ssh 100.91.4.7"`, 1), "the address 100.91.4.7"},
		"tailnet":                  {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"done"`, `"on box.tail1234.ts.net"`, 1), "a tailnet name"},
		"home in a key":            {"codex/0.160.0/notify/a.json", strings.Replace(notify, `"cwd"`, `"/Users/jsmith"`, 1), `home folder of "jsmith"`},
		"no pid":                   {"claude/2.1.284/registry/a.json", `{"sessionId": "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a03"}`, "pid is required"},
		"file name":                {"claude/2.1.284/registry/Waiting.json", waiting, "a contribution is a <name>.json file"},
		"not json":                 {"claude/2.1.284/registry/a.json", `{"pid": 4243,`, "not JSON"},
		"unknown slot":             {"claude/2.1.284/transcripts/a.json", waiting, "not a contribution folder for claude (hooks, registry)"},
		"slot of another":          {"codex/0.160.0/registry/a.json", waiting, "not a contribution folder for codex (hooks, notify)"},
		"unknown agent":            {"cursor/1.0.0/hooks/Stop/a.json", hook, "not an agent module's id"},
		"version folder":           {"claude/latest/registry/a.json", waiting, "not an agent version folder"},
		"stray file":               {"claude/2.1.284/hooks/Notification/.DS_Store", "x", "a contribution is a <name>.json file"},
		"too large":                {"claude/2.1.284/registry/a.json", strings.Replace(waiting, `"busy"`, `"`+strings.Repeat("x", maxPayload)+`"`, 1), "larger than"},
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

// Capture folders name the capturer: Claude's temporary folder and a project folder named
// after a home are caught.
func TestScrubRefuses(t *testing.T) {
	for s, want := range map[string]string{
		"/tmp/claude-501/-Users-jsmith-git-demo/x":     "temporary folder",
		"/tmp/claude-1000/-home-jsmith-git-demo/x":     `home folder of "jsmith" in a folder name`,
		"/var/tmp/opencode/x":                          "temporary folder",
		"/private/tmp/x":                               "temporary folder",
		"~/.claude/projects/-Users-jsmith-git-x/a.txt": `home folder of "jsmith" in a folder name`,
	} {
		if got := scrubString(s); !slices.ContainsFunc(got, func(f string) bool { return strings.Contains(f, want) }) {
			t.Errorf("%q: %q, want %q", s, got, want)
		}
	}
}

func TestScrubAllows(t *testing.T) {
	for _, s := range []string{
		"/home/u/git/demo", "/Users/alice/x", `C:\Users\bob\demo`, "alice@example.com", "x@sub.example.com",
		"127.0.0.1:8080", "100.64.0.2", "192.0.2.10", "version 2.1.284", "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01", "sk-short",
		"/tmp/opencode", "-Users-alice-git-demo", "~/.claude/projects/-home-u-git-demo/x.jsonl",
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

// OpenCode exports are held to the transfer schema, each message type's members, and
// what OpenCode's import keeps.
func TestOpenCodeExports(t *testing.T) {
	plain, err := os.ReadFile(filepath.Join(repo, "testbundle", "opencode", "2.3.0", "export", "plain.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(plain)
	if _, errs := check(root(t, map[string]string{"opencode/2.3.0/export/plain.json": doc})); len(errs) > 0 {
		t.Fatalf("a good export refused: %q", errs)
	}
	for name, c := range map[string]struct{ doc, want string }{
		"no project":       {strings.Replace(doc, `"projectID"`, `"project"`, 1), "projectID is required"},
		"unknown type":     {strings.Replace(doc, `"type": "idle"`, `"type": "nap"`, 1), "$.messages[2].type"},
		"no prompt text":   {strings.Replace(doc, `"text": "Reply with exactly: hi there",`, ``, 1), "messages[0].text: is required"},
		"no agent":         {strings.Replace(doc, `"agent": "build",`, ``, 1), "messages[1].agent: is required"},
		"unknown member":   {strings.Replace(doc, `"text": "Reply with exactly: hi there",`, `"text": "Reply with exactly: hi there", "mood": "calm",`, 1), "messages[0].mood: OpenCode does not know this member and drops it on import"},
		"unknown in info":  {strings.Replace(doc, `"cost": 0,`, `"cost": 0, "hopsesh": true,`, 1), "hopsesh"},
		"unsettled reply":  {strings.Replace(doc, `"completed": 1794597379064`, `"x": 1`, 1), "settled replies only"},
		"bad outcome":      {strings.Replace(doc, `"outcome": "succeeded"`+"\n    }", `"outcome": "done"`+"\n    }", 1), `outcome: expected one of succeeded, failed, interrupted, got "done"`},
		"real home":        {strings.Replace(doc, `/home/u/git/demo`, `/Users/jsmith/git/demo`, 1), `home folder of "jsmith"`},
		"machine name":     {strings.Replace(doc, `hi there"`, `hi from studio.local"`, 1), "local network name"},
		"capture folder":   {strings.Replace(doc, `/home/u/git/demo`, `/private/tmp/capture/demo`, 1), "temporary folder"},
		"repeated message": {strings.Replace(doc, `msg_1d656dbfa001qYc8WjRlOipIl7`, `msg_1d655e1e1001hzS9xR19AT9he0`, 1), "is repeated"},
	} {
		if c.doc == doc {
			t.Fatalf("%s: the change did not apply", name)
		}
		_, errs := check(root(t, map[string]string{"opencode/2.3.0/export/a.json": c.doc}))
		if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, c.want) }) {
			t.Errorf("%s: %q, want an error with %q", name, errs, c.want)
		}
	}
}

// OpenCode's SQL fixtures must build a database whose JSON columns hold JSON.
func TestOpenCodeSQL(t *testing.T) {
	schema, err := os.ReadFile(filepath.Join(repo, "agents", "opencode", "testdata", "2.3.0", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	row := `INSERT INTO "project" ("id", "worktree", "time_created", "time_updated", "sandboxes") VALUES ('p', '/home/u/git/demo', 1, 1, '[]');
INSERT INTO "session_v2" ("id", "project_id", "slug", "directory", "version", "time_created", "time_updated") VALUES ('ses_x', 'p', 's', '/home/u/git/demo', '2.3.0', 1, 1);
INSERT INTO "session_message" ("id", "session_id", "type", "seq", "time_created", "time_updated", "data") VALUES ('msg_x', 'ses_x', 'user', 1, 1, 1, '%s');`
	if errs := checkOpenCodeSQL(map[string][]byte{"schema.sql": schema, "data.sql": []byte(strings.Replace(row, "%s", `{"text":"hi"}`, 1))}); len(errs) > 0 {
		t.Errorf("good rows refused: %q", errs)
	}
	if errs := checkOpenCodeSQL(map[string][]byte{"schema.sql": schema, "data.sql": []byte(strings.Replace(row, "%s", `{"text":`, 1))}); !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, "session_message.data of msg_x is not JSON") }) {
		t.Errorf("a message that is not JSON: %q", errs)
	}
	if errs := checkOpenCodeSQL(map[string][]byte{"schema.sql": schema, "data.sql": []byte("INSERT INTO nowhere VALUES (1);")}); len(errs) == 0 {
		t.Error("SQL that does not build was accepted")
	}
	if got := scrubText([]byte(`INSERT INTO t VALUES ('/Users/jsmith/x', 'it''s /home/u/ok');`), true); len(got) != 1 || !strings.Contains(got[0], "jsmith") {
		t.Errorf("scrub of SQL strings: %q", got)
	}
}
