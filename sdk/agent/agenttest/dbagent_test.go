package agenttest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// dbAgent keeps its sessions in SQLite, as OpenCode does: it reads them with the Host's
// SQL, its bundle file is virtual, and only its own program (a FakeHost Program) writes
// the database. The conformance suite must accept it.
type dbAgent struct{ minted atomic.Int64 }

const dbSchema = `
CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT NOT NULL, title TEXT, updated INTEGER NOT NULL);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, seq INTEGER NOT NULL, role TEXT NOT NULL, text TEXT NOT NULL);
CREATE TABLE credential (id TEXT, value TEXT);
INSERT INTO session VALUES ('ses_a', '/home/u/git/demo', 'Fix the build', 1700000000000);
INSERT INTO message VALUES ('msg_1', 'ses_a', 1, 'user', 'Why does the build fail?'), ('msg_2', 'ses_a', 2, 'assistant', 'A missing import.');
INSERT INTO credential VALUES ('provider', 'sk-never-read');
`

func (*dbAgent) Spec() agent.Spec {
	return agent.Spec{
		ID: "dbagent", Name: "DB Agent", Vendor: "Example", Stability: agent.Experimental, Tested: []string{"1.0"},
		Binaries:  []agent.Binary{{Name: "dbagent", VersionArgs: []string{"--version"}}},
		Roots:     []agent.Root{{Name: "data", EnvSub: []agent.EnvSub{{Env: "XDG_DATA_HOME", Sub: "dbagent"}}, Default: map[string]string{"*": "~/.local/share/dbagent"}}},
		SQLFiles:  []string{"{data}/db.sqlite"},
		SQLTables: []string{"session", "message"},
		SQLDenied: []string{"credential"},
	}
}

func dbOf(in agent.Install, h agent.Host) string { return h.Path().Join(in.Main(), "db.sqlite") }

func anchorRel(id agent.SessionID) string { return "hopsesh/sessions/" + string(id) + ".jsonl" }

func (m *dbAgent) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	return agent.DefaultInstall(m.Spec(), h), nil
}

func sqlOf(h agent.Host) (agent.SQL, error) {
	s, ok := agent.HostSQL(h)
	if !ok {
		return nil, agent.ErrUnsupported
	}
	return s, nil
}

func (m *dbAgent) List(ctx context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	sq, err := sqlOf(h)
	if err != nil {
		return agent.Listing{}, err
	}
	r, err := sq.Query(ctx, dbOf(in, h), `SELECT s.id, s.directory, s.title, s.updated, (SELECT count(*) FROM message m WHERE m.session_id = s.id) AS n FROM session s ORDER BY s.updated`)
	if err != nil {
		return agent.Listing{}, err
	}
	var l agent.Listing
	for _, row := range r.Rows {
		id := agent.SessionID(row.String("id"))
		sig := fmt.Sprintf("%s|%d|%d", id, row.Int("updated"), row.Int("n"))
		s, err := agent.ListingSummaryKey(ctx, h, dbOf(in, h)+"#"+string(id), sig, "dbagent-1", func() (*agent.Summary, error) {
			return &agent.Summary{Key: agent.SessionKey{Agent: "dbagent", Session: id}, Title: row.String("title"), CWD: row.String("directory"),
				Size: row.Int("n"), LastActivity: time.UnixMilli(row.Int("updated")), Path: h.Path().Join(in.Main(), anchorRel(id))}, nil
		})
		if err != nil {
			return l, err
		}
		l.Sessions = append(l.Sessions, *s)
	}
	return l, nil
}

func (*dbAgent) VirtualSessions() {}

func (*dbAgent) Bundle(_ context.Context, _ agent.Host, _ agent.Install, s agent.Summary) (agent.Bundle, error) {
	return agent.Bundle{Files: []agent.BundleFile{{Root: "data", Rel: anchorRel(s.Key.Session), Role: agent.RoleMain, Rewrite: agent.RewriteJSONL, Virtual: true, Size: s.Size}}}, nil
}

type record struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Role      string `json:"role,omitempty"`
	Text      string `json:"text,omitempty"`
	Directory string `json:"directory,omitempty"`
	Title     string `json:"title,omitempty"`
}

func (m *dbAgent) records(ctx context.Context, h agent.Host, in agent.Install, id agent.SessionID) (record, []record, error) {
	sq, err := sqlOf(h)
	if err != nil {
		return record{}, nil, err
	}
	rs, err := sq.Batch(ctx, dbOf(in, h), []agent.Statement{
		{Query: "SELECT id, directory, title FROM session WHERE id = ?", Args: []any{string(id)}},
		{Query: "SELECT id, role, text FROM message WHERE session_id = ? ORDER BY seq", Args: []any{string(id)}},
	})
	if err != nil {
		return record{}, nil, err
	}
	if len(rs[0].Rows) == 0 {
		return record{}, nil, agent.ErrNotFound
	}
	info := record{Kind: "info", ID: string(id), Directory: rs[0].Rows[0].String("directory"), Title: rs[0].Rows[0].String("title")}
	var msgs []record
	for _, row := range rs[1].Rows {
		msgs = append(msgs, record{Kind: "message", ID: row.String("id"), Role: row.String("role"), Text: row.String("text")})
	}
	return info, msgs, nil
}

func jsonl(info record, msgs []record) []byte {
	var b bytes.Buffer
	for _, r := range append([]record{info}, msgs...) {
		line, _ := json.Marshal(r)
		b.Write(append(line, '\n'))
	}
	return b.Bytes()
}

func (m *dbAgent) Materialize(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, _ agent.BundleFile) ([]byte, error) {
	info, msgs, err := m.records(ctx, h, in, s.Key.Session)
	if err != nil {
		return nil, err
	}
	return jsonl(info, msgs), nil
}

func (*dbAgent) PlanMove(_, _ agent.Install, _ agent.Summary, b agent.Bundle, _ agent.Placement) (agent.MovePlan, error) {
	var mp agent.MovePlan
	for _, f := range b.Files {
		mp.Files = append(mp.Files, agent.PlacedFile{From: f, ToRoot: f.Root, ToRel: f.Rel})
	}
	return mp, nil
}

func (*dbAgent) Verify(context.Context, agent.Host, agent.MovePlan, map[string]string, agent.Placement) error {
	return nil
}

func (*dbAgent) Resume(_ agent.Install, key agent.SessionKey, p agent.Placement, _ agent.ResumeOptions) agent.Command {
	return agent.Command{Argv: []string{"dbagent", "-s", string(key.Session)}, Dir: p.CWD}
}

// AfterInstall is the install: the agent imports the staged file.
func (*dbAgent) AfterInstall(ctx context.Context, h agent.Host, in agent.Install, key agent.SessionKey, _ agent.Placement) error {
	_, err := h.Exec().Run(ctx, []string{"dbagent", "import", h.Path().Join(in.Main(), anchorRel(key.Session))}, agent.RunOptions{})
	return err
}

func (*dbAgent) InstallsViaVendor() bool { return true }

func (m *dbAgent) Read(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, from ir.Cursor) (ir.Segment, error) {
	info, msgs, err := m.records(ctx, h, in, s.Key.Session)
	if err != nil {
		return ir.Segment{}, err
	}
	var nodes []ir.Node
	for _, r := range msgs {
		actor := ir.User
		if r.Role == "assistant" {
			actor = ir.Agent
		}
		nodes = append(nodes, ir.Node{Kind: ir.KindMessage, Actor: actor, Text: r.Text, Native: &ir.Native{Format: "dbagent.v1", Anchor: r.ID}})
	}
	ir.Chain(nodes, "")
	seg := ir.Segment{Header: ir.Header{Agent: "dbagent", SessionID: info.ID, CWD: info.Directory, Title: info.Title}}
	if from.Offset > int64(len(nodes)) || from.Offset > 0 && nodes[from.Offset-1].ID != from.Head {
		return seg, agent.ErrDiverged
	}
	seg.Nodes = nodes[from.Offset:]
	seg.Cursor = ir.Cursor{Offset: int64(len(nodes))}
	if len(nodes) > 0 {
		seg.Cursor.Head = nodes[len(nodes)-1].ID
	}
	return seg, nil
}

func (*dbAgent) Profile(agent.Install) ir.Profile { return ir.Profile{Window: 100_000} }

func (m *dbAgent) Write(ctx context.Context, h agent.Host, in agent.Install, req ir.WriteRequest) (ir.WriteResult, error) {
	if req.Mode != ir.WriteNew {
		return ir.WriteResult{}, agent.ErrUnsupported
	}
	id := agent.NewSessionID(m, in, time.Now(), func() string { return "" })
	info := record{Kind: "info", ID: string(id), Directory: req.Header.CWD, Title: req.Header.Title}
	var msgs []record
	for i, it := range req.Items {
		role := "user"
		if it.Role == ir.RoleAgent {
			role = "assistant"
		}
		msgs = append(msgs, record{Kind: "message", ID: fmt.Sprintf("msg_%s_%d", id, i), Role: role, Text: it.Text})
	}
	anchor := h.Path().Join(in.Main(), anchorRel(id))
	if err := h.FS().WriteFile(anchor, jsonl(info, msgs), 0o600); err != nil {
		return ir.WriteResult{}, err
	}
	if err := m.AfterInstall(ctx, h, in, agent.SessionKey{Agent: "dbagent", Session: id}, agent.Placement{}); err != nil {
		return ir.WriteResult{}, err
	}
	seg, err := m.Read(ctx, h, in, agent.Summary{Key: agent.SessionKey{Agent: "dbagent", Session: id}}, ir.Cursor{})
	if err != nil {
		return ir.WriteResult{}, err
	}
	return ir.WriteResult{SessionID: string(id), Path: anchor, To: seg.Cursor.Offset, Cursor: seg.Cursor}, nil
}

func (m *dbAgent) NewSessionID(agent.Install, time.Time) agent.SessionID {
	return agent.SessionID(fmt.Sprintf("ses_%04d", m.minted.Add(1)))
}

func (m *dbAgent) SessionStamp(ctx context.Context, h agent.Host, in agent.Install, id agent.SessionID) (agent.SessionStamp, error) {
	sq, err := sqlOf(h)
	if err != nil {
		return "", err
	}
	r, err := sq.Query(ctx, dbOf(in, h), "SELECT s.updated, (SELECT count(*) FROM message m WHERE m.session_id = s.id) AS n FROM session s WHERE s.id = ?", string(id))
	if err != nil {
		return "", err
	}
	if len(r.Rows) == 0 {
		return "", agent.ErrNotFound
	}
	return agent.SessionStamp(fmt.Sprintf("%d@%d", r.Rows[0].Int("n"), r.Rows[0].Int("updated"))), nil
}

func (m *dbAgent) Backup(ctx context.Context, h agent.Host, in agent.Install, id agent.SessionID) (string, agent.SessionStamp, error) {
	st, err := m.SessionStamp(ctx, h, in, id)
	if err != nil {
		return "", "", err
	}
	info, msgs, err := m.records(ctx, h, in, id)
	if err != nil {
		return "", "", err
	}
	p := h.Path().Join(in.Main(), "hopsesh", "backup", string(id)+".jsonl")
	return p, st, h.FS().WriteFile(p, jsonl(info, msgs), 0o600)
}

func (m *dbAgent) Delete(ctx context.Context, h agent.Host, in agent.Install, id agent.SessionID, expect agent.SessionStamp) error {
	st, err := m.SessionStamp(ctx, h, in, id)
	if err != nil {
		return err
	}
	if st != expect {
		return fmt.Errorf("%w: %s was used since", agent.ErrDiverged, id)
	}
	_, err = h.Exec().Run(ctx, []string{"dbagent", "delete", string(id)}, agent.RunOptions{})
	return err
}

func (m *dbAgent) Restore(ctx context.Context, h agent.Host, in agent.Install, id agent.SessionID, backup string) (agent.SessionStamp, error) {
	if _, err := h.Exec().Run(ctx, []string{"dbagent", "import", backup}, agent.RunOptions{}); err != nil {
		return "", err
	}
	return m.SessionStamp(ctx, h, in, id)
}

// newDBHost is a machine with dbagent's database, and its program importing and deleting
// sessions in it.
func newDBHost(t *testing.T) *agenttest.FakeHost {
	fh := agenttest.NewFakeHost("/home/u")
	fh.AddBinary("dbagent", "dbagent 1.0.0")
	db := "/home/u/.local/share/dbagent/db.sqlite"
	fh.LoadSQL(t, db)
	if err := fh.ExecSQL(db, dbSchema); err != nil {
		t.Fatal(err)
	}
	fh.Programs["dbagent"] = func(argv []string, _ agent.RunOptions) agent.Result {
		fail := func(err error) agent.Result { return agent.Result{Code: 1, Stderr: []byte(err.Error())} }
		switch argv[1] {
		case "import":
			b, ok := fh.Get(argv[2])
			if !ok {
				return fail(fmt.Errorf("no file %s", argv[2]))
			}
			var info record
			for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				var r record
				if err := json.Unmarshal([]byte(line), &r); err != nil {
					return fail(err)
				}
				if i == 0 {
					info = r
					if err := fh.ExecSQL(db, "INSERT OR IGNORE INTO session VALUES (?, ?, ?, ?)", r.ID, r.Directory, r.Title, time.Now().UnixMilli()); err != nil {
						return fail(err)
					}
					continue
				}
				if err := fh.ExecSQL(db, "INSERT OR IGNORE INTO message VALUES (?, ?, ?, ?, ?)", r.ID, info.ID, i, r.Role, r.Text); err != nil {
					return fail(err)
				}
			}
			return agent.Result{Stdout: []byte("Imported session: " + info.ID + "\n")}
		case "delete":
			if err := fh.ExecSQL(db, "DELETE FROM message WHERE session_id = ?; DELETE FROM session WHERE id = ?", argv[2], argv[2]); err != nil {
				return fail(err)
			}
			return agent.Result{}
		}
		return fail(fmt.Errorf("unknown command %v", argv))
	}
	return fh
}

func TestDatabaseAgentConformance(t *testing.T) {
	agenttest.Run(t, &dbAgent{}, newDBHost)
}

// The module reads only its tables, and its stamps guard deletes and restores.
func TestDatabaseAgentStore(t *testing.T) {
	ctx := context.Background()
	m := &dbAgent{}
	fh := newDBHost(t)
	in, _ := m.Detect(ctx, fh)
	h := agent.Confine(fh, m.Spec(), in)
	if _, err := h.FS().ReadFile(dbOf(in, h), 1<<20); err == nil {
		t.Fatal("the database file must stay a secret")
	}
	backup, st, err := m.Backup(ctx, h, in, "ses_a")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, h, in, "ses_a", st); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SessionStamp(ctx, h, in, "ses_a"); err == nil {
		t.Fatal("a deleted session has no stamp")
	}
	if got, err := m.Restore(ctx, h, in, "ses_a", backup); err != nil || got == "" {
		t.Fatalf("Restore: %q, %v", got, err)
	}
	l, _ := m.List(ctx, h, in)
	if len(l.Sessions) != 1 || l.Sessions[0].Size != 2 {
		t.Fatalf("restored: %+v", l.Sessions)
	}
	for _, read := range fh.SQLReads() {
		if read != "session" && read != "message" {
			t.Fatalf("read the %s table", read)
		}
	}
}
