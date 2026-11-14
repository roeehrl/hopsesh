package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

var (
	ctx      = context.Background()
	timeZero = time.Unix(0, 0)
)

func TestConformance(t *testing.T) {
	agenttest.Run(t, New(), func(t *testing.T) *agenttest.FakeHost {
		// The stand-in OpenCode plays the commands that change sessions (the store's
		// cycle: its import refuses the synthetic session's message of a newer type, as
		// OpenCode 2.3.0 does, so the captured sessions only).
		fh, _ := newMachine(t, setup{secrets: true})
		return fh
	})
}

func TestSpecDeniesCredentialTables(t *testing.T) {
	s := New().Spec()
	for _, table := range []string{"credential", "account", "control_account", "account_state", "permission", "workspace"} {
		if !slices.Contains(s.SQLDenied, table) || slices.Contains(s.SQLTables, table) {
			t.Errorf("%s must be denied and not allowed", table)
		}
	}
	if s.Stability != agent.Experimental || !slices.Equal(s.Tested, []string{"2.2", "2.3"}) {
		t.Errorf("stability %s, tested %v", s.Stability, s.Tested)
	}
	if s.MainRoot().Name != rootData || !slices.Contains(s.SQLFiles, "{data}/opencode*.db") || !slices.Contains(s.SQLFiles, "{db}") {
		t.Errorf("main root %s, SQL files %v", s.MainRoot().Name, s.SQLFiles)
	}
	for _, sec := range []string{"{config}/opencode.json", "{config}/config.json", "{data}/auth.json", "{state}/service*.json"} {
		if !slices.Contains(s.Secrets, sec) {
			t.Errorf("%s is not a secret", sec)
		}
	}
	if len(s.Kinds) != 1 || s.Kinds[0].ID != kindArchived {
		t.Errorf("kinds %v", s.Kinds)
	}
}

// The confined host refuses the credential tables whatever the module asks.
func TestConfinedHostRefusesDeniedTables(t *testing.T) {
	fh, _ := newMachine(t, setup{secrets: true})
	_, h, _ := open(t, fh)
	sq, ok := agent.HostSQL(h)
	if !ok {
		t.Fatal("no SQL")
	}
	for _, q := range []string{"SELECT value FROM credential", "SELECT * FROM account", "SELECT s.id FROM session_v2 s JOIN permission p ON 1"} {
		if _, err := sq.Query(ctx, dbPath, q); !errors.Is(err, agent.ErrDenied) {
			t.Errorf("%s: %v", q, err)
		}
	}
	if _, err := sq.Query(ctx, home+"/other.db", "SELECT id FROM session_v2"); !errors.Is(err, agent.ErrDenied) {
		t.Errorf("another database: %v", err)
	}
	// The database is a secret for file reads.
	if _, err := h.FS().ReadFile(dbPath, 1<<20); !errors.Is(err, agent.ErrDenied) {
		t.Errorf("reading the database file: %v", err)
	}
}

func TestDetectRoots(t *testing.T) {
	fh := agenttest.NewFakeHost(home)
	fh.SetEnv("XDG_DATA_HOME", "/x/data")
	fh.SetEnv("XDG_STATE_HOME", "/x/state")
	fh.SetEnv("XDG_CONFIG_HOME", "/x/config")
	fh.Put("/x/data/opencode/opencode.db", []byte("x"), timeZero)
	m := New()
	in, err := m.Detect(ctx, fh)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{rootData: "/x/data/opencode", rootState: "/x/state/opencode", rootConfig: "/x/config/opencode"}
	for k, v := range want {
		if in.Root(k) != v {
			t.Errorf("%s root %q, want %q", k, in.Root(k), v)
		}
	}
	if !in.Present || in.Main() != "/x/data/opencode" || in.Root(rootDB) != "" {
		t.Errorf("install %+v", in)
	}
	// OPENCODE_CONFIG_DIR replaces the config folder.
	fh.SetEnv("OPENCODE_CONFIG_DIR", "/cfg")
	if in, _ = m.Detect(ctx, fh); in.Root(rootConfig) != "/cfg" {
		t.Errorf("config root %q", in.Root(rootConfig))
	}
	// OPENCODE_DB names the database, relative to the data root as OpenCode resolves it.
	for env, want := range map[string]string{"custom.db": "/x/data/opencode/custom.db", "/elsewhere/oc.db": "/elsewhere/oc.db", ":memory:": ""} {
		fh.SetEnv("OPENCODE_DB", env)
		in, _ = m.Detect(ctx, fh)
		if in.Root(rootDB) != want {
			t.Errorf("OPENCODE_DB=%s: db %q, want %q", env, in.Root(rootDB), want)
		}
	}
}

func TestDetectDesktop(t *testing.T) {
	t.Setenv("HOPSESH_APP_RUNTIMES", "on") // a fake host's /Applications: nothing real is found or run
	fh := agenttest.NewFakeHost(home)
	fh.Put("/Applications/OpenCode.app/Contents/Info.plist", []byte("<dict><key>CFBundleIdentifier</key>\n<string>ai.opencode.desktop</string><key>CFBundleShortVersionString</key>\n<string>2.2.7</string></dict>"), timeZero)
	fh.Put("/Applications/OpenCode.app/Contents/Resources/opencode-cli.version", []byte("2.2.7\n"), timeZero)
	in, _ := New().Detect(ctx, fh)
	if in.Desktop != "" {
		t.Fatalf("a Linux machine has no OpenCode desktop: %+v", in)
	}
	darwin := &osHost{fh, "darwin"}
	in, _ = New().Detect(ctx, darwin)
	if in.Desktop != "/Applications/OpenCode.app" || in.DesktopVersion != "2.2.7" || in.DesktopEngine != "2.2.7" {
		t.Fatalf("desktop %q %q %q", in.Desktop, in.DesktopVersion, in.DesktopEngine)
	}
}

// osHost is a fake machine of another OS (paths stay POSIX).
type osHost struct {
	*agenttest.FakeHost
	os string
}

func (h *osHost) Facts() agent.Facts {
	f := h.FakeHost.Facts()
	f.OS = h.os
	f.Arch = "arm64"
	return f
}

func TestListRootSessionsOnly(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	l, err := m.List(ctx, h, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Sessions) != 12 || len(l.Errors) != 0 {
		t.Fatalf("%d sessions, %d errors; want 12 root sessions", len(l.Sessions), len(l.Errors))
	}
	for _, s := range l.Sessions {
		if s.Key.Session == sesChild || s.Key.Session == sesImpChild {
			t.Errorf("the child session %s is listed", s.Key.Session)
		}
	}
	if s := summaryFor(t, l, sesSubagent); s.Subagents != 1 {
		t.Errorf("subagent session counts %d children", s.Subagents)
	}
	if s := summaryFor(t, l, sesImported); s.Subagents != 1 {
		t.Errorf("imported parent counts %d children", s.Subagents)
	}
	if s := summaryFor(t, l, sesRenamed); s.Subagents != 0 {
		t.Errorf("a session without children counts %d", s.Subagents)
	}
	// Newest first, as OpenCode lists them.
	for i := 1; i < len(l.Sessions); i++ {
		if l.Sessions[i].LastActivity.After(l.Sessions[i-1].LastActivity) {
			t.Fatal("not newest first")
		}
	}
}

func TestListArchivedKindAndForks(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	l, _ := m.List(ctx, h, in)
	for _, sid := range []string{sesArchived, sesForkPre} {
		if s := summaryFor(t, l, sid); !slices.Equal(s.Kinds, []string{kindArchived}) {
			t.Errorf("%s kinds %v", sid, s.Kinds)
		}
	}
	if s := summaryFor(t, l, sesRenamed); len(s.Kinds) != 0 {
		t.Errorf("an active session has kinds %v", s.Kinds)
	}
	for _, sid := range []string{sesForkAll, sesForkPre} {
		if s := summaryFor(t, l, sid); s.NativeParent != sesFiles {
			t.Errorf("fork %s parent %q", sid, s.NativeParent)
		}
	}
	if s := summaryFor(t, l, sesFiles); s.NativeParent != "" {
		t.Errorf("the original has a parent %q", s.NativeParent)
	}
}

func TestListSummaryFields(t *testing.T) {
	fh, _ := newMachine(t, setup{synthetic: true})
	m, h, in := open(t, fh)
	l, _ := m.List(ctx, h, in)
	s := summaryFor(t, l, sesRenamed)
	if s.Title != "renamed by api" || s.TitleSource != "generated" || s.CWD != "/home/u/git/demo" || s.AgentVersion != "2.3.0" {
		t.Errorf("summary %+v", s)
	}
	if s.Size != 3 || !s.LastActivity.Equal(time.UnixMilli(1794597524919)) || s.Key.Agent != id {
		t.Errorf("size %d, last activity %v", s.Size, s.LastActivity)
	}
	if s.Path != dataDir+"/hopsesh/opencode/sessions/"+sesRenamed+".jsonl" {
		t.Errorf("anchor %s", s.Path)
	}
	if s.LastPrompt != "Reply with exactly: hi there" {
		t.Errorf("last prompt %q", s.LastPrompt)
	}
	if s := summaryFor(t, l, sesLong); s.LastPrompt != "What number did I ask you to remember? Reply with just it." {
		t.Errorf("last prompt of a longer session %q", s.LastPrompt)
	}
	// The move put the shell session in another folder.
	if s := summaryFor(t, l, sesShell); s.CWD != "/home/u/git/other" {
		t.Errorf("moved session cwd %q", s.CWD)
	}
	// An untitled session (OpenCode's fallback title) is named by its first prompt, as
	// the person typed it; its size counts settled messages only.
	mixed := summaryFor(t, l, sesMixed)
	if mixed.Title != "Look at the screenshot and fix the build" || mixed.TitleSource != "prompt" || mixed.Size != 6 {
		t.Errorf("untitled session: %q (%s), size %d", mixed.Title, mixed.TitleSource, mixed.Size)
	}
}

func TestListSummaryTitles(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	// An untitled session with several prompts is named by its first.
	setRow(t, fh, sesLong, "title = NULL")
	// A title an older hopsesh labelled shows without its label.
	setRow(t, fh, sesFiles, "title = ?", "↪ continued in Codex on laptop · file tools")
	// An untitled session whose first prompt names nothing (a command) stays untitled.
	setRow(t, fh, sesShell, "title = ?", "New session - 2026-11-13T12:00:00.000Z")
	if err := fh.ExecSQL(dbPath, `UPDATE session_message SET data = json_set(data, '$.text', '/init') WHERE session_id = ? AND type = 'user' AND seq = (SELECT min(seq) FROM session_message WHERE session_id = ? AND type = 'user')`, sesShell, sesShell); err != nil {
		t.Fatal(err)
	}
	// A long last prompt is clipped.
	long := strings.Repeat("word ", 100)
	if err := fh.ExecSQL(dbPath, `INSERT INTO session_message VALUES ('msg_zzlong', ?, 'user', 999, 1, 1, json_object('time', json_object('created', 1), 'text', ?))`, sesRenamed, long); err != nil {
		t.Fatal(err)
	}
	l, err := m.List(ctx, h, in)
	if err != nil {
		t.Fatal(err)
	}
	if s := summaryFor(t, l, sesLong); s.Title != "Remember the number 7. Reply 'noted'." || s.TitleSource != "prompt" {
		t.Errorf("untitled session: %q (%s)", s.Title, s.TitleSource)
	}
	if s := summaryFor(t, l, sesFiles); s.Title != "file tools" || s.LegacyLabel == nil || s.LegacyLabel.AgentName != "Codex" {
		t.Errorf("labelled title: %q %+v", s.Title, s.LegacyLabel)
	}
	if s := summaryFor(t, l, sesShell); s.Title != "" || s.TitleSource != "" {
		t.Errorf("untitled, no usable prompt: %q (%s)", s.Title, s.TitleSource)
	}
	if s := summaryFor(t, l, sesRenamed); len([]rune(s.LastPrompt)) != 200 || !strings.HasSuffix(s.LastPrompt, "…") {
		t.Errorf("long last prompt: %d runes", len([]rune(s.LastPrompt)))
	}
}

func TestListWithoutDatabase(t *testing.T) {
	fh, f := newMachine(t, setup{noDB: true})
	m, h, in := open(t, fh)
	l, err := m.List(ctx, h, in)
	if err != nil || len(l.Sessions) != 0 {
		t.Fatalf("no database: %d sessions, %v", len(l.Sessions), err)
	}
	if c := f.commands(); len(c) != 0 {
		t.Errorf("listing with no database ran %v", c)
	}
}

// schemaHost builds a database from the fixture schema changed by edit.
func schemaHost(t *testing.T, edit func(string) string, extra ...string) *agenttest.FakeHost {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixture, "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	schema := filepath.Join(dir, "schema.sql")
	// A checkout on Windows may end the fixture's lines with CRLF; the edits look for LF.
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	if err := os.WriteFile(schema, []byte(edit(text)), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []string{schema}
	for i, e := range extra {
		p := filepath.Join(dir, "extra"+string(rune('a'+i))+".sql")
		if err := os.WriteFile(p, []byte(e), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	fh := agenttest.NewFakeHost(home)
	fh.AddBinary(binName, "opencode v2.3.0")
	fh.LoadSQL(t, dbPath, files...)
	return fh
}

func TestSchemaGateMissingColumn(t *testing.T) {
	fh := schemaHost(t, func(s string) string {
		s = strings.Replace(s, "`time_suspended` integer,", "", 1)
		i := strings.Index(s, "CREATE INDEX `session_v2_time_suspended_idx`")
		j := i + strings.Index(s[i:], ";\n")
		return s[:i] + s[j+2:]
	}, `INSERT INTO migration VALUES ('20261201000000_drop_suspended', 1);`)
	m, h, in := open(t, fh)
	_, err := m.List(ctx, h, in)
	var se *schemaError
	if !errors.As(err, &se) || !errors.Is(err, agent.ErrUnsupported) {
		t.Fatalf("a missing column must refuse the listing, got %v", err)
	}
	if !strings.Contains(err.Error(), "time_suspended") || !strings.Contains(err.Error(), "20261201000000_drop_suspended") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
}

// Every read is gated, not only the listing.
func TestSchemaGateGuardsEveryRead(t *testing.T) {
	fh := schemaHost(t, func(s string) string {
		return strings.Replace(s, "`time_viewed` integer,", "", 1)
	}, `INSERT INTO migration VALUES ('20261201000001_drop_viewed', 1);`)
	m, h, in := open(t, fh)
	s := agent.Summary{Key: sessionKey(in, sesFiles)}
	var se *schemaError
	if _, err := m.Read(ctx, h, in, s, ir.Cursor{}); !errors.As(err, &se) {
		t.Errorf("Read: %v", err)
	}
	if _, err := m.Preview(ctx, h, in, s, 2); !errors.As(err, &se) {
		t.Errorf("Preview: %v", err)
	}
	if _, err := m.SessionStamp(ctx, h, in, sesFiles); !errors.As(err, &se) {
		t.Errorf("SessionStamp: %v", err)
	}
	if _, err := m.Materialize(ctx, h, in, s, agent.BundleFile{Role: agent.RoleMain}); !errors.As(err, &se) {
		t.Errorf("Materialize: %v", err)
	}
	// Capacity is an estimate: an unknown schema makes it unknown, not an error.
	if c, err := m.ContextCapacity(ctx, h, in, &s); err != nil || !c.Unknown {
		t.Errorf("ContextCapacity: %+v %v", c, err)
	}
}

// A database last opened by OpenCode 2.1.x or 2.2.0 has no session_part table and no
// time_busy column: it is read all the same.
func TestSchemaOfOpenCode21(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	if err := fh.ExecSQL(dbPath, `DELETE FROM session_part; DROP TABLE session_part; ALTER TABLE session_v2 DROP COLUMN time_busy; `+
		`DELETE FROM migration WHERE id >= '20261029002625'`); err != nil {
		t.Fatal(err)
	}
	m, h, in := open(t, fh)
	l, err := m.List(ctx, h, in)
	if err != nil || len(l.Sessions) != 12 {
		t.Fatalf("listing a 2.1 database: %d sessions, %v", len(l.Sessions), err)
	}
	for _, s := range l.Sessions {
		if _, err := m.Read(ctx, h, in, s, ir.Cursor{}); err != nil {
			t.Errorf("Read %s: %v", s.Key.Session, err)
		}
		if _, err := m.Preview(ctx, h, in, s, 2); err != nil {
			t.Errorf("Preview %s: %v", s.Key.Session, err)
		}
	}
	setRow(t, fh, sesFiles, "time_suspended = 1794597600000")
	live, err := m.Live(ctx, h, in, []agent.SessionID{sesFiles, sesRenamed})
	if err != nil || live[sesFiles].State != agent.Live || live[sesRenamed].State != agent.Ended {
		t.Fatalf("Live on a 2.1 database: %+v %v", live, err)
	}
}

// The gate checks a database once per migration: OpenCode changes its schema only by
// migrations.
func TestSchemaGateRunsOncePerMigration(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	if err := fh.ExecSQL(dbPath, `INSERT INTO migration VALUES ('20991231000000_gate_cache_test', 1)`); err != nil {
		t.Fatal(err)
	}
	m := New()
	in, _ := m.Detect(ctx, fh)
	var n int
	h := agent.Confine(countingHost{fh, &n}, m.Spec(), in)
	if _, err := m.List(ctx, h, in); err != nil || n == 0 {
		t.Fatalf("first listing: %d column checks, %v", n, err)
	}
	n = 0
	if _, err := m.List(ctx, h, in); err != nil || n != 0 {
		t.Fatalf("second listing: %d column checks, %v", n, err)
	}
	if err := fh.ExecSQL(dbPath, `INSERT INTO migration VALUES ('20991231000001_gate_cache_test', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.List(ctx, h, in); err != nil || n == 0 {
		t.Fatalf("after a migration: %d column checks, %v", n, err)
	}
}

// countingHost counts the schema checks of its SQL.
type countingHost struct {
	*agenttest.FakeHost
	n *int
}

func (h countingHost) SQL() agent.SQL { return countingSQL{h.FakeHost.SQL(), h.n} }

type countingSQL struct {
	agent.SQL
	n *int
}

func (c countingSQL) Columns(ctx context.Context, db, table string) ([]string, error) {
	*c.n++
	return c.SQL.Columns(ctx, db, table)
}

func TestSchemaGateOpenCode1(t *testing.T) {
	fh := schemaHost(t, func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "`session_v2`", "`session_v1_only`"), `"session_v2"`, `"session_v1_only"`)
	})
	m, h, in := open(t, fh)
	_, err := m.List(ctx, h, in)
	if err == nil || !strings.Contains(err.Error(), "OpenCode 1.x") {
		t.Fatalf("a database without session_v2: %v", err)
	}
}

func TestSchemaGateAllowsNewColumnsAndMigrations(t *testing.T) {
	fh := schemaHost(t, func(s string) string {
		return strings.Replace(s, "`resume_attempts` integer DEFAULT 0 NOT NULL,", "`resume_attempts` integer DEFAULT 0 NOT NULL,\n `future_column` text,", 1)
	}, `INSERT INTO migration VALUES ('20261201000000_future', 1);`)
	b, err := os.ReadFile(filepath.Join(fixture, "data.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := fh.ExecSQL(dbPath, string(b)); err != nil {
		t.Fatal(err)
	}
	m, h, in := open(t, fh)
	l, err := m.List(ctx, h, in)
	if err != nil || len(l.Sessions) != 12 {
		t.Fatalf("a newer schema with every column: %d sessions, %v", len(l.Sessions), err)
	}
}

func TestBundleCarriesChildren(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	l, _ := m.List(ctx, h, in)
	s := summaryFor(t, l, sesSubagent)
	b, err := m.Bundle(ctx, h, in, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 2 || b.Files[0].Role != agent.RoleMain || !b.Files[1].Virtual || b.Files[1].Rel != "hopsesh/opencode/sessions/"+sesSubagent+".children/"+sesChild+".jsonl" {
		t.Fatalf("bundle %+v", b.Files)
	}
	data, err := m.Materialize(ctx, h, in, s, b.Files[1])
	if err != nil {
		t.Fatal(err)
	}
	sess, msgs, err := decodeJSONL("child", data)
	if err != nil || sess.ID != sesChild || sess.ParentID != sesSubagent || len(msgs) != 3 {
		t.Fatalf("child file: %s parent %s, %d messages, %v", sess.ID, sess.ParentID, len(msgs), err)
	}
	// A file naming a session that is not a child is refused.
	bad := b.Files[1]
	bad.Rel = childRel(sesSubagent, sesRenamed)
	if _, err := m.Materialize(ctx, h, in, s, bad); !errors.Is(err, agent.ErrNotFound) {
		t.Errorf("materializing another session as a child: %v", err)
	}
}

func TestPlanMoveKeepBothAndOtherAccount(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	l, _ := m.List(ctx, h, in)
	s := summaryFor(t, l, sesSubagent)
	b, _ := m.Bundle(ctx, h, in, s)
	p := agent.Placement{Key: sessionKey(in, "ses_newcopy0000000000000000000"), SourceID: s.Key.Session, CWD: s.CWD, OtherAccount: true}
	mp, err := m.PlanMove(in, in, s, b, p)
	if err != nil {
		t.Fatal(err)
	}
	if mp.Files[0].ToRel != agent.AnchorRel(id, p.Key.Session) || mp.Files[1].ToRel != childRel(p.Key.Session, sesChild) {
		t.Errorf("placed at %s and %s", mp.Files[0].ToRel, mp.Files[1].ToRel)
	}
	if mp.Policy.Rename != [2]string{sesSubagent, string(p.Key.Session)} || !slices.Equal(mp.Policy.RenameKeys, renameKeys) {
		t.Errorf("rename %v %v", mp.Policy.Rename, mp.Policy.RenameKeys)
	}
	if !slices.Contains(mp.Policy.Protect, "native") || len(mp.Policy.DropElems) != 1 || mp.Policy.DropElems[0].Values[0] != "reasoning" {
		t.Errorf("policy %+v", mp.Policy)
	}
}

// Descendants of every depth travel with the root session, and count as its subagents.
func TestGrandchildren(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	const grand = "ses_e29a832f0ffeGrandchild0000"
	if err := fh.ExecSQL(dbPath, `CREATE TEMP TABLE g AS SELECT * FROM session_v2 WHERE id = '`+sesChild+`'; `+
		`UPDATE g SET id = '`+grand+`', parent_id = '`+sesChild+`', slug = 'grand'; INSERT INTO session_v2 SELECT * FROM g`); err != nil {
		t.Fatal(err)
	}
	m, h, in := open(t, fh)
	l, _ := m.List(ctx, h, in)
	s := summaryFor(t, l, sesSubagent)
	if s.Subagents != 2 || len(l.Sessions) != 12 {
		t.Fatalf("subagents %d, %d sessions listed", s.Subagents, len(l.Sessions))
	}
	b, err := m.Bundle(ctx, h, in, s)
	if err != nil || len(b.Files) != 3 || b.Files[2].Rel != childRel(sesSubagent, grand) {
		t.Fatalf("bundle %+v %v", b.Files, err)
	}
	data, err := m.Materialize(ctx, h, in, s, b.Files[2])
	if err != nil {
		t.Fatal(err)
	}
	if sess, _, err := decodeJSONL("grandchild", data); err != nil || sess.ID != grand || sess.ParentID != sesChild {
		t.Fatalf("grandchild file: %s parent %s, %v", sess.ID, sess.ParentID, err)
	}
}

func TestSessionWatchPaths(t *testing.T) {
	in := agent.Install{Roots: map[string]string{rootData: dataDir}}
	got := New().SessionWatchPaths(in, agent.PosixPath{})
	want := []string{dbPath, dbPath + "-wal", dataDir + "/hopsesh/opencode"}
	if !slices.Equal(got, want) {
		t.Fatalf("watch %v", got)
	}
	in.Roots[rootDB] = "/elsewhere/oc.db"
	if got := New().SessionWatchPaths(in, agent.PosixPath{}); got[0] != "/elsewhere/oc.db" {
		t.Fatalf("watch with OPENCODE_DB %v", got)
	}
}

func TestChannelDatabase(t *testing.T) {
	fh := agenttest.NewFakeHost(home)
	fh.Put(dataDir+"/opencode-beta.db", []byte("x"), time.Unix(100, 0))
	fh.Put(dataDir+"/opencode-dev.db", []byte("x"), time.Unix(200, 0))
	in, _ := New().Detect(ctx, fh)
	if got := dbFile(fh, in); got != dataDir+"/opencode-dev.db" {
		t.Errorf("channel database %s", got)
	}
	// Detect chose it, so the watcher watches it.
	if w := New().SessionWatchPaths(in, agent.PosixPath{}); w[0] != dataDir+"/opencode-dev.db" || w[1] != dataDir+"/opencode-dev.db-wal" {
		t.Errorf("watch %v", w)
	}
	fh.Put(dbPath, []byte("x"), time.Unix(1, 0))
	in, _ = New().Detect(ctx, fh)
	if got := dbFile(fh, in); got != dbPath || in.Root(rootDB) != "" {
		t.Errorf("with opencode.db: %s (db root %q)", got, in.Root(rootDB))
	}
}
