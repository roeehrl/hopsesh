// Package codex is the OpenAI Codex module (CLI, IDE extension and desktop app share one
// store). Sessions ("threads") are append-only JSON-lines rollouts under
// $CODEX_HOME/sessions/YYYY/MM/DD/rollout-<local time>-<id>.jsonl. Codex also keeps SQLite
// indexes; hopsesh never touches them: Codex finds a copied-in rollout by id and indexes it
// itself.
package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
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

// iconSVG is the module's own mark for the window (drawn for hopsesh, not the vendor's
// logo); the installed desktop app's icon is preferred.
//
//go:embed icon.svg
var iconSVG string

// Spec declares Codex.
func (*Module) Spec() agent.Spec {
	return agent.Spec{
		Accounts:      &agent.ProfileSpec{RootEnv: []string{"CODEX_HOME", "CODEX_SQLITE_HOME"}, Unset: []string{"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL", "CODEX_APP_SERVER_ADDRESS"}, Login: []string{"login"}, InitialFiles: map[string]string{"config.toml": "cli_auth_credentials_store = \"keyring\"\n"}},
		ID:            id,
		DesktopScheme: "codex",
		Name:          "Codex",
		Vendor:        "OpenAI",
		Stability:     agent.Experimental,
		Tested:        []string{"0.153"},
		Binaries: []agent.Binary{{
			Name: "codex",
			Candidates: map[string][]string{
				"*":       {"~/.local/bin/codex", "/opt/homebrew/bin/codex", "/usr/local/bin/codex", "~/.npm-global/bin/codex"},
				"windows": {"~/AppData/Roaming/npm/codex.cmd", "~/.local/bin/codex.exe"},
			},
			VersionArgs: []string{"--version"},
		}},
		Roots:              []agent.Root{{Name: home, Env: []string{"CODEX_HOME"}, Default: map[string]string{"*": "~/.codex"}}},
		LoginEnv:           []string{"CODEX_HOME"},
		Secrets:            []string{"{home}/auth.json"},
		Instructions:       []string{"AGENTS.md"},
		GlobalInstructions: []string{"{home}/AGENTS.override.md", "{home}/AGENTS.md"},
		Tools:              "shell, apply_patch, update_plan",
		Features:           []agent.Capability{agent.CapFork, agent.CapApp},
		Icon: agent.Icon{SVG: iconSVG, Apps: map[string][]string{
			"darwin": {"/Applications/Codex.app", "~/Applications/Codex.app", "/Applications/ChatGPT.app", "~/Applications/ChatGPT.app"},
		}},
		Clouds: []agent.Cloud{cloud()},
	}
}

// Detect resolves CODEX_HOME and the codex binary.
func (m *Module) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	in := agent.DefaultInstall(m.Spec(), h)
	detectDesktop(h, &in)
	return in, nil
}

// meta is the first record of a rollout.
type meta struct {
	ForkedFromID                string          `json:"forked_from_id"`
	ForkedFromOrdinalExclusive  *uint64         `json:"forked_from_ordinal_exclusive"`
	ID                          string          `json:"id"`
	Timestamp                   string          `json:"timestamp"`
	CWD                         string          `json:"cwd"`
	Originator                  string          `json:"originator"`
	CLIVersion                  string          `json:"cli_version"`
	Source                      json.RawMessage `json:"source"`
	ModelProvider               string          `json:"model_provider"`
	SubagentHistoryStartOrdinal *uint64         `json:"subagent_history_start_ordinal"`
	HistoryMode                 string          `json:"history_mode"`
	HistoryBase                 *struct {
		ThreadID string `json:"thread_id"`
	} `json:"history_base"`
	Git *struct {
		Branch string `json:"branch"`
	} `json:"git"`
	CreatorAccountID string `json:"creator_account_id"`
}

// line is one rollout record.
type line struct {
	Ordinal      *uint64         `json:"ordinal,omitempty"`
	Timestamp    string          `json:"timestamp"`
	Type         string          `json:"type"`
	Payload      json.RawMessage `json:"payload"`
	compactBytes *int            // bounded analysis: replacement context size, without retaining its repeated payload
}

// List walks sessions/YYYY/MM/DD, newest first. Sub-agent threads are left out, as
// Codex's own pickers do.
func (m *Module) List(ctx context.Context, h agent.Host, in agent.Install) (agent.Listing, error) {
	files, err := rollouts(h, in)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return agent.Listing{}, nil
		}
		return agent.Listing{}, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].info.ModTime().After(files[j].info.ModTime()) })
	titles := names(h, in)
	titleSalt := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprint(titles))))
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
			sums[i], errs[i] = agent.ListingSummary(ctx, h, f.path, f.info, "codex-summary-1:"+titleSalt, nil, func() (*agent.Summary, error) {
				s, err := summarize(h, f)
				if s != nil {
					s.Key.Profile = in.ProfileID()
					if t := titles[string(s.Key.Session)]; t != "" {
						s.Title, s.TitleSource = t, "custom"
						if l, orig, ok := agent.StripLegacyLabel(t); ok {
							s.LegacyLabel, s.Title = &l, orig
						}
					}
				}
				return s, err
			})
			if s := sums[i]; s != nil {
				s.Key.Profile = in.ProfileID()
			}
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
				if l, orig, ok := agent.StripLegacyLabel(t); ok {
					s.LegacyLabel, s.Title = &l, orig
				}
			}
			out.Sessions = append(out.Sessions, *s)
		}
	}
	sort.Slice(out.Sessions, func(i, j int) bool { return out.Sessions[i].LastActivity.After(out.Sessions[j].LastActivity) })
	for i := range out.Sessions {
		out.Sessions[i].Key.Profile = in.ProfileID()
	}
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
		months, err := fsys.ReadDir(pa.Join(root, y.Name()))
		if err != nil {
			return out, err
		}
		for _, mo := range months {
			if !mo.IsDir() || !numeric(mo.Name()) {
				continue
			}
			days, err := fsys.ReadDir(pa.Join(root, y.Name(), mo.Name()))
			if err != nil {
				return out, err
			}
			for _, d := range days {
				if !d.IsDir() || !numeric(d.Name()) {
					continue
				}
				dir := pa.Join(root, y.Name(), mo.Name(), d.Name())
				entries, err := fsys.ReadDir(dir)
				if err != nil {
					return out, err
				}
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
	// Always an append, even to create it: the index is shared by every thread, and undo
	// takes out only this line.
	p := h.Path().Join(in.Root(home), "session_index.jsonl")
	return h.FS().Append(p, append(line, '\n'), agent.AppendOptions{NewLine: true, Standalone: true})
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
		NativeParent: agent.SessionID(first.ForkedFromID),
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
		if p = agent.PromptTitle(p); p != "" {
			s.Title, s.TitleSource = p, "prompt"
			break
		}
	}
	if s.Title == "" {
		for _, l := range lines(head, false) {
			role, text := previewMessage(l)
			if role != "assistant" {
				continue
			}
			text = agent.PreviewText(text)
			if text == "" {
				continue
			}
			text, _, _ = strings.Cut(text, "\n")
			text = strings.TrimLeft(text, "#>*- ")
			for _, end := range []string{". ", "! ", "? "} {
				if i := strings.Index(text, end); i >= 0 {
					text = text[:i+1]
				}
			}
			if title := agent.PromptTitle(strings.TrimRight(text, ".:")); title != "" {
				s.Title, s.TitleSource = title, "reply"
				break
			}
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
		if p = agent.OwnText(p); p != "" {
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
	for _, record := range tl {
		if record.Type != "event_msg" {
			continue
		}
		var event struct {
			Type    string          `json:"type"`
			Error   json.RawMessage `json:"error"`
			Message string          `json:"message"`
			Code    string          `json:"codex_error_info"`
		}
		if json.Unmarshal(record.Payload, &event) != nil {
			continue
		}
		if event.Type == "error" || event.Type == "task_complete" {
			body := string(event.Error) + " " + event.Message + " " + event.Code
			if strings.Contains(body, "context_window_exceeded") || strings.Contains(body, "ContextWindowExceeded") || strings.Contains(body, "ran out of room in the model's context window") {
				s.ContextOverflow = true
			} else if event.Type == "task_complete" && (len(event.Error) == 0 || string(event.Error) == "null") {
				s.ContextOverflow = false
			}
		}
	}
	if !talked && size <= headChunk {
		return nil, nil // a fully sampled file has no conversation yet
	}
	return s, nil
}

// readHead reads up to headChunk bytes from the start of a rollout, however large it is.
func readHead(h agent.Host, path string) ([]byte, error) {
	f, err := h.FS().Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, headChunk)
	n, err := f.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return head[:n], nil
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
	if !strings.ContainsAny(string(s.Key.Session), "/\\") && s.Key.Session != ".." && s.Key.Session != "." && s.Key.Session != "" {
		archiveRel := "hopsesh/archives/" + string(s.Key.Session) + ".jsonl"
		if archive, err := h.FS().Stat(pa.Join(root, archiveRel)); err == nil {
			b.Files = append(b.Files, agent.BundleFile{Root: home, Rel: archiveRel, Size: archive.Size(), Role: agent.RoleSide, Rewrite: agent.RewriteJSONL})
		} else if !errors.Is(err, fs.ErrNotExist) {
			return agent.Bundle{}, fmt.Errorf("preserved history: %w", err)
		}
	}

	head, _ := readHead(h, s.Path)
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
		if f.Rel == "hopsesh/archives/"+oldID+".jsonl" {
			to = "hopsesh/archives/" + newID + ".jsonl"
		}
		if f.Role == agent.RoleMain && oldID != newID {
			to = path.Join(path.Dir(f.Rel), strings.Replace(path.Base(f.Rel), oldID, newID, 1))
		}
		mp.Files = append(mp.Files, agent.PlacedFile{From: f, ToRoot: home, ToRel: to})
	}
	return mp, nil
}

// Verify checks that the staged rollout starts in the target folder, as the target thread.
func (m *Module) Verify(ctx context.Context, h agent.Host, mp agent.MovePlan, staged map[string]string, p agent.Placement) error {
	for _, f := range mp.Files {
		if f.From.Role != agent.RoleMain {
			continue
		}
		b, err := agent.ReadNative(ctx, h.FS(), staged[agent.StagedKey(f)])
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
	in.Accounts = m.Spec().Accounts
	if o.App {
		if m.CheckApp(in, key, o) != nil {
			return agent.Command{}
		}
		return desktopCommand(in, key)
	}
	verb := "resume"
	if o.Fork {
		verb = "fork"
	}
	argv := []string{"codex", verb, string(key.Session)}
	if o.Prompt != "" {
		argv = append(argv, o.Prompt)
	}
	return in.ScopeCommand(agent.Command{Argv: argv, Dir: p.CWD})
}

// Live probes the lock Codex holds on thread-writer-locks/<id>.lock while a thread is
// open (the file stays after Codex exits; only the lock means open). The processes holding
// an open thread's lock are its Procs, the first of them its PID; a machine that cannot
// name lock holders leaves them out.
func (m *Module) Live(ctx context.Context, h agent.Host, in agent.Install, ids []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	paths := make([]string, len(ids))
	for i, sid := range ids {
		paths[i] = lockPath(h, in, sid)
	}
	states, err := h.Locks().Probe(ctx, paths)
	if err != nil {
		return nil, err
	}
	var held []string
	for _, p := range paths {
		if states[p] == agent.LockHeld {
			held = append(held, p)
		}
	}
	holders := lockHolders(ctx, h, held)
	var files map[agent.SessionID]string
	out := make(map[agent.SessionID]agent.LiveInfo, len(ids))
	for i, sid := range ids {
		switch states[paths[i]] {
		case agent.LockHeld:
			if files == nil {
				files = rolloutPaths(h, in)
			}
			status := "idle"
			if busy, err := turnOpen(h, files[sid]); err == nil && busy {
				status = "working"
			}
			li := agent.LiveInfo{State: agent.Live, Status: status}
			seen := map[int]bool{}
			for _, pid := range holders[paths[i]] {
				if pid <= 0 || seen[pid] {
					continue
				}
				seen[pid] = true
				li.Procs = append(li.Procs, agent.LiveProc{PID: pid, ObservedAt: time.Now().UTC()})
			}
			if len(li.Procs) > 0 {
				li.PID = li.Procs[0].PID
			}
			out[sid] = li
		case agent.LockFree:
			out[sid] = agent.LiveInfo{State: agent.Ended}
		default:
			out[sid] = agent.LiveInfo{State: agent.Unknown}
		}
	}
	return out, nil
}

// lockHolders names the codex processes holding each held lock, best effort: none when the
// machine cannot tell. Holders found by open files (lsof) can include other programs, so
// only processes named codex (any case: the Codex app too) are kept when names are known.
func lockHolders(ctx context.Context, h agent.Host, held []string) map[string][]int {
	if len(held) == 0 {
		return nil
	}
	holders, err := h.Locks().Holders(ctx, held)
	if err != nil {
		return nil
	}
	var pids []int
	for _, ps := range holders {
		pids = append(pids, ps...)
	}
	names, err := h.Procs().Names(ctx, pids)
	if err != nil {
		return holders
	}
	out := make(map[string][]int, len(holders))
	for p, ps := range holders {
		for _, pid := range ps {
			if strings.HasPrefix(strings.ToLower(names[pid]), "codex") && !slices.Contains(out[p], pid) {
				out[p] = append(out[p], pid)
			}
		}
	}
	return out
}

func lockPath(h agent.Host, in agent.Install, sid agent.SessionID) string {
	return h.Path().Join(in.Root(home), "thread-writer-locks", string(sid)+".lock")
}

// rolloutPaths maps thread ids to their rollout files.
func rolloutPaths(h agent.Host, in agent.Install) map[agent.SessionID]string {
	out := map[agent.SessionID]string{}
	files, _ := rollouts(h, in)
	for _, r := range files {
		// rollout-<local time>-<thread id>.jsonl; the id is the trailing UUID.
		if n := strings.TrimSuffix(h.Path().Base(r.path), ".jsonl"); len(n) > 36 {
			out[agent.SessionID(n[len(n)-36:])] = r.path
		}
	}
	return out
}

// turnOpen reports whether the thread's last turn started and has not ended: Codex is
// working on it right now.
func turnOpen(h agent.Host, path string) (bool, error) {
	if path == "" {
		return false, fs.ErrNotExist
	}
	f, err := h.FS().Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	start := max(fi.Size()-tailChunk, 0)
	tail := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(tail, start); err != nil && err != io.EOF {
		return false, err
	}
	open := false
	for _, l := range lines(tail, start > 0) {
		if l.Type != "event_msg" {
			continue
		}
		var e struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(l.Payload, &e) != nil {
			continue
		}
		switch e.Type {
		case "task_started":
			open = true
		case "task_complete", "turn_aborted":
			open = false
		}
	}
	return open, nil
}

// ErrBusy means Codex is in the middle of a turn in that thread.
var ErrBusy = errors.New("the Codex session is working right now; let it finish (or stop it with Esc), then try again")

// ErrStillRunning means a stopped thread's process did not exit in time.
var ErrStillRunning = errors.New("the Codex session did not exit in time; quit it yourself (Ctrl+C twice, or /quit), then try again")

// Stop quits the Codex process that has the thread open. Codex installs no handler for
// SIGTERM, so it is sent only between turns (Codex writes every item to the rollout as
// it goes, so nothing finished is lost), and only to a process named codex that holds the
// thread's writer lock.
func (m *Module) Stop(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, grace time.Duration) error {
	lock := lockPath(h, in, s.Key.Session)
	holders, err := h.Locks().Holders(ctx, []string{lock})
	if err != nil {
		return err
	}
	pids := holders[lock]
	if len(pids) == 0 {
		return nil // already gone
	}
	path := s.Path
	if path == "" {
		path = rolloutPaths(h, in)[s.Key.Session]
	}
	if busy, err := turnOpen(h, path); err == nil && busy {
		return ErrBusy
	}
	names, err := h.Procs().Names(ctx, pids)
	if err != nil {
		return err
	}
	var targets []int
	for _, pid := range pids {
		if strings.HasPrefix(names[pid], "codex") {
			targets = append(targets, pid)
		}
	}
	if len(targets) == 0 {
		return fmt.Errorf("the session's lock is held by another program (%v), not codex; quit it yourself", names)
	}
	for _, pid := range targets {
		if err := h.Procs().Terminate(ctx, pid); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		st, err := h.Locks().Probe(ctx, []string{lock})
		if err != nil {
			return err
		}
		if st[lock] != agent.LockHeld {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return ErrStillRunning
}

func toSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// SessionWatchPaths excludes logs, credentials and unrelated caches.
func (m *Module) SessionWatchPaths(in agent.Install, pa agent.Path) []string {
	root := in.Root(home)
	if root == "" {
		return nil
	}
	return []string{pa.Join(root, "sessions"), pa.Join(root, "archived_sessions"), pa.Join(root, "session_index.jsonl"), pa.Join(root, "hopsesh")}
}
