// Package codex is the OpenAI Codex module (CLI, IDE extension and desktop app share one
// store). Sessions ("threads") are append-only JSON-lines rollouts under
// $CODEX_HOME/sessions/YYYY/MM/DD/rollout-<local time>-<id>.jsonl. Codex also keeps SQLite
// indexes; hopsesh never touches them: Codex finds a copied-in rollout by id and indexes it
// itself.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

const id agent.ID = "codex"

const home = "home"

// Module is the Codex module.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

var (
	_ agent.Module       = (*Module)(nil)
	_ agent.LiveDetector = (*Module)(nil)
	_ agent.Integrator   = (*Module)(nil)
)

// Spec declares Codex.
func (*Module) Spec() agent.Spec {
	return agent.Spec{
		ID:        id,
		Name:      "Codex",
		Vendor:    "OpenAI",
		Stability: agent.Experimental,
		Tested:    []string{"0.153"},
		Binaries: []agent.Binary{{
			Name: "codex",
			Candidates: map[string][]string{
				"*":       {"~/.local/bin/codex", "/opt/homebrew/bin/codex", "/usr/local/bin/codex", "~/.npm-global/bin/codex"},
				"windows": {"~/AppData/Roaming/npm/codex.cmd", "~/.local/bin/codex.exe"},
			},
			VersionArgs: []string{"--version"},
		}},
		Roots:        []agent.Root{{Name: home, Env: []string{"CODEX_HOME"}, Default: map[string]string{"*": "~/.codex"}}},
		LoginEnv:     []string{"CODEX_HOME"},
		Secrets:      []string{"{home}/auth.json"},
		Instructions: []string{"AGENTS.md"},
		Tools:        "shell, apply_patch, update_plan",
		Features:     []agent.Capability{agent.CapFork},
	}
}

// Detect resolves CODEX_HOME and the codex binary.
func (m *Module) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	return agent.DefaultInstall(m.Spec(), h), nil
}

// meta is the first record of a rollout.
type meta struct {
	ID            string          `json:"id"`
	Timestamp     string          `json:"timestamp"`
	CWD           string          `json:"cwd"`
	Originator    string          `json:"originator"`
	CLIVersion    string          `json:"cli_version"`
	Source        json.RawMessage `json:"source"`
	ModelProvider string          `json:"model_provider"`
	HistoryMode   string          `json:"history_mode"`
	HistoryBase   *struct {
		ThreadID string `json:"thread_id"`
	} `json:"history_base"`
	Git *struct {
		Branch string `json:"branch"`
	} `json:"git"`
	CreatorAccountID string `json:"creator_account_id"`
}

// line is one rollout record.
type line struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// List walks sessions/YYYY/MM/DD, newest first. Sub-agent threads are left out, as
// Codex's own pickers do.
func (m *Module) List(_ context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	files, err := rollouts(h, in)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return agent.Listing{}, nil
		}
		return agent.Listing{}, err
	}
	titles := names(h, in)
	out := agent.Listing{Sessions: make([]agent.Summary, 0, len(files))}
	sums := make([]*agent.Summary, len(files))
	errs := make([]error, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			sums[i], errs[i] = summarize(h, f)
		}()
	}
	wg.Wait()
	for i, s := range sums {
		switch {
		case errs[i] != nil:
			out.Errors = append(out.Errors, agent.SessionError{Path: files[i].path, Err: errs[i]})
		case s != nil:
			if t := titles[string(s.Key.Session)]; t != "" {
				s.Title, s.TitleSource = t, "custom"
				if mk, orig, ok := agent.ParseMarkTitle(t); ok {
					s.Mark, s.Title = &mk, orig
				}
			}
			out.Sessions = append(out.Sessions, *s)
		}
	}
	sort.Slice(out.Sessions, func(i, j int) bool { return out.Sessions[i].LastActivity.After(out.Sessions[j].LastActivity) })
	return out, nil
}

type rollout struct {
	path string
	info fs.FileInfo
}

// rollouts lists every rollout file under sessions/.
func rollouts(h agent.Host, in agent.Install) ([]rollout, error) {
	pa, fsys := h.Path(), h.FS()
	root := pa.Join(in.Root(home), "sessions")
	years, err := fsys.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []rollout
	for _, y := range years {
		if !y.IsDir() || !numeric(y.Name()) {
			continue
		}
		months, _ := fsys.ReadDir(pa.Join(root, y.Name()))
		for _, mo := range months {
			if !mo.IsDir() || !numeric(mo.Name()) {
				continue
			}
			days, _ := fsys.ReadDir(pa.Join(root, y.Name(), mo.Name()))
			for _, d := range days {
				if !d.IsDir() || !numeric(d.Name()) {
					continue
				}
				dir := pa.Join(root, y.Name(), mo.Name(), d.Name())
				entries, _ := fsys.ReadDir(dir)
				for _, e := range entries {
					if isRollout(e.Name()) && !e.IsDir() {
						out = append(out, rollout{pa.Join(dir, e.Name()), e})
					}
				}
			}
		}
	}
	return out, nil
}

func numeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

func isRollout(n string) bool {
	return strings.HasPrefix(n, "rollout-") && strings.HasSuffix(n, ".jsonl")
}

// names reads session_index.jsonl: thread names, the last entry for an id wins. (Codex
// also keeps names in SQLite for newer threads; hopsesh does not read its databases.)
func names(h agent.Host, in agent.Install) map[string]string {
	out := map[string]string{}
	b, err := h.FS().ReadFile(h.Path().Join(in.Root(home), "session_index.jsonl"), 16<<20)
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n") {
		var e struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal([]byte(l), &e) == nil && e.ID != "" {
			out[e.ID] = e.Name
		}
	}
	return out
}

// setName gives a thread a name the way Codex does: a line appended to
// session_index.jsonl (the last line for an id wins).
func setName(h agent.Host, in agent.Install, sid, name string) error {
	line, err := marshal(map[string]any{"id": sid, "thread_name": name, "updated_at": stamp(time.Now())})
	if err != nil {
		return err
	}
	p := h.Path().Join(in.Root(home), "session_index.jsonl")
	if _, err := h.FS().Stat(p); err != nil {
		return h.FS().WriteFile(p, append(line, '\n'), 0o600)
	}
	return h.FS().Append(p, append(line, '\n'), agent.AppendOptions{NewLine: true})
}

// Mark names the thread left behind "↪ moved to …" (or "continued in …"), which Codex's
// own thread list shows.
func (m *Module) Mark(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, mk agent.Mark) error {
	return setName(h, in, string(s.Key.Session), agent.MarkTitle(mk, s.Title))
}

// headChunk and tailChunk bound what a listing reads of each rollout.
const (
	headChunk = 256 << 10
	tailChunk = 256 << 10
)

// summarize reads a rollout's first records (meta, first prompt) and last records (last
// prompt, activity). It returns nil for threads hopsesh does not list.
func summarize(h agent.Host, r rollout) (*agent.Summary, error) {
	f, err := h.FS().Open(r.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	size := r.info.Size()
	head := make([]byte, min(size, headChunk))
	if _, err := f.ReadAt(head, 0); err != nil && err != io.EOF {
		return nil, err
	}
	first, err := firstMeta(head)
	if err != nil {
		return nil, &agent.FormatError{Path: r.path, Line: 1, Err: err}
	}
	if subagent(first.Source) {
		return nil, nil
	}
	s := &agent.Summary{
		Key:          agent.SessionKey{Agent: id, Session: agent.SessionID(first.ID)},
		CWD:          first.CWD,
		Size:         size,
		Path:         r.path,
		AgentVersion: first.CLIVersion,
		LastActivity: r.info.ModTime().UTC(),
		Account:      first.CreatorAccountID,
	}
	if first.Git != nil {
		s.GitBranch = first.Git.Branch
	}
	talked := false // any user message, hopsesh's own included
	for _, l := range lines(head, false) {
		p := userPrompt(l)
		talked = talked || p != ""
		if p != "" && !agent.IsNote(p) {
			s.Title, s.TitleSource = clip(p), "prompt"
			break
		}
	}
	tail := head
	if size > headChunk {
		start := max(size-tailChunk, int64(len(head)))
		tail = make([]byte, size-start)
		if _, err := f.ReadAt(tail, start); err != nil && err != io.EOF {
			return nil, err
		}
	}
	tl := lines(tail, size > headChunk)
	for i := len(tl) - 1; i >= 0; i-- {
		p := userPrompt(tl[i])
		talked = talked || p != ""
		if p != "" && !agent.IsNote(p) {
			s.LastPrompt = clip(p)
			break
		}
	}
	for i := len(tl) - 1; i >= 0; i-- {
		if tl[i].Type == "response_item" || tl[i].Type == "event_msg" {
			if ts, err := time.Parse(time.RFC3339Nano, tl[i].Timestamp); err == nil {
				s.LastActivity = ts.UTC()
			}
			break
		}
	}
	if !talked {
		return nil, nil // no conversation yet
	}
	return s, nil
}

// firstMeta decodes the session_meta record that starts every rollout.
func firstMeta(b []byte) (meta, error) {
	ls := lines(b, false)
	if len(ls) == 0 || ls[0].Type != "session_meta" {
		return meta{}, errors.New("a rollout starts with session_meta")
	}
	var m meta
	if err := json.Unmarshal(ls[0].Payload, &m); err != nil {
		return meta{}, err
	}
	if m.ID == "" || m.CWD == "" {
		return meta{}, errors.New("session_meta lacks id or cwd")
	}
	return m, nil
}

// lines decodes complete JSON lines; a tail chunk's first line is partial.
func lines(b []byte, isTail bool) []line {
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	var out []line
	firstLine := true
	for sc.Scan() {
		if isTail && firstLine {
			firstLine = false
			continue
		}
		firstLine = false
		var l line
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Type != "" {
			out = append(out, l)
		}
	}
	return out
}

func subagent(source json.RawMessage) bool {
	var obj map[string]json.RawMessage
	return json.Unmarshal(source, &obj) == nil && obj["subagent"] != nil
}

// userPrompt returns a user message's own text: injected context (environment, AGENTS.md,
// plugin lists) is not the user's.
func userPrompt(l line) string {
	switch l.Type {
	case "event_msg":
		var e struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(l.Payload, &e) == nil && e.Type == "user_message" {
			return userText(e.Message)
		}
	case "response_item":
		var r struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(l.Payload, &r) == nil && r.Type == "message" && r.Role == "user" {
			var parts []string
			for _, c := range r.Content {
				if c.Type == "input_text" {
					parts = append(parts, c.Text)
				}
			}
			return userText(strings.Join(parts, "\n"))
		}
	}
	return ""
}

func userText(s string) string {
	t := strings.TrimSpace(s)
	if t == "" || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "# AGENTS.md instructions") {
		return ""
	}
	return t
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}

// Bundle is the rollout, plus the rollout a forked thread's history starts in.
func (m *Module) Bundle(_ context.Context, h agent.Host, in agent.Install, s agent.Summary) (agent.Bundle, error) {
	pa := h.Path()
	root := in.Root(home)
	rel, ok := pa.Rel(root, s.Path)
	if !ok {
		return agent.Bundle{}, fmt.Errorf("%s is outside %s", s.Path, root)
	}
	fi, err := h.FS().Stat(s.Path)
	if err != nil {
		return agent.Bundle{}, err
	}
	b := agent.Bundle{Files: []agent.BundleFile{{Root: home, Rel: toSlash(rel), Size: fi.Size(), Role: agent.RoleMain, Rewrite: agent.RewriteJSONL, Growable: true}}}
	head, err := h.FS().ReadFile(s.Path, headChunk)
	if err != nil && !errors.Is(err, io.EOF) {
		head = nil
	}
	if mt, err := firstMeta(head); err == nil && mt.HistoryBase != nil && mt.HistoryBase.ThreadID != "" {
		// A fork reads its parent's rollout by byte offset: it travels unchanged.
		if files, err := rollouts(h, in); err == nil {
			for _, f := range files {
				if strings.HasSuffix(f.path, "-"+mt.HistoryBase.ThreadID+".jsonl") {
					if r, ok := pa.Rel(root, f.path); ok {
						b.Files = append(b.Files, agent.BundleFile{Root: home, Rel: toSlash(r), Size: f.info.Size(), Role: agent.RoleSide, Rewrite: agent.RewriteNone})
					}
				}
			}
		}
	}
	return b, nil
}

// PlanMove keeps every rollout at its relative path (Codex's index finds a thread by its
// file name, and an existing index row keeps its path). A keep-both copy gets a new id,
// which appears in the file name and inside many records.
func (m *Module) PlanMove(src, dst agent.Install, s agent.Summary, b agent.Bundle, p agent.Placement) (agent.MovePlan, error) {
	oldID, newID := string(p.SourceID), string(p.Key.Session)
	mp := agent.MovePlan{Policy: agent.RewritePolicy{Protect: []string{"encrypted_content"}}}
	if oldID != newID {
		mp.Policy.Rename = [2]string{oldID, newID}
	}
	for _, f := range b.Files {
		to := f.Rel
		if f.Role == agent.RoleMain && oldID != newID {
			to = path.Join(path.Dir(f.Rel), strings.Replace(path.Base(f.Rel), oldID, newID, 1))
		}
		mp.Files = append(mp.Files, agent.PlacedFile{From: f, ToRoot: home, ToRel: to})
	}
	return mp, nil
}

// Verify checks that the staged rollout starts in the target folder, as the target thread.
func (m *Module) Verify(_ context.Context, h agent.Host, mp agent.MovePlan, staged map[string]string, p agent.Placement) error {
	for _, f := range mp.Files {
		if f.From.Role != agent.RoleMain {
			continue
		}
		b, err := h.FS().ReadFile(staged[agent.StagedKey(f)], 1<<30)
		if err != nil {
			return err
		}
		mt, err := firstMeta(b)
		if err != nil {
			return err
		}
		if mt.CWD != p.CWD {
			return fmt.Errorf("the staged thread starts in %q, expected %q", mt.CWD, p.CWD)
		}
		if mt.ID != string(p.Key.Session) {
			return fmt.Errorf("the staged thread is %s, expected %s", mt.ID, p.Key.Session)
		}
		if mt.HistoryMode != "" && mt.HistoryMode != "legacy" && mt.HistoryMode != "paginated" {
			return &agent.VersionError{Agent: id, Have: "history mode " + mt.HistoryMode, Blocking: true}
		}
		return nil
	}
	return errors.New("no rollout was staged")
}

// Resume is `codex resume <id> [prompt]` (or `codex fork <id>`), run in the thread's folder.
func (m *Module) Resume(in agent.Install, key agent.SessionKey, p agent.Placement, o agent.ResumeOptions) agent.Command {
	verb := "resume"
	if o.Fork {
		verb = "fork"
	}
	argv := []string{"codex", verb, string(key.Session)}
	if o.Prompt != "" {
		argv = append(argv, o.Prompt)
	}
	return agent.Command{Argv: argv, Dir: p.CWD}
}

// Live probes the lock Codex holds on thread-writer-locks/<id>.lock while a thread is
// open (the file stays after Codex exits; only the lock means open).
func (m *Module) Live(ctx context.Context, h agent.Host, in agent.Install, ids []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	paths := make([]string, len(ids))
	for i, sid := range ids {
		paths[i] = h.Path().Join(in.Root(home), "thread-writer-locks", string(sid)+".lock")
	}
	states, err := h.Locks().Probe(ctx, paths)
	if err != nil {
		return nil, err
	}
	out := make(map[agent.SessionID]agent.LiveInfo, len(ids))
	for i, sid := range ids {
		switch states[paths[i]] {
		case agent.LockHeld:
			out[sid] = agent.LiveInfo{State: agent.Live}
		case agent.LockFree:
			out[sid] = agent.LiveInfo{State: agent.Ended}
		default:
			out[sid] = agent.LiveInfo{State: agent.Unknown}
		}
	}
	return out, nil
}

func toSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }
