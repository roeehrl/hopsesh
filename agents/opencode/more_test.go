package opencode

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func TestCapacityFromCatalog(t *testing.T) {
	fh, _ := newMachine(t, setup{synthetic: true})
	m, h, in := open(t, fh)
	s := agent.Summary{Key: sessionKey(in, sesMixed)}
	c, err := m.ContextCapacity(ctx, h, in, &s)
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "openai/gpt-5.5-pro" || c.Window != 922000 || c.Source != "models.dev catalog cached by OpenCode" || c.Unknown || c.ConfigHash == "" {
		t.Fatalf("capacity %+v", c)
	}
	// The newest reply's usage (100 + 10 + 1000), and the bytes of what follows it.
	r := mustSQL(t, h, in)
	after, err := r.sq.Query(ctx, r.db, "SELECT sum(length(data) + 32) AS n FROM session_message WHERE session_id = ? AND seq > 4", sesMixed)
	if err != nil {
		t.Fatal(err)
	}
	if want := 1110 + int(after.Rows[0].Int("n")); c.Existing != want {
		t.Errorf("existing %d, want %d", c.Existing, want)
	}
}

// A model the catalog does not know (a local provider) gets the fallback window; the
// config file, which holds provider keys, is never read.
func TestCapacityFallback(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	fh.Put(home+"/.config/opencode/opencode.json", []byte(`{"provider":{"omlx":{"options":{"apiKey":"never-read"}}}}`), timeZero)
	m, h, in := open(t, fh)
	s := agent.Summary{Key: sessionKey(in, sesFiles)}
	c, err := m.ContextCapacity(ctx, h, in, &s)
	if err != nil {
		t.Fatal(err)
	}
	if c.Window != ir.FallbackWindow || !strings.HasPrefix(c.Model, "omlx/") || c.Existing == 0 || c.Unknown {
		t.Fatalf("capacity %+v", c)
	}
	if _, err := h.FS().ReadFile(home+"/.config/opencode/opencode.json", 1<<20); !errors.Is(err, agent.ErrDenied) {
		t.Fatalf("the config is readable: %v", err)
	}
	// A new session: the newest reply's model.
	c, err = m.ContextCapacity(ctx, h, in, nil)
	if err != nil || !strings.HasPrefix(c.Model, "omlx/") || c.Existing != 0 {
		t.Fatalf("a new session: %+v %v", c, err)
	}
	// Without the database, an existing session's usage is unknown.
	c, _ = m.ContextCapacity(ctx, agent.Confine(placed{fh}, m.Spec(), in), in, &s)
	if !c.Unknown {
		t.Fatalf("without the database: %+v", c)
	}
}

func TestIDs(t *testing.T) {
	at := time.UnixMilli(1794597439442)
	ses := newID("ses_", true, at)
	msg1, msg2 := newID("msg_", false, at), newID("msg_", false, at.Add(time.Millisecond))
	if len(ses) != 30 || !strings.HasPrefix(ses, "ses_") || len(msg1) != 30 {
		t.Fatalf("ids %s %s", ses, msg1)
	}
	for _, c := range ses[16:] {
		if !strings.ContainsRune(base62, c) {
			t.Fatalf("tail %s", ses[16:])
		}
	}
	o1, _ := idOrder(msg1)
	o2, _ := idOrder(msg2)
	os, _ := idOrder(ses)
	if !(o1 < o2) || os>>12 != o1>>12 {
		t.Fatalf("orders %x %x %x", o1, o2, os)
	}
	// Session ids sort newest first, as OpenCode's do.
	later := newID("ses_", true, at.Add(time.Second))
	if !(later < ses) {
		t.Fatalf("%s does not sort before %s", later, ses)
	}
	if a, b := New().NewSessionID(agent.Install{}, at), New().NewSessionID(agent.Install{}, at); a == b || !strings.HasPrefix(string(a), "ses_") {
		t.Fatalf("minted %s %s", a, b)
	}
	if _, ok := idOrder("ses_nothex000000000"); ok {
		t.Fatal("a malformed id decoded")
	}
}

// The fixture's ids are OpenCode's: each carries the time it was made.
func TestIDsOfTheFixture(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	_, h, in := open(t, fh)
	r := mustSQL(t, h, in)
	rows, err := r.sq.Query(ctx, r.db, "SELECT id, time_created FROM session_v2 WHERE id NOT LIKE '%HandMade%'")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows.Rows {
		// OpenCode mints the id just before it writes the row.
		got, ok := idOrder(row.String("id"))
		created := uint64(row.Int("time_created")) & (1<<36 - 1)
		if ms := got >> 12; !ok || ms > created || created-ms > 2000 {
			t.Errorf("%s: made at %d, the row at %d", row.String("id"), ms, created)
		}
		if idTime(int64(got>>12), int64(got&0xfff), true) != row.String("id")[4:16] {
			t.Errorf("%s: does not encode back", row.String("id"))
		}
	}
}

func TestStamp(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	st, err := m.SessionStamp(ctx, h, in, sesFiles)
	if err != nil || st != "v1:8@1794597433808" || st.Format() != "v1" {
		t.Fatalf("stamp %q %v", st, err)
	}
	if err := fh.ExecSQL(dbPath, `INSERT INTO session_message VALUES ('msg_zz', ?, 'user', 999, 1, 1, '{"time":{"created":1},"text":"more","files":[]}')`, sesFiles); err != nil {
		t.Fatal(err)
	}
	if again, _ := m.SessionStamp(ctx, h, in, sesFiles); again == st {
		t.Fatal("a new message left the stamp as it was")
	}
	if _, err := m.SessionStamp(ctx, h, in, "ses_missing"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("a missing session: %v", err)
	}
	// Through OpenCode's export the stamp has the same form and value.
	byExport, err := m.SessionStamp(ctx, agent.Confine(noSQL{fh}, m.Spec(), in), in, sesRenamed)
	byDB, _ := m.SessionStamp(ctx, h, in, sesRenamed)
	if err != nil || byExport != byDB {
		t.Fatalf("export stamp %q, database %q, %v", byExport, byDB, err)
	}
}

func TestPreview(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	s := agent.Summary{Key: sessionKey(in, sesLong)}
	p, err := m.Preview(ctx, h, in, s, 2)
	if err != nil {
		t.Fatal(err)
	}
	if p.Messages() != 2 || !p.More || p.First == nil || p.First.Role != agent.PreviewUser {
		t.Fatalf("preview %+v", p)
	}
	last := p.Items[len(p.Items)-1]
	if last.Role != agent.PreviewAgent || last.Text == "" {
		t.Errorf("last item %+v", last)
	}
	full, err := m.Preview(ctx, h, in, s, 100)
	if err != nil {
		t.Fatal(err)
	}
	var compacted, tools bool
	for _, it := range full.Items {
		compacted = compacted || it.Role == agent.PreviewCompacted
		tools = tools || it.Role == agent.PreviewTools && it.Tools[ir.ToolExecute] > 0
	}
	if !compacted || !tools || full.More {
		t.Errorf("full preview: compacted %v, tools %v, more %v", compacted, tools, full.More)
	}
	// From OpenCode's export, the same.
	viaExport, err := m.Preview(ctx, agent.Confine(noSQL{fh}, m.Spec(), in), in, s, 2)
	if err != nil || viaExport.Messages() != 2 || viaExport.Items[len(viaExport.Items)-1].Text != last.Text || viaExport.First.Text != p.First.Text {
		t.Fatalf("preview from the export: %+v %v", viaExport, err)
	}
}

// Where hopsesh cannot read the database, it lists and reads through OpenCode's API and
// export: the same root sessions, kinds and messages.
func TestRemoteAPITier(t *testing.T) {
	fh, f := newMachine(t, setup{})
	m, h, in := open(t, fh)
	viaSQL, _ := m.List(ctx, h, in)
	api := agent.Confine(noSQL{fh}, m.Spec(), in)
	viaAPI, err := m.List(ctx, api, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(viaAPI.Sessions) != len(viaSQL.Sessions) {
		t.Fatalf("API lists %d sessions, the database %d", len(viaAPI.Sessions), len(viaSQL.Sessions))
	}
	for _, s := range viaSQL.Sessions {
		a := summaryFor(t, viaAPI, string(s.Key.Session))
		if a.Title != s.Title || a.CWD != s.CWD || !slices.Equal(a.Kinds, s.Kinds) || a.Subagents != s.Subagents || a.NativeParent != s.NativeParent {
			t.Errorf("%s: API %+v, database %+v", s.Key.Session, a, s)
		}
		sameNodes(t, "API "+string(s.Key.Session), readAll(t, m, h, in, s), readAll(t, m, api, in, a))
	}
	b, err := m.Bundle(ctx, api, in, summaryFor(t, viaAPI, sesSubagent))
	if err != nil || len(b.Files) != 2 || !b.Files[1].Virtual {
		t.Fatalf("bundle through the API %+v %v", b, err)
	}
	for _, c := range f.commands() {
		if c != "service status" && !strings.Contains(c, "--standalone") {
			t.Errorf("%q", c)
		}
	}
}

// placedSQL is a machine whose reader cannot read the database (another machine's WAL
// database that no program has open).
type placedSQL struct{ agent.Host }

func (placedSQL) SQL() agent.SQL { return unsupportedSQL{} }

type unsupportedSQL struct{}

func (unsupportedSQL) Query(_ context.Context, _, _ string, _ ...any) (agent.Rows, error) {
	return agent.Rows{}, fmt.Errorf("%w: that machine cannot open the database read only", agent.ErrUnsupported)
}
func (unsupportedSQL) Batch(_ context.Context, _ string, _ []agent.Statement) ([]agent.Rows, error) {
	return nil, fmt.Errorf("%w: that machine cannot open the database read only", agent.ErrUnsupported)
}
func (unsupportedSQL) Columns(_ context.Context, _, _ string) ([]string, error) {
	return nil, fmt.Errorf("%w: that machine cannot open the database read only", agent.ErrUnsupported)
}

func TestClosedWALFallsBackToTheAPI(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, _, in := open(t, fh)
	h := agent.Confine(placedSQL{fh}, m.Spec(), in)
	if _, ok := agent.HostSQL(h); !ok {
		t.Fatal("the host has no reader")
	}
	l, err := m.List(ctx, h, in)
	if err != nil || len(l.Sessions) != 12 {
		t.Fatalf("%d sessions, %v", len(l.Sessions), err)
	}
	seg, err := m.Read(ctx, h, in, summaryFor(t, l, sesRenamed), ir.Cursor{})
	if err != nil || len(seg.Nodes) == 0 {
		t.Fatalf("read %d nodes, %v", len(seg.Nodes), err)
	}
}

// Where nothing runs (a peer's package), the listing is empty and the bundle is the
// files hopsesh placed.
func TestPlacedFilesOnly(t *testing.T) {
	fh, _ := newMachine(t, setup{})
	m, h, in := open(t, fh)
	l, _ := m.List(ctx, h, in)
	s := summaryFor(t, l, sesSubagent)
	b, _ := m.Bundle(ctx, h, in, s)
	for _, f := range b.Files {
		data, err := m.Materialize(ctx, h, in, s, f)
		if err != nil {
			t.Fatal(err)
		}
		fh.Put(dataDir+"/"+f.Rel, data, time.Now())
	}
	ph := agent.Confine(placed{fh}, m.Spec(), in)
	if pl, err := m.List(ctx, ph, in); err != nil || len(pl.Sessions) != 0 {
		t.Fatalf("listing where nothing runs: %d, %v", len(pl.Sessions), err)
	}
	pb, err := m.Bundle(ctx, ph, in, s)
	if err != nil || len(pb.Files) != 2 || pb.Files[0].Virtual || pb.Files[1].Rel != b.Files[1].Rel {
		t.Fatalf("placed bundle %+v %v", pb, err)
	}
}
