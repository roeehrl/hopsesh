// Package dbagent is a stand-in agent that keeps its sessions in SQLite, as OpenCode does,
// for the core's tests: it lists and reads them with the Host's SQL, its bundle file is
// virtual (Materializer), only its own program writes the database (here, in the test's
// process), installs go through that program (VendorInstaller), and undo reaches it
// through SessionStore. Its sessions sit beside an anchor file at agent.AnchorRel.
package dbagent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// ID is the stand-in's agent id.
const ID agent.ID = "dbagent"

// Module is the stand-in agent. Its counters say what its program did.
type Module struct {
	imports atomic.Int64
	minted  atomic.Int64
	// FailImport makes its program refuse every import while set.
	FailImport atomic.Bool
	// OwnIDs makes Write give a new session an id of its own, not the one asked for.
	OwnIDs atomic.Bool
}

// New returns the stand-in agent.
func New() *Module { return &Module{} }

// Imports is how many sessions its program imported (an import of a session already
// there with the same content does nothing and is not counted).
func (m *Module) Imports() int { return int(m.imports.Load()) }

// Spec declares one data root holding the database, which only SQL reads.
func (*Module) Spec() agent.Spec {
	return agent.Spec{
		ID: ID, Name: "DB Agent", Vendor: "Example", Stability: agent.Experimental, Tested: []string{"1.0"},
		Binaries:  []agent.Binary{{Name: "dbagent", VersionArgs: []string{"--version"}}},
		Roots:     []agent.Root{{Name: "data", Env: []string{"DBAGENT_DATA_DIR"}, Default: map[string]string{"*": "~/.local/share/dbagent"}}},
		SQLFiles:  []string{"{data}/db.sqlite"},
		SQLTables: []string{"session", "message"},
		SQLDenied: []string{"credential"},
	}
}

// DBFile is the database of a data root.
func DBFile(data string) string { return filepath.Join(data, "db.sqlite") }

// Anchor is the anchor file of a session in a data root.
func Anchor(data string, id agent.SessionID) string {
	return filepath.Join(data, filepath.FromSlash(agent.AnchorRel(ID, id)))
}

const schema = `
CREATE TABLE IF NOT EXISTS session (id TEXT PRIMARY KEY, directory TEXT NOT NULL, title TEXT NOT NULL, updated INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS message (session_id TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL, role TEXT NOT NULL, text TEXT NOT NULL, PRIMARY KEY (session_id, id));
CREATE TABLE IF NOT EXISTS credential (id TEXT, value TEXT);
`

// Message is one message of a session.
type Message struct {
	ID   string `json:"id"`
	Role string `json:"role"` // user or assistant
	Text string `json:"text"`
}

// record is one line of a session's JSON lines: its info first, then its messages.
type record struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Role      string `json:"role,omitempty"`
	Text      string `json:"text,omitempty"`
	Directory string `json:"directory,omitempty"`
	Title     string `json:"title,omitempty"`
}

// program is the agent's own program: the only writer of its database.
func program(data string, write func(*sql.Tx) error) error {
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	d, err := sql.Open("sqlite", "file:"+DBFile(data)+"?_pragma=busy_timeout(2000)")
	if err != nil {
		return err
	}
	defer d.Close()
	if _, err := d.Exec(schema); err != nil {
		return err
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := write(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// touch moves a session's update time on, always by at least a millisecond, so its stamp
// changes with every write.
func touch(tx *sql.Tx, id string) error {
	_, err := tx.Exec("UPDATE session SET updated = max(updated + 1, ?) WHERE id = ?", time.Now().UnixMilli(), id)
	return err
}

// Seed makes a session in a data root, as the agent would.
func Seed(data string, id agent.SessionID, dir, title string, msgs ...Message) error {
	return program(data, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO session VALUES (?, ?, ?, ?)", string(id), dir, title, time.Now().UnixMilli()); err != nil {
			return err
		}
		for i, msg := range msgs {
			if _, err := tx.Exec("INSERT INTO message VALUES (?, ?, ?, ?, ?)", string(id), msg.ID, i+1, msg.Role, msg.Text); err != nil {
				return err
			}
		}
		_, err := tx.Exec("INSERT INTO credential VALUES ('provider', 'sk-never-read')")
		return err
	})
}

// Init makes a data root's database with no sessions, as the agent does when it first runs.
func Init(data string) error {
	return program(data, func(*sql.Tx) error { return nil })
}

// Continue adds a message to a session, as using it in the agent does.
func Continue(data string, id agent.SessionID, msg Message) error {
	return program(data, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow("SELECT count(*) FROM message WHERE session_id = ?", string(id)).Scan(&n); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO message VALUES (?, ?, ?, ?, ?)", string(id), msg.ID, n+1, msg.Role, msg.Text); err != nil {
			return err
		}
		return touch(tx, string(id))
	})
}

// Exists reports whether a data root's database holds a session.
func Exists(data string, id agent.SessionID) bool {
	found := false
	_ = program(data, func(tx *sql.Tx) error {
		var n int
		err := tx.QueryRow("SELECT count(*) FROM session WHERE id = ?", string(id)).Scan(&n)
		found = n > 0
		return err
	})
	return found
}

func (m *Module) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	return agent.DefaultInstall(m.Spec(), h), nil
}

func (*Module) VirtualSessions() {}

func dbOf(h agent.Host, in agent.Install) string { return h.Path().Join(in.Main(), "db.sqlite") }

func anchorOf(h agent.Host, in agent.Install, id agent.SessionID) string {
	return h.Path().Join(in.Main(), agent.AnchorRel(ID, id))
}

func sqlOf(h agent.Host) (agent.SQL, error) {
	s, ok := agent.HostSQL(h)
	if !ok {
		return nil, agent.ErrUnsupported
	}
	return s, nil
}

func (m *Module) List(ctx context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	sq, err := sqlOf(h)
	if err != nil {
		return agent.Listing{}, nil // a snapshot: its sessions are the package's
	}
	r, err := sq.Query(ctx, dbOf(h, in), `SELECT s.id, s.directory, s.title, s.updated, (SELECT count(*) FROM message m WHERE m.session_id = s.id) AS n FROM session s ORDER BY s.updated`)
	if errors.Is(err, agent.ErrNotInstalled) {
		return agent.Listing{}, nil // no database yet
	}
	if err != nil {
		return agent.Listing{}, err
	}
	var l agent.Listing
	for _, row := range r.Rows {
		id := agent.SessionID(row.String("id"))
		l.Sessions = append(l.Sessions, agent.Summary{Key: agent.SessionKey{Agent: ID, Session: id}, Title: row.String("title"), CWD: row.String("directory"),
			Size: row.Int("n"), LastActivity: time.UnixMilli(row.Int("updated")), Path: anchorOf(h, in, id), AgentVersion: in.Version})
	}
	return l, nil
}

func (*Module) Bundle(_ context.Context, _ agent.Host, _ agent.Install, s agent.Summary) (agent.Bundle, error) {
	return agent.Bundle{Files: []agent.BundleFile{{Root: "data", Rel: agent.AnchorRel(ID, s.Key.Session), Role: agent.RoleMain, Rewrite: agent.RewriteJSONL, Virtual: true, Size: s.Size * 100}}}, nil
}

func (m *Module) records(ctx context.Context, h agent.Host, in agent.Install, id agent.SessionID) (record, []record, error) {
	sq, err := sqlOf(h)
	if err != nil {
		return record{}, nil, err
	}
	rs, err := sq.Batch(ctx, dbOf(h, in), []agent.Statement{
		{Query: "SELECT id, directory, title FROM session WHERE id = ?", Args: []any{string(id)}},
		{Query: "SELECT id, role, text FROM message WHERE session_id = ? ORDER BY seq", Args: []any{string(id)}},
	})
	if err != nil {
		return record{}, nil, err
	}
	if len(rs[0].Rows) == 0 {
		return record{}, nil, fmt.Errorf("%w: %s", agent.ErrNotFound, id)
	}
	info := record{Kind: "info", ID: string(id), Directory: rs[0].Rows[0].String("directory"), Title: rs[0].Rows[0].String("title")}
	var msgs []record
	for _, row := range rs[1].Rows {
		msgs = append(msgs, record{Kind: "message", ID: row.String("id"), Role: row.String("role"), Text: row.String("text")})
	}
	return info, msgs, nil
}

func parse(b []byte) (record, []record, error) {
	var info record
	var msgs []record
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return record{}, nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		if i == 0 {
			if r.Kind != "info" {
				return record{}, nil, errors.New("the first line is not the session's info")
			}
			info = r
		} else {
			msgs = append(msgs, r)
		}
	}
	return info, msgs, nil
}

func encode(info record, msgs []record) []byte {
	var b bytes.Buffer
	for _, r := range append([]record{info}, msgs...) {
		line, _ := json.Marshal(r)
		b.Write(append(line, '\n'))
	}
	return b.Bytes()
}

// Materialize is the session as JSON lines, from the database.
func (m *Module) Materialize(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, _ agent.BundleFile) ([]byte, error) {
	info, msgs, err := m.records(ctx, h, in, s.Key.Session)
	if err != nil {
		return nil, err
	}
	return encode(info, msgs), nil
}

func (*Module) PlanMove(_, _ agent.Install, s agent.Summary, b agent.Bundle, p agent.Placement) (agent.MovePlan, error) {
	var mp agent.MovePlan
	for _, f := range b.Files {
		mp.Files = append(mp.Files, agent.PlacedFile{From: f, ToRoot: "data", ToRel: agent.AnchorRel(ID, p.Key.Session)})
	}
	mp.Policy.Protect = []string{"id"}
	if p.Key.Session != s.Key.Session {
		mp.Policy.Rename = [2]string{string(s.Key.Session), string(p.Key.Session)}
		mp.Policy.RenameKeys = []string{"id"}
	}
	return mp, nil
}

// Verify checks the staged session reads and is in the target's folder.
func (*Module) Verify(ctx context.Context, h agent.Host, mp agent.MovePlan, staged map[string]string, p agent.Placement) error {
	for _, f := range mp.Files {
		b, err := h.FS().ReadFile(staged[agent.StagedKey(f)], 64<<20)
		if err != nil {
			return err
		}
		info, _, err := parse(b)
		if err != nil {
			return err
		}
		if info.Directory != p.CWD {
			return fmt.Errorf("the staged session is in %s, not %s", info.Directory, p.CWD)
		}
	}
	return nil
}

func (*Module) Resume(_ agent.Install, key agent.SessionKey, p agent.Placement, _ agent.ResumeOptions) agent.Command {
	return agent.Command{Argv: []string{"dbagent", "-s", string(key.Session)}, Dir: p.CWD}
}

// AfterInstall is the install itself: the agent's program imports the anchor the core
// placed, under the placement's id. A session already there with that content is done
// (a recovery runs it again); one with other content is ErrDiverged.
func (m *Module) AfterInstall(ctx context.Context, h agent.Host, in agent.Install, key agent.SessionKey, _ agent.Placement) error {
	b, err := h.FS().ReadFile(anchorOf(h, in, key.Session), 64<<20)
	if err != nil {
		return err
	}
	return m.importAs(in.Main(), key.Session, b)
}

func (*Module) InstallsViaVendor() bool { return true }

func (m *Module) importAs(data string, id agent.SessionID, b []byte) error {
	if m.FailImport.Load() {
		return errors.New("dbagent import: the database is locked")
	}
	info, msgs, err := parse(b)
	if err != nil {
		return err
	}
	return program(data, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow("SELECT count(*) FROM session WHERE id = ?", string(id)).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			have, err := txRecords(tx, string(id))
			if err != nil {
				return err
			}
			if !sameMessages(have, msgs) {
				return fmt.Errorf("%w: %s is there with other messages", agent.ErrDiverged, id)
			}
			return nil
		}
		if _, err := tx.Exec("INSERT INTO session VALUES (?, ?, ?, ?)", string(id), info.Directory, info.Title, time.Now().UnixMilli()); err != nil {
			return err
		}
		for i, r := range msgs {
			if _, err := tx.Exec("INSERT INTO message VALUES (?, ?, ?, ?, ?)", string(id), r.ID, i+1, r.Role, r.Text); err != nil {
				return err
			}
		}
		m.imports.Add(1)
		return nil
	})
}

func txRecords(tx *sql.Tx, id string) ([]record, error) {
	rows, err := tx.Query("SELECT id, role, text FROM message WHERE session_id = ? ORDER BY seq", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []record
	for rows.Next() {
		r := record{Kind: "message"}
		if err := rows.Scan(&r.ID, &r.Role, &r.Text); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func sameMessages(a, b []record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Role != b[i].Role || a[i].Text != b[i].Text {
			return false
		}
	}
	return true
}

// Read reads the database, or, on a host without it (a peer's snapshot, staged files), the
// session's JSON lines at s.Path.
func (m *Module) Read(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, from ir.Cursor) (ir.Segment, error) {
	var info record
	var msgs []record
	var err error
	if _, ok := agent.HostSQL(h); ok {
		info, msgs, err = m.records(ctx, h, in, s.Key.Session)
	} else {
		var b []byte
		if b, err = h.FS().ReadFile(s.Path, 64<<20); err == nil {
			info, msgs, err = parse(b)
		}
	}
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
	seg := ir.Segment{Header: ir.Header{Agent: string(ID), SessionID: string(s.Key.Session), CWD: info.Directory, Title: info.Title}}
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

func (*Module) Profile(agent.Install) ir.Profile { return ir.Profile{Window: 200_000} }

// Write writes a new session from another agent's: its JSON lines at its anchor, which
// its program then imports, under the id the core chose.
func (m *Module) Write(ctx context.Context, h agent.Host, in agent.Install, req ir.WriteRequest) (ir.WriteResult, error) {
	if req.Mode != ir.WriteNew {
		return ir.WriteResult{}, fmt.Errorf("%w: dbagent only writes new sessions", agent.ErrUnsupported)
	}
	id := agent.SessionID(req.SessionID)
	if id == "" || m.OwnIDs.Load() {
		id = m.NewSessionID(in, time.Now())
	}
	info := record{Kind: "info", ID: string(id), Directory: req.Header.CWD, Title: req.Header.Title}
	var msgs []record
	items := map[string]ir.Item{}
	for i, it := range req.Items {
		role := "user"
		if it.Role == ir.RoleAgent {
			role = "assistant"
		}
		mid := fmt.Sprintf("msg_%s_%d", id, i)
		items[mid] = it
		msgs = append(msgs, record{Kind: "message", ID: mid, Role: role, Text: it.Text})
	}
	anchor := anchorOf(h, in, id)
	if err := h.FS().WriteFile(anchor, encode(info, msgs), 0o600); err != nil {
		return ir.WriteResult{}, err
	}
	if err := m.AfterInstall(ctx, h, in, agent.SessionKey{Agent: ID, Session: id}, agent.Placement{}); err != nil {
		return ir.WriteResult{}, err
	}
	return m.result(ctx, h, in, id, items)
}

func (m *Module) result(ctx context.Context, h agent.Host, in agent.Install, id agent.SessionID, items map[string]ir.Item) (ir.WriteResult, error) {
	anchor := anchorOf(h, in, id)
	seg, err := m.Read(ctx, h, in, agent.Summary{Key: agent.SessionKey{Agent: ID, Session: id}, Path: anchor}, ir.Cursor{})
	if err != nil {
		return ir.WriteResult{}, err
	}
	res := ir.WriteResult{SessionID: string(id), Path: anchor, To: seg.Cursor.Offset, Cursor: seg.Cursor}
	for _, n := range seg.Nodes {
		if it, ok := items[n.Native.Anchor]; ok {
			res.Projection = append(res.Projection, ir.ProjectionFor(n, it))
		}
	}
	return res, nil
}

// RecoverWrite finishes an interrupted Write: the anchor is there, so the import runs
// again (it does nothing when it ran).
func (m *Module) RecoverWrite(ctx context.Context, h agent.Host, in agent.Install, req ir.WriteRequest) (ir.WriteResult, error) {
	id := agent.SessionID(req.SessionID)
	if err := m.AfterInstall(ctx, h, in, agent.SessionKey{Agent: ID, Session: id}, agent.Placement{}); err != nil {
		return ir.WriteResult{}, err
	}
	items := map[string]ir.Item{}
	for i, it := range req.Items {
		items[fmt.Sprintf("msg_%s_%d", id, i)] = it
	}
	return m.result(ctx, h, in, id, items)
}

// NewSessionID mints ids in the agent's own form.
func (m *Module) NewSessionID(agent.Install, time.Time) agent.SessionID {
	return agent.SessionID(fmt.Sprintf("ses_%d%04d", time.Now().UnixNano(), m.minted.Add(1)))
}

// SessionStamp is the session's message count and update time.
func (m *Module) SessionStamp(ctx context.Context, h agent.Host, in agent.Install, id agent.SessionID) (agent.SessionStamp, error) {
	sq, err := sqlOf(h)
	if err != nil {
		return "", err
	}
	r, err := sq.Query(ctx, dbOf(h, in), "SELECT s.updated, s.directory, (SELECT count(*) FROM message m WHERE m.session_id = s.id) AS n FROM session s WHERE s.id = ?", string(id))
	if err != nil {
		return "", err
	}
	if len(r.Rows) == 0 {
		return "", fmt.Errorf("%w: %s", agent.ErrNotFound, id)
	}
	return agent.SessionStamp(fmt.Sprintf("v1:%d@%d", r.Rows[0].Int("n"), r.Rows[0].Int("updated"))), nil
}

// Backup exports the session to a file in its data root, through h (journaled).
func (m *Module) Backup(ctx context.Context, h agent.Host, in agent.Install, id agent.SessionID) (string, agent.SessionStamp, error) {
	st, err := m.SessionStamp(ctx, h, in, id)
	if err != nil {
		return "", "", err
	}
	info, msgs, err := m.records(ctx, h, in, id)
	if err != nil {
		return "", "", err
	}
	p := h.Path().Join(in.Main(), "hopsesh", string(ID), "backup", fmt.Sprintf("%s-%d.jsonl", id, time.Now().UnixNano()))
	return p, st, h.FS().WriteFile(p, encode(info, msgs), 0o600)
}

// Delete deletes the session through its program while its stamp is expect.
func (m *Module) Delete(ctx context.Context, h agent.Host, in agent.Install, id agent.SessionID, expect agent.SessionStamp) error {
	if expect == "" {
		return fmt.Errorf("%w: a delete names the stamp it deletes", agent.ErrDenied)
	}
	st, err := m.SessionStamp(ctx, h, in, id)
	if err != nil {
		return err
	}
	if st != expect {
		return fmt.Errorf("%w: %s was used since", agent.ErrDiverged, id)
	}
	return program(in.Main(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM message WHERE session_id = ?", string(id)); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM session WHERE id = ?", string(id))
		return err
	})
}

// Restore imports a backup again; a session there with other content is newer work.
func (m *Module) Restore(ctx context.Context, h agent.Host, in agent.Install, id agent.SessionID, backup string) (agent.SessionStamp, error) {
	b, err := h.FS().ReadFile(backup, 64<<20)
	if err != nil {
		return "", err
	}
	if err := m.importAs(in.Main(), id, b); err != nil {
		return "", err
	}
	return m.SessionStamp(ctx, h, in, id)
}

// PlanChangeDir: the agent's program moves the session (DirAgent); its anchor stays.
func (*Module) PlanChangeDir(_ context.Context, _ agent.Host, _ agent.Install, s agent.Summary, dir string) (agent.DirPlan, error) {
	return agent.DirPlan{From: s.CWD, To: dir, Method: agent.DirAgent, Main: s.Path}, nil
}

func (*Module) ChangeDir(_ context.Context, _ agent.Host, in agent.Install, s agent.Summary, p agent.DirPlan) error {
	return program(in.Main(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE session SET directory = ? WHERE id = ?", p.To, string(s.Key.Session)); err != nil {
			return err
		}
		return touch(tx, string(s.Key.Session))
	})
}

func (*Module) InSession(_ agent.Install, dir string) string { return "/cd " + dir }
