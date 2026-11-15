package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/opencode"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/internal/testkit/fakeopencode"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"

	_ "modernc.org/sqlite"
)

// Tests against the installed OpenCode (HOPSESH_REAL_AGENTS=1; the nightly job installs a
// pinned 2.x on its runners, the drift probe the latest). No model is called. Every
// folder OpenCode uses (home, data, state, cache, config) is the test's own, and
// OpenCode runs only through a guard that refuses any command that could start its
// background service: everything but --version, `service status` and `debug paths` needs
// --standalone. A test fails if a background service was registered anyway.

// realOC is the installed OpenCode behind its guard.
type realOC struct {
	bin, version, log string
	root              string
}

var ocVersion = regexp.MustCompile(`\d+\.\d+\.\d+`)

// realOpenCode prepares the installed opencode for a test, or skips it.
func realOpenCode(t *testing.T) realOC {
	t.Helper()
	real := installedOpenCode(t)
	if runtime.GOOS == "windows" {
		t.Skip("the guard is a shell script")
	}
	root := t.TempDir()
	testkit.UnsetAgentEnv(t)
	for k, d := range map[string]string{"HOME": "home", "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state", "XDG_CACHE_HOME": "cache", "XDG_CONFIG_HOME": "config", "TMPDIR": "tmp"} {
		p := filepath.Join(root, d)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(k, p)
	}
	t.Setenv("OPENCODE_DISABLE_AUTOUPDATE", "1")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "opencode.log")
	guard := ocGuard(real, log)
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(guard), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := exec.Command(filepath.Join(bin, "opencode"), "--version").Output()
	if err != nil {
		t.Fatalf("opencode --version: %v", err)
	}
	v := ocVersion.FindString(string(out))
	if v == "" {
		t.Fatalf("opencode --version printed no version: %q", out)
	}
	if c, ok := agent.CompareVersions(v, "2.1.0"); !ok || c < 0 {
		t.Skipf("opencode %s is older than the 2.1.0 hopsesh reads", v)
	}
	t.Logf("opencode %s (%s)", v, real)
	oc := realOC{bin: filepath.Join(bin, "opencode"), version: v, log: log, root: root}
	t.Cleanup(func() {
		b, _ := os.ReadFile(log)
		if strings.Contains(string(b), "REFUSED ") {
			t.Errorf("hopsesh ran OpenCode without --standalone:\n%s", b)
		}
		_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() && strings.HasPrefix(fi.Name(), "service") && strings.HasSuffix(fi.Name(), ".json") && strings.Contains(p, "state") {
				t.Errorf("OpenCode's background service was started (%s)", p)
			}
			return nil
		})
	})
	return oc
}

// installedOpenCode is the installed opencode, or a skip without HOPSESH_REAL_AGENTS=1.
var installedOpenCode = func(t *testing.T) string { return realAgents(t, "opencode") }

// ocGuard is the guard in front of the installed opencode: every call is logged, and one
// that could start OpenCode's background service (any but these, without --standalone)
// is refused.
var ocGuard = func(real, log string) string {
	return `#!/bin/sh
printf '%s\n' "$*" >> ` + shellQuote(log) + `
case "$*" in
  --version|-v|"service status"|"debug paths"*) ;;
  *) case " $* " in *" --standalone "*) ;; *) echo "guard: refused opencode $*" >&2; echo "REFUSED $*" >> ` + shellQuote(log) + `; exit 97 ;; esac ;;
esac
exec ` + shellQuote(real) + ` "$@"
`
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// at is a location whose OpenCode is the installed one.
func (oc realOC) at(l location) agent.Install {
	l.m.Facts.Binaries["opencode"] = agent.BinaryFact{Path: oc.bin, Version: "opencode v" + oc.version}
	in := ocInstall(l)
	in.Version, in.Binary = oc.version, oc.bin
	for _, r := range in.Roots {
		_ = os.MkdirAll(r, 0o700)
	}
	return in
}

// run runs the installed opencode (through its guard) on a location's folders.
func (oc realOC) run(t *testing.T, in agent.Install, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(oc.bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+filepath.Dir(in.Root("data")), "XDG_STATE_HOME="+filepath.Dir(in.Root("state")))
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if x, ok := err.(*exec.ExitError); ok {
		code = x.ExitCode()
	} else if err != nil {
		t.Fatalf("opencode %v: %v", args, err)
	}
	return o.String(), e.String(), code
}

// importFixture imports a captured 2.3.0 export, its folder made dir, and returns its id.
func (oc realOC) importFixture(t *testing.T, in agent.Install, name, dir string) agent.SessionID {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "agents", "opencode", "testdata", "2.3.0", name))
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", jsonText(dir)))
	f := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := oc.run(t, in, dir, "session", "import", f, "--directory", dir, "--standalone")
	id := regexp.MustCompile(`Imported session: (ses_\w+)`).FindStringSubmatch(out)
	if code != 0 || id == nil {
		t.Fatalf("import %s: %d %s %s", name, code, out, errOut)
	}
	return agent.SessionID(id[1])
}

// export is OpenCode's own export of a session ({info, messages}), or nil when it has none.
func (oc realOC) export(t *testing.T, in agent.Install, dir string, id agent.SessionID) map[string]json.RawMessage {
	t.Helper()
	out, _, code := oc.run(t, in, dir, "session", "export", string(id), "--standalone")
	if code != 0 {
		return nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("export of %s: %v", id, err)
	}
	return doc
}

// The schema hopsesh reads is there: the module lists a database the installed OpenCode
// made (its schema gate passes), every API call hopsesh makes is in OpenCode's OpenAPI
// document, and migrations newer than the stand-in's are reported. With
// $HOPSESH_DRIFT_SNAPSHOTS the migration ids and the OpenAPI document are written there
// for the drift review, which compares them week to week.
func TestRealOpenCodeSchemaAndAPI(t *testing.T) {
	oc := realOpenCode(t)
	l := newLocation(t, "here", oc.root)
	in := oc.at(l)
	if _, errOut, code := oc.run(t, in, l.repo, "session", "list", "--format", "json", "--standalone"); code != 0 {
		t.Fatalf("session list: %d %s", code, errOut)
	}
	m := opencode.New()
	if sessions := listAgent(t, l, m, in); len(sessions) != 0 {
		t.Fatalf("a new database lists %d sessions", len(sessions))
	}
	db := filepath.Join(in.Root("data"), "opencode.db")
	migrations := migrationIDs(t, db)
	known := fixtureMigrations(t)
	var newer []string
	for _, id := range migrations {
		if !known[id] {
			newer = append(newer, id)
		}
	}
	t.Logf("%d migrations, the latest %s; not in the stand-in's schema: %v", len(migrations), migrations[len(migrations)-1], newer)

	out, errOut, code := oc.run(t, in, l.repo, "api", "GET", "/openapi.json", "--standalone")
	if code != 0 {
		t.Fatalf("api GET /openapi.json: %d %s", code, errOut)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("OpenAPI document: %v", err)
	}
	param := regexp.MustCompile(`\{[^}]*\}`)
	have := map[string]bool{}
	for p, methods := range doc.Paths {
		for method := range methods {
			have[strings.ToUpper(method)+" "+param.ReplaceAllString(p, "{}")] = true
		}
	}
	for _, call := range []string{"GET /api/session", "GET /api/session/active", "GET /api/info", "GET /api/plugin", "PATCH /api/session/{}",
		"GET /api/session/{}/message", "POST /api/session/{}/interrupt", "POST /api/session/{}/move", "POST /api/session/{}/fork"} {
		if !have[call] {
			t.Errorf("OpenCode %s has no %s, which hopsesh calls", oc.version, call)
		}
	}
	if dir := os.Getenv("HOPSESH_DRIFT_SNAPSHOTS"); dir != "" {
		pretty, _ := json.MarshalIndent(json.RawMessage(out), "", "  ")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "opencode-openapi.json"), append(pretty, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "opencode-migrations.txt"), []byte(strings.Join(migrations, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// migrationIDs are the migrations a database has, oldest first (a database the test's own
// OpenCode made, read only).
func migrationIDs(t *testing.T, db string) []string {
	t.Helper()
	conn, err := openReadOnly(db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.Query("SELECT id FROM migration ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		t.Fatal("the database has no migrations")
	}
	return out
}

// fixtureMigrations are the migrations of the stand-in's 2.3.0 schema.
func fixtureMigrations(t *testing.T) map[string]bool {
	t.Helper()
	f := filepath.Join(t.TempDir(), "standin.db")
	db, err := fakeopencode.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	out := map[string]bool{}
	for _, id := range migrationIDs(t, f) {
		out[id] = true
	}
	return out
}

// The stand-in plays what hopsesh relies on as the installed OpenCode does: each command
// runs on its own copy of the same data, and their exit codes and the first line of
// their answers match (folders and new ids made alike).
func TestRealOpenCodeMatchesStandIn(t *testing.T) {
	oc := realOpenCode(t)
	self, _ := os.Executable()
	standIn := filepath.Join(oc.root, "standin", "opencode")
	if err := os.MkdirAll(filepath.Dir(standIn), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, standIn); err != nil {
		t.Fatal(err)
	}
	sides := map[string]struct {
		bin string
		l   location
	}{}
	for _, name := range []string{"real", "standin"} {
		l := newLocation(t, name, oc.root)
		bin := oc.bin
		if name == "standin" {
			bin = standIn
		}
		sides[name] = struct {
			bin string
			l   location
		}{bin, l}
		in := ocInstall(l)
		for _, r := range in.Roots {
			_ = os.MkdirAll(r, 0o700)
		}
	}
	fixture := func(l location) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "agents", "opencode", "testdata", "2.3.0", "export-02.json"))
		if err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(l.m.Facts.Home, "export-02.json")
		if err := os.WriteFile(f, []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", jsonText(l.repo))), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	const sid = "ses_e29a8e62bffehroVT7xbmzgU1q"
	const missing = "ses_e29a3ffe0ffeNoSuchSession0"
	cases := []struct {
		name string
		args func(l location) []string
	}{
		{"import", func(l location) []string {
			return []string{"session", "import", fixture(l), "--directory", l.repo, "--standalone"}
		}},
		{"import again", func(l location) []string {
			return []string{"session", "import", fixture(l), "--directory", l.repo, "--standalone"}
		}},
		{"import into a missing folder", func(l location) []string {
			return []string{"session", "import", fixture(l), "--directory", filepath.Join(l.repo, "nothere"), "--standalone"}
		}},
		{"export missing", func(location) []string { return []string{"session", "export", missing, "--standalone"} }},
		{"api get missing", func(location) []string { return []string{"api", "GET", "/api/session/" + missing, "--standalone"} }},
		{"api rename", func(location) []string {
			return []string{"api", "PATCH", "/api/session/" + sid, "-d", `{"title":"renamed by the check"}`, "--standalone"}
		}},
		{"api active", func(location) []string { return []string{"api", "GET", "/api/session/active", "--standalone"} }},
		{"api interrupt idle", func(location) []string {
			return []string{"api", "POST", "/api/session/" + sid + "/interrupt", "--standalone"}
		}},
		{"auth list", func(location) []string { return []string{"auth", "list", "--format", "json", "--standalone"} }},
		{"delete", func(location) []string { return []string{"session", "delete", sid, "--standalone"} }},
		{"delete again", func(location) []string { return []string{"session", "delete", sid, "--standalone"} }},
		{"service status", func(location) []string { return []string{"service", "status"} }},
	}
	norm := func(l location, s string) string {
		s = strings.ReplaceAll(s, l.m.Facts.Home, "$HOME")
		s = regexp.MustCompile(`\b1[789]\d{11}\b`).ReplaceAllString(s, "<time>")
		line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
		return line
	}
	for _, c := range cases {
		var got [2]string
		for i, name := range []string{"real", "standin"} {
			side := sides[name]
			in := ocInstall(side.l)
			cmd := exec.Command(side.bin, c.args(side.l)...)
			cmd.Dir = side.l.repo
			cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+filepath.Dir(in.Root("data")), "XDG_STATE_HOME="+filepath.Dir(in.Root("state")), "FAKE_OPENCODE_SERVICE=")
			var o, e bytes.Buffer
			cmd.Stdout, cmd.Stderr = &o, &e
			err := cmd.Run()
			code := 0
			if x, ok := err.(*exec.ExitError); ok {
				code = x.ExitCode()
			}
			got[i] = strings.Join([]string{"exit " + strconv.Itoa(code), norm(side.l, o.String()), norm(side.l, e.String())}, " | ")
		}
		if got[0] != got[1] {
			t.Errorf("%s:\n  opencode %s: %s\n  stand-in:      %s", c.name, oc.version, got[0], got[1])
		} else {
			t.Logf("%s: %s", c.name, got[0])
		}
	}
}

// A captured session imported into the installed OpenCode is listed, previewed, read (as
// the stand-in's import of the same export reads), renamed, moved to another data folder
// and back out by undo; a Claude Code session continues into it and undo deletes that
// one; a folder change is refused with the service stopped, and the sign-in probe reads
// no provider. Every change is OpenCode's own, checked with its export.
func TestRealOpenCodeLifecycle(t *testing.T) {
	oc := realOpenCode(t)
	ctx := context.Background()
	box, here := newLocation(t, "box", oc.root), newLocation(t, "here", oc.root)
	boxIn, hereIn := oc.at(box), oc.at(here)
	m := opencode.New()
	id := oc.importFixture(t, boxIn, "export-02.json", box.repo)
	src := oc.export(t, boxIn, box.repo, id)
	var srcMsgs []json.RawMessage
	_ = json.Unmarshal(src["messages"], &srcMsgs)

	s := findRouteSession(t, box, m, boxIn, agent.SessionKey{Agent: "opencode", Session: id})
	if s.CWD != box.repo || s.Size != int64(len(srcMsgs)) {
		t.Fatalf("listed: %+v (the export has %d messages)", s, len(srcMsgs))
	}
	h := mustHost(t, box, m, boxIn)
	if p, err := m.Preview(ctx, h, boxIn, s, 3); err != nil || p.Messages() == 0 {
		t.Fatalf("preview: %+v %v", p, err)
	}
	seg := readAll(t, box, m, boxIn, s)

	// The stand-in's import of the same export reads the same.
	sl := newLocation(t, "standin", oc.root)
	slIn := ocInstall(sl)
	b, _ := os.ReadFile(filepath.Join("..", "..", "agents", "opencode", "testdata", "2.3.0", "export-02.json"))
	if _, err := fakeopencode.Import(filepath.Join(slIn.Root("data"), "opencode.db"), []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", jsonText(box.repo))), box.repo, time.Now()); err != nil {
		t.Fatal(err)
	}
	if want := readAll(t, sl, m, slIn, findRouteSession(t, sl, m, slIn, s.Key)); !sameNodes(seg, want) {
		t.Errorf("the session reads otherwise from OpenCode %s's database than from the stand-in's", oc.version)
	}

	if err := m.Rename(ctx, h, boxIn, s, "Renamed by hopsesh"); err != nil {
		t.Fatal(err)
	}
	var info struct{ Title string }
	_ = json.Unmarshal(oc.export(t, boxIn, box.repo, id)["info"], &info)
	if info.Title != "Renamed by hopsesh" {
		t.Fatalf("OpenCode's title after the rename: %q", info.Title)
	}

	// Moved to here's data folder, then undone.
	s = findRouteSession(t, box, m, boxIn, s.Key)
	in := move.Input{Source: move.Side{Machine: box.m, Module: m, Install: boxIn}, Session: s, Target: move.Side{Machine: here.m, Module: m, Install: hereIn}}
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil || len(p.Blockers) > 0 {
		t.Fatalf("plan: %v %v", err, p.Blockers)
	}
	env := move.Env{StateDir: t.TempDir()}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	moved := oc.export(t, hereIn, here.repo, p.Placement.Key.Session)
	var movedMsgs []json.RawMessage
	_ = json.Unmarshal(moved["messages"], &movedMsgs)
	if moved == nil || len(movedMsgs) != len(srcMsgs) {
		t.Fatalf("the moved session in OpenCode: %d of %d messages", len(movedMsgs), len(srcMsgs))
	}
	j, err := journal.Load(env.StateDir, res.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Undo(ctx, ocReach(m, box, here), false); err != nil {
		t.Fatal(err)
	}
	if oc.export(t, hereIn, here.repo, p.Placement.Key.Session) != nil {
		t.Fatal("undo left the moved session in OpenCode")
	}

	// A Claude Code session continued into OpenCode, then undone.
	seed(t, box)
	csrc := list(t, box)[sid]
	cin := move.Input{Source: move.Side{Machine: box.m, Module: claude.New(), Install: box.in}, Session: csrc, Target: move.Side{Machine: here.m, Module: m, Install: hereIn}}
	cp, err := move.Build(ctx, cin, move.Options{TargetDir: here.repo})
	if err != nil || len(cp.Blockers) > 0 {
		t.Fatalf("plan: %v %v", err, cp.Blockers)
	}
	cres, err := move.Apply(ctx, cp, cin, env)
	if err != nil {
		t.Fatal(err)
	}
	written := oc.export(t, hereIn, here.repo, cp.Placement.Key.Session)
	if written == nil || !strings.Contains(string(written["messages"]), "PLUM-7") {
		t.Fatal("OpenCode's export of the continued session lacks its history")
	}
	cj, err := journal.Load(env.StateDir, cres.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := cj.Undo(ctx, ocReach(m, here), false); err != nil {
		t.Fatal(err)
	}
	if oc.export(t, hereIn, here.repo, cp.Placement.Key.Session) != nil {
		t.Fatal("undo left the continued session in OpenCode")
	}

	// A folder change needs the running service, which hopsesh never starts.
	s = findRouteSession(t, box, m, boxIn, s.Key)
	other := filepath.Join(filepath.Dir(box.repo), "elsewhere")
	_ = os.MkdirAll(other, 0o700)
	if cd, err := m.PlanChangeDir(ctx, h, boxIn, s, other); err == nil && len(cd.Blockers) == 0 {
		t.Fatalf("cd with the service stopped: %+v", cd)
	}
	if a, err := m.Account(ctx, h, boxIn); err != nil || a.Key != "" {
		t.Fatalf("account: %+v %v", a, err)
	}
}

// sameNodes reports whether two readings have the same kinds, actors and texts in order.
func sameNodes(a, b ir.Segment) bool {
	sig := func(s ir.Segment) []string {
		var out []string
		for _, n := range s.Nodes {
			out = append(out, string(n.Kind)+"/"+string(n.Actor)+"/"+n.Text)
		}
		return out
	}
	x, y := sig(a), sig(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// openReadOnly opens a SQLite database the test's own OpenCode made, read only.
func openReadOnly(file string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+filepath.ToSlash(file)+"?mode=ro")
}
