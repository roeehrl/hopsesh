package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// liteChunk is how much of the head and the tail of a transcript is read, matching
// Claude Code's own session picker. Transcripts can exceed 100 MB.
const liteChunk = 64 * 1024

// quickChunk is the first, smaller read; enough for most sessions.
const quickChunk = 16 * 1024

// quickEnough reports whether the small head/tail already contain what the summary needs:
// a launch cwd in the head, and a title plus a last prompt in the tail.
func quickEnough(head, tail []byte) bool {
	hasTitle := bytes.Contains(tail, []byte(`"type":"custom-title"`)) || bytes.Contains(tail, []byte(`"type":"ai-title"`))
	hasPrompt := bytes.Contains(tail, []byte(`"type":"last-prompt"`))
	return hasTitle && hasPrompt && hasChainedCWD(parseLines(head, false))
}

// info is what a transcript's head and tail say about its session.
type info struct {
	ID          string
	File        string
	Title       string
	TitleSource string // custom | ai | summary | prompt | none
	Mark        *agent.Mark
	CWD         string // project directory: relocated cwd, else the launch cwd
	LastCWD     string // shell cwd of the newest record (may be a subdirectory)
	// WorktreeRoot is set when CWD is a Claude-managed worktree (<repo>/.claude/worktrees/<name>):
	// such transcripts stay in the repo root's project folder.
	WorktreeRoot string
	GitBranch    string
	LastPrompt   string
	Version      string
	LastActivity time.Time
	Size         int64
	IsSidechain  bool
	HasMessages  bool
	Subagents    int
	Mirror       *agent.CloudLink
}

// record is the subset of transcript fields the summary needs.
type record struct {
	Type             string          `json:"type"`
	UUID             string          `json:"uuid"`
	ParentUUID       *string         `json:"parentUuid"`
	SessionID        string          `json:"sessionId"`
	CWD              string          `json:"cwd"`
	GitBranch        string          `json:"gitBranch"`
	Version          string          `json:"version"`
	Timestamp        string          `json:"timestamp"`
	IsSidechain      bool            `json:"isSidechain"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	ToolUseResult    json.RawMessage `json:"toolUseResult"`
	Origin           *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	CustomTitle  string `json:"customTitle"`
	AITitle      string `json:"aiTitle"`
	Summary      string `json:"summary"`
	LastPrompt   string `json:"lastPrompt"`
	RelocatedCWD string `json:"relocatedCwd"`
	// A Remote Control link (bridge-session): the claude.ai copy, in cse_ form, and its owner.
	BridgeSessionID string `json:"bridgeSessionId"`
	OwnerOrg        string `json:"ownerOrganizationUuid"`
}

// summarize reads the head and tail of a transcript and derives what Claude Code's picker
// shows, with the same priorities. fi and hasSidecar are what a directory listing already
// knows (on a remote machine every avoided request is a network round trip); both may be
// nil.
func summarize(fsys agent.FS, pa agent.Path, file string, fi fs.FileInfo, hasSidecar *bool) (*info, error) {
	f, err := fsys.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi == nil {
		if fi, err = f.Stat(); err != nil {
			return nil, err
		}
	}
	size := fi.Size()
	// Read 16 KB first; metadata records are re-appended often, so the tail usually holds
	// the title and last prompt. Widen to Claude Code's own 64 KB only when needed.
	head, tail, err := readHeadTail(f, size, quickChunk)
	if err != nil {
		return nil, err
	}
	if size > 2*quickChunk && !quickEnough(head, tail) {
		if head, tail, err = readHeadTail(f, size, liteChunk); err != nil {
			return nil, err
		}
	}
	s := &info{
		ID:           strings.TrimSuffix(pa.Base(file), ".jsonl"),
		File:         file,
		Size:         size,
		LastActivity: fi.ModTime().UTC(),
	}
	headRecs := parseLines(head, false)
	tailRecs := headRecs
	if tail != nil {
		tailRecs = parseLines(tail, true)
	}
	if !hasChainedCWD(headRecs) {
		// The first records can exceed the 64 KB head (large pasted prompts or attachments);
		// stream forward, bounded, until the first record that carries the launch cwd.
		headRecs = append(headRecs, scanForFirstChained(f, size)...)
	}
	apply(s, headRecs, tailRecs)
	if hasSidecar == nil || *hasSidecar {
		if s.TitleSource == "" || s.TitleSource == "prompt" || s.TitleSource == "none" {
			if t := readTitleSidecar(fsys, pa, file, s.ID); t != "" {
				s.Title, s.TitleSource = t, "custom"
			}
		}
		s.Subagents = countSubagents(fsys, pa, file, s.ID)
	}
	if m, title, ok := agent.ParseMarkTitle(s.Title); ok {
		s.Mark = &m
		s.Title = title
		if s.Title == "" {
			s.Title = s.LastPrompt
		}
	}
	return s, nil
}

// readHeadTail reads the first and last 64 KB concurrently (two parallel requests on a
// remote filesystem instead of two sequential ones).
func readHeadTail(r io.ReaderAt, size int64, chunk int64) (head, tail []byte, err error) {
	n := size
	if n > chunk {
		n = chunk
	}
	head = make([]byte, n)
	if size <= chunk {
		if _, err = r.ReadAt(head, 0); err != nil && err != io.EOF {
			return nil, nil, err
		}
		return head, nil, nil
	}
	start := size - chunk
	if start < n {
		start = n
	}
	tail = make([]byte, size-start)
	var tailErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, e := r.ReadAt(tail, start); e != nil && e != io.EOF {
			tailErr = e
		}
	}()
	_, err = r.ReadAt(head, 0)
	<-done
	if err != nil && err != io.EOF {
		return nil, nil, err
	}
	if tailErr != nil {
		return nil, nil, tailErr
	}
	return head, tail, nil
}

// parseLines decodes complete JSON lines. A tail chunk starts mid-line, so its first
// line is skipped; a head chunk may end mid-line, which simply fails to decode.
func parseLines(b []byte, isTail bool) []record {
	lines := bytes.Split(b, []byte{'\n'})
	if isTail && len(lines) > 0 {
		lines = lines[1:]
	}
	out := make([]record, 0, len(lines))
	for _, ln := range lines {
		ln = bytes.TrimSpace(ln)
		if len(ln) == 0 || ln[0] != '{' {
			continue
		}
		var r record
		if json.Unmarshal(ln, &r) == nil && r.Type != "" {
			out = append(out, r)
		}
	}
	return out
}

func apply(s *info, head, tail []record) {
	all := append(append([]record{}, head...), tail...)
	// Title: custom (tail, then head) > ai > legacy summary > first real prompt.
	if t := lastString(tail, func(r record) string { return pick(r.Type == "custom-title", r.CustomTitle) }); t != "" {
		s.Title, s.TitleSource = t, "custom"
	} else if t := lastString(head, func(r record) string { return pick(r.Type == "custom-title", r.CustomTitle) }); t != "" {
		s.Title, s.TitleSource = t, "custom"
	} else if t := lastString(all, func(r record) string { return pick(r.Type == "ai-title", r.AITitle) }); t != "" {
		s.Title, s.TitleSource = t, "ai"
	} else if t := lastString(all, func(r record) string { return pick(r.Type == "summary", r.Summary) }); t != "" {
		s.Title, s.TitleSource = t, "summary"
	}
	firstPrompt := ""
	for _, r := range head {
		if p := realPrompt(r); p != "" {
			firstPrompt = p
			break
		}
	}
	if s.Title == "" {
		if firstPrompt != "" {
			s.Title, s.TitleSource = firstPrompt, "prompt"
		} else {
			s.TitleSource = "none"
		}
	}
	// Last prompt: newest last-prompt record, else newest real user prompt, else first.
	if p := agent.OwnText(lastString(tail, func(r record) string { return pick(r.Type == "last-prompt", r.LastPrompt) })); p != "" {
		s.LastPrompt = clip(oneLine(p))
	} else {
		for i := len(tail) - 1; i >= 0; i-- {
			if p := realPrompt(tail[i]); p != "" {
				s.LastPrompt = p
				break
			}
		}
		if s.LastPrompt == "" {
			s.LastPrompt = firstPrompt
		}
	}
	// Project cwd, as Claude Code keys the project folder: the newest relocation, else the
	// first chained record's cwd. Later records follow the shell into subdirectories.
	if c := lastString(all, func(r record) string { return pick(r.Type == "relocated", r.RelocatedCWD) }); c != "" {
		s.CWD = c
	} else {
		for _, r := range head {
			if isChained(r) && r.CWD != "" {
				s.CWD = r.CWD
				break
			}
		}
	}
	s.LastCWD = lastString(all, func(r record) string { return pick(isChained(r), r.CWD) })
	s.WorktreeRoot = worktreeRoot(s.CWD)
	if s.CWD == "" {
		s.CWD = s.LastCWD
	}
	s.GitBranch = lastString(all, func(r record) string { return pick(isChained(r), r.GitBranch) })
	s.Version = lastString(all, func(r record) string { return pick(isChained(r), r.Version) })
	for _, r := range head {
		if isChained(r) && r.IsSidechain {
			s.IsSidechain = true
			break
		}
	}
	for _, r := range all {
		if r.Type == "user" || r.Type == "assistant" {
			s.HasMessages = true
			break
		}
	}
	// Remote Control: the newest bridge record names the session's copy on claude.ai.
	for i := len(all) - 1; i >= 0; i-- {
		if r := all[i]; r.Type == "bridge-session" && canonical(r.BridgeSessionID) != "" {
			cid := agent.SessionID(canonical(r.BridgeSessionID))
			s.Mirror = &agent.CloudLink{Cloud: cloudName, ID: cid, URL: "https://claude.ai/code/" + string(cid), Account: accountKey(r.OwnerOrg)}
			break
		}
	}
	for i := len(all) - 1; i >= 0; i-- {
		r := all[i]
		if (r.Type == "user" || r.Type == "assistant") && r.Timestamp != "" {
			if ts, err := time.Parse(time.RFC3339Nano, r.Timestamp); err == nil {
				s.LastActivity = ts.UTC()
			}
			break
		}
	}
}

func hasChainedCWD(rs []record) bool {
	for _, r := range rs {
		if isChained(r) && r.CWD != "" {
			return true
		}
	}
	return false
}

// maxForwardScan bounds how far scanForFirstChained reads (huge first lines are rare).
const maxForwardScan = 32 << 20

func scanForFirstChained(f io.ReaderAt, size int64) []record {
	limit := size
	if limit > maxForwardScan {
		limit = maxForwardScan
	}
	br := bufio.NewReaderSize(io.NewSectionReader(f, 0, limit), 256*1024)
	var out []record
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var r record
			if json.Unmarshal(bytes.TrimSpace(line), &r) == nil && r.Type != "" {
				out = append(out, r)
				if isChained(r) && r.CWD != "" {
					return out
				}
			}
		}
		if err != nil {
			return out
		}
	}
}

func isChained(r record) bool {
	switch r.Type {
	case "user", "assistant", "system", "attachment":
		return true
	}
	return false
}

func pick(ok bool, v string) string {
	if ok {
		return v
	}
	return ""
}

func lastString(rs []record, f func(record) string) string {
	for i := len(rs) - 1; i >= 0; i-- {
		if v := f(rs[i]); v != "" {
			return v
		}
	}
	return ""
}

var (
	commandNameRE = regexp.MustCompile(`<command-name>\s*/?([^<\s]+)\s*</command-name>`)
	commandArgsRE = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	bashInputRE   = regexp.MustCompile(`(?s)<bash-input>(.*?)</bash-input>`)
	internalTagRE = regexp.MustCompile(`^\s*<(local-command-stdout|local-command-stderr|local-command-caveat|system-reminder|bash-stdout|bash-stderr|task-notification|cross-session-message|scheduled-task|tick)\b`)
)

// builtinCommands are slash commands whose invocation is not a meaningful "prompt".
var builtinCommands = map[string]bool{
	"clear": true, "compact": true, "resume": true, "model": true, "config": true, "help": true,
	"exit": true, "login": true, "logout": true, "cost": true, "status": true, "init": true,
	"memory": true, "permissions": true, "doctor": true, "rename": true, "cd": true, "add-dir": true,
	"remote-control": true, "rc": true, "branch": true, "rewind": true, "export": true, "context": true,
	"usage": true, "fast": true, "effort": true, "agents": true, "mcp": true, "hooks": true, "plugin": true,
}

// realPrompt returns the user's own prompt text for a record, or "" if the record is a
// tool result, meta message, compaction summary, peer/task notification or command noise.
func realPrompt(r record) string {
	if r.Type != "user" || r.IsMeta || r.IsCompactSummary || r.Message == nil {
		return ""
	}
	if len(r.ToolUseResult) > 0 && string(r.ToolUseResult) != "null" {
		return ""
	}
	if r.Origin != nil && r.Origin.Kind != "" && r.Origin.Kind != "human" {
		return ""
	}
	text := agent.OwnText(contentText(r.Message.Content))
	if text == "" {
		return ""
	}
	if m := commandNameRE.FindStringSubmatch(text); m != nil {
		name := m[1]
		args := ""
		if a := commandArgsRE.FindStringSubmatch(text); a != nil {
			args = strings.TrimSpace(a[1])
		}
		if builtinCommands[name] || args == "" {
			return ""
		}
		return clip(oneLine("/" + name + " " + args))
	}
	if m := bashInputRE.FindStringSubmatch(text); m != nil {
		return clip(oneLine("! " + strings.TrimSpace(m[1])))
	}
	if internalTagRE.MatchString(text) {
		return ""
	}
	return clip(oneLine(text))
}

// contentText extracts text from string content or from text blocks; tool_result
// blocks disqualify the message.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return ""
		}
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " ")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string) string {
	const max = 200
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

func readTitleSidecar(fsys agent.FS, pa agent.Path, file, id string) string {
	b, err := fsys.ReadFile(pa.Join(pa.Dir(file), id, "custom-title.json"), 64*1024)
	if err != nil {
		return ""
	}
	var v struct {
		CustomTitle string `json:"customTitle"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	return v.CustomTitle
}

func countSubagents(fsys agent.FS, pa agent.Path, file, id string) int {
	entries, err := fsys.ReadDir(pa.Join(pa.Dir(file), id, "subagents"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			n++
		}
	}
	return n
}

// worktreeRoot returns <repo> for a Claude-managed worktree path <repo>/.claude/worktrees/<name>[/...].
func worktreeRoot(cwd string) string {
	for _, sep := range []string{"/" + worktreesDir + "/", `\` + strings.ReplaceAll(worktreesDir, "/", `\`) + `\`} {
		if i := strings.Index(cwd, sep); i > 0 {
			return cwd[:i]
		}
	}
	return ""
}
