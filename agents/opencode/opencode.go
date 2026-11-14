// Package opencode is the OpenCode 2.x module. OpenCode keeps every session in one SQLite
// database, {data}/opencode.db (WAL mode, event-sourced), which its background service,
// TUI and desktop app share. hopsesh reads it through the Host's SQL, from an allowlist of
// tables, and never writes it: changes go through OpenCode's own program (opencode api,
// session import and delete), never starting, stopping or restarting its service. Where the
// database cannot be read, OpenCode's API and export are read instead.
//
// A session's file, for moves and packages, is made from the database (Materializer): JSON
// lines at an anchor hopsesh keeps, {data}/hopsesh/opencode/sessions/<id>.jsonl, beside
// which its lineage sits. Child sessions (subagents) belong to their root session: they
// are counted, not listed, and travel with it.
package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Module is the OpenCode module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

var (
	_ agent.Module               = (*Module)(nil)
	_ agent.VirtualSessions      = (*Module)(nil)
	_ agent.Materializer         = (*Module)(nil)
	_ agent.SessionStamper       = (*Module)(nil)
	_ agent.SessionIDMinter      = (*Module)(nil)
	_ agent.Reader               = (*Module)(nil)
	_ agent.Previewer            = (*Module)(nil)
	_ agent.Renamer              = (*Module)(nil)
	_ agent.LiveDetector         = (*Module)(nil)
	_ agent.Stopper              = (*Module)(nil)
	_ agent.AccountProber        = (*Module)(nil)
	_ agent.ContextSizer         = (*Module)(nil)
	_ agent.Sanitizer            = (*Module)(nil)
	_ agent.SanitizedProjection  = (*Module)(nil)
	_ agent.SessionWatchProvider = (*Module)(nil)
	_ agent.HostResumer          = (*Module)(nil)
	_ agent.VendorInstaller      = (*Module)(nil)
	_ agent.SessionStore         = (*Module)(nil)
	_ agent.DirChanger           = (*Module)(nil)
	_ agent.DirChangeHelp        = (*Module)(nil)
	_ agent.NativeForkVerifier   = (*Module)(nil)
	_ agent.NativeForkOrphans    = (*Module)(nil)
)

// VirtualSessions marks OpenCode's sessions as rows of a database, each one's size a count
// of messages.
func (*Module) VirtualSessions() {}

// Detect resolves the roots and, on a Mac, the desktop app. The "db" root names the
// database when it is not {data}/opencode.db: OPENCODE_DB's, or another channel's.
func (m *Module) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	in := agent.DefaultInstall(m.Spec(), h)
	if db := resolveDB(h, in.Root(rootData), h.Facts().Env["OPENCODE_DB"]); db != "" {
		in.Roots[rootDB] = db
	} else if data := in.Root(rootData); data != "" {
		if db := dbFile(h, in); db != h.Path().Join(data, "opencode.db") {
			in.Roots[rootDB] = db
		}
	}
	detectDesktop(h, &in)
	return in, nil
}

var plistVersion = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`)

var plistID = regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>ai\.opencode\.desktop</string>`)

// detectDesktop finds OpenCode desktop on a Mac by its bundle's public metadata, and the
// version of the CLI it bundles. It shares the data root, so its sessions are these.
func detectDesktop(h agent.Host, in *agent.Install) {
	in.OS = h.Facts().OS
	if in.OS != "darwin" {
		return
	}
	for _, app := range desktopApps {
		app = agent.Expand(app, h.Facts().Home, nil, h.Path())
		plist, err := h.FS().ReadFile(h.Path().Join(app, "Contents", "Info.plist"), 1<<20)
		if err != nil || !plistID.Match(plist) {
			continue
		}
		in.Desktop = app
		if v := plistVersion.FindSubmatch(plist); len(v) == 2 {
			in.DesktopVersion = strings.TrimSpace(string(v[1]))
		}
		if b, err := h.FS().ReadFile(h.Path().Join(app, "Contents", "Resources", "opencode-cli.version"), 1<<10); err == nil {
			in.DesktopEngine = strings.TrimSpace(string(b))
		}
		return
	}
}

// List lists the root sessions: child sessions (subagents) are counted on their root,
// as OpenCode's own list shows them; archived sessions are of the kind "archived".
// The database is checked first for every column the module reads (as every read does).
func (m *Module) List(ctx context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	r, ok, err := openSQL(ctx, h, in)
	if !ok {
		l, err := m.listAPI(ctx, h, in)
		if notRun(err) {
			return agent.Listing{}, nil // nothing runs here (a peer's package): its sessions are the package's
		}
		return l, err
	}
	if err == nil {
		var rows []listed
		if rows, err = r.list(ctx); err == nil {
			return m.summaries(ctx, h, in, rows, r.db, customTitles(ctx, h, in))
		}
	}
	switch {
	case errors.Is(err, agent.ErrNotInstalled):
		return agent.Listing{}, nil // no database yet: OpenCode has not run
	case readUnsupported(err):
		return m.listAPI(ctx, h, in)
	}
	return agent.Listing{}, err
}

// listingSalt versions the summaries the listing cache keeps.
const listingSalt = "opencode-summary-2"

// summaries turns session rows into the listing: the root sessions, with their
// descendants counted as subagents.
func (m *Module) summaries(ctx context.Context, h agent.Host, in agent.Install, rows []listed, cacheKey string, titles map[string]string) (agent.Listing, error) {
	children := map[string][]string{}
	for _, r := range rows {
		if r.ParentID != "" {
			children[r.ParentID] = append(children[r.ParentID], r.ID)
		}
	}
	l := agent.Listing{Sessions: make([]agent.Summary, 0, len(rows))}
	for _, r := range rows {
		if r.ParentID != "" {
			continue
		}
		subagents := len(family(children, r.ID))
		custom := r.HasTitle && titles[r.ID] == r.Title
		sig := fmt.Sprintf("%s|%d|%d|%d|%d|%s|%t", r.ID, r.Updated, r.Settled, r.Parts, subagents, in.ProfileID(), custom)
		if cacheKey == "api" {
			sig = "" // an API listing has no counts to key on
		}
		sum, err := agent.ListingSummaryKey(ctx, h, cacheKey+"#"+r.ID, sig, listingSalt, func() (*agent.Summary, error) {
			s := summaryOf(h, in, r, subagents)
			if custom && s.TitleSource == "generated" {
				s.TitleSource = "custom"
			}
			return &s, nil
		})
		if err != nil {
			return l, err
		}
		l.Sessions = append(l.Sessions, *sum)
	}
	return l, nil
}

// Bundle is the session's file and one for each of its descendants, all made from the
// database (Virtual). Where nothing runs (a peer's package), the files hopsesh placed are
// the bundle.
func (m *Module) Bundle(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary) (agent.Bundle, error) {
	main := agent.BundleFile{Root: rootData, Rel: agent.AnchorRel(id, s.Key.Session), Role: agent.RoleMain, Rewrite: agent.RewriteJSONL, Virtual: true, Size: estimate(s.Size)}
	kids, err := m.children(ctx, h, in, s.Key.Session)
	if notRun(err) {
		if files, ok := placedBundle(h, in, s, main); ok {
			return files, nil
		}
	}
	if err != nil {
		return agent.Bundle{}, err
	}
	b := agent.Bundle{Files: []agent.BundleFile{main}, Warnings: m.moveWarnings(ctx, h, in, s)}
	for _, c := range kids {
		b.Files = append(b.Files, agent.BundleFile{Root: rootData, Rel: childRel(s.Key.Session, c), Role: agent.RoleSide, Rewrite: agent.RewriteJSONL, Virtual: true, Size: estimate(1)})
	}
	return b, nil
}

// claudeAgent is the provider of OpenCode's "Claude Agent" sessions.
const claudeAgent = "claude-agent"

// moveWarnings are what a move's preview says about an OpenCode session: a window that has
// it open while it is idle cannot be seen, and a session of the Claude Agent provider
// stays an OpenCode session.
func (m *Module) moveWarnings(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary) []string {
	where := h.Facts().Machine
	if where == "" {
		where = "the machine it is on"
	}
	out := []string{fmt.Sprintf("hopsesh cannot see an OpenCode window (a terminal or the desktop app) that has this session open while it is idle on %s; close it there, so it does not continue the copy left behind", where)}
	if r, ok, err := openSQL(ctx, h, in); ok && err == nil {
		if row, err := r.session(ctx, s.Key.Session); err == nil {
			var model modelRef
			if json.Unmarshal(row.Model, &model) == nil && model.ProviderID == claudeAgent {
				out = append(out, "this session runs on OpenCode's Claude Agent provider: it moves as an OpenCode session (not a Claude Code one), and OpenCode here needs that provider set up to continue it")
			}
		}
	}
	return out
}

// placedBundle is a bundle from the files at the session's anchor and its children's
// folder, when the anchor is there.
func placedBundle(h agent.Host, in agent.Install, s agent.Summary, main agent.BundleFile) (agent.Bundle, bool) {
	fi, err := h.FS().Stat(anchorOf(h, in, s.Key.Session))
	if err != nil {
		return agent.Bundle{}, false
	}
	main.Virtual, main.Size = false, fi.Size()
	b := agent.Bundle{Files: []agent.BundleFile{main}}
	dir := strings.TrimSuffix(anchorOf(h, in, s.Key.Session), ".jsonl") + ".children"
	entries, err := h.FS().ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return agent.Bundle{}, false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		child := agent.SessionID(strings.TrimSuffix(e.Name(), ".jsonl"))
		b.Files = append(b.Files, agent.BundleFile{Root: rootData, Rel: childRel(s.Key.Session, child), Role: agent.RoleSide, Rewrite: agent.RewriteJSONL, Size: e.Size()})
	}
	return b, true
}

// estimate is a virtual file's size from its message count, for progress and limits.
func estimate(messages int64) int64 { return max(messages, 1) * 4096 }

// PlanMove places the session's file at the target's anchor for its id (a keep-both copy
// renamed), and its children's files beside it, rewritten with the module's policy.
func (m *Module) PlanMove(_, _ agent.Install, s agent.Summary, b agent.Bundle, p agent.Placement) (agent.MovePlan, error) {
	mp := agent.MovePlan{Policy: policy()}
	if p.OtherAccount {
		mp.Policy.DropElems = m.Sanitize().DropElems
	}
	if p.Key.Session != s.Key.Session {
		mp.Policy.Rename = [2]string{string(s.Key.Session), string(p.Key.Session)}
		mp.Policy.RenameKeys = renameKeys
	}
	for _, f := range b.Files {
		to := agent.AnchorRel(id, p.Key.Session)
		if f.Role != agent.RoleMain {
			child := childOf(s.Key.Session, f.Rel)
			if child == "" {
				return agent.MovePlan{}, fmt.Errorf("%s is not a file of the OpenCode session %s", f.Rel, s.Key.Session)
			}
			to = childRel(p.Key.Session, child)
		}
		mp.Files = append(mp.Files, agent.PlacedFile{From: f, ToRoot: rootData, ToRel: to})
	}
	return mp, nil
}

// Resume is `opencode -s <id> [--prompt …]` in the session's folder. It never forks (no
// CapFork yet). The program is the PATH opencode; ResumeOn picks one of the background
// service's version and checks the session is there first.
func (m *Module) Resume(in agent.Install, key agent.SessionKey, p agent.Placement, o agent.ResumeOptions) agent.Command {
	return resumeCommand(in, binName, key, p, o)
}

func resumeCommand(in agent.Install, program string, key agent.SessionKey, p agent.Placement, o agent.ResumeOptions) agent.Command {
	argv := []string{program, "-s", string(key.Session)}
	if o.Prompt != "" {
		argv = append(argv, "--prompt", o.Prompt)
	}
	// A terminal opened from an OpenCode shell carries that session's id; the resumed
	// session's own commands get their own.
	return in.ScopeCommand(agent.Command{Argv: argv, Dir: p.CWD, Unset: []string{"OPENCODE_SESSION_ID"}})
}

// ResumeOn is Resume after checking the machine: the session must be there (OpenCode's
// -s makes an empty session for an id it does not have), and the program must be of the
// running background service's version, or the TUI would restart the service (D2).
func (m *Module) ResumeOn(ctx context.Context, h agent.Host, in agent.Install, key agent.SessionKey, p agent.Placement, o agent.ResumeOptions) (agent.Command, error) {
	if _, err := m.SessionStamp(ctx, h, in, key.Session); err != nil {
		if errors.Is(err, agent.ErrNotFound) {
			return agent.Command{}, fmt.Errorf("%w: OpenCode has no session %s here", agent.ErrNotFound, key.Session)
		}
		return agent.Command{}, err
	}
	program, err := resumeProgram(ctx, h, in, probeService(ctx, h))
	if err != nil {
		return agent.Command{}, err
	}
	return resumeCommand(in, program, key, p, o), nil
}

// SessionWatchPaths are the database Detect chose and its WAL (every change lands there)
// and hopsesh's own files; never logs, snapshots or the config.
func (m *Module) SessionWatchPaths(in agent.Install, pa agent.Path) []string {
	data := in.Root(rootData)
	if data == "" {
		return nil
	}
	db := pa.Join(data, "opencode.db")
	if p := in.Root(rootDB); p != "" {
		db = p
	}
	return []string{db, db + "-wal", pa.Join(data, "hopsesh", string(id))}
}
