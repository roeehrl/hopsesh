package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
)

// The helper does on one machine what a scenario needs there: make a repository and a
// session in it, add a turn, and report which sessions contain what. The runner calls it
// in-process on this machine and as `hsmatrix agent <op>` over ssh on the other (JSON on
// stdin and stdout), so both sides are prepared and checked the same way.

// SeedReq makes a repository (if missing) and one session in it.
type SeedReq struct {
	Agent   string `json:"agent"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Text    string `json:"text"`    // the user's message (with the row's marker)
	Large   bool   `json:"large"`   // pad the conversation to about 2 MB
	Repo    string `json:"repo"`    // repository name
	State   string `json:"state"`   // clean | unpushed | uncommitted | worktree | none
	Session bool   `json:"session"` // false: only the repository
}

// SeedRes says where things are.
type SeedRes struct {
	Cwd  string `json:"cwd"`
	Head string `json:"head,omitempty"`
	File string `json:"file,omitempty"`
}

// FindReq asks which sessions contain a marker.
type FindReq struct {
	Marker  string   `json:"marker"`
	Needles []string `json:"needles"`
}

// Found is one session that contains the marker.
type Found struct {
	SHA256 string            `json:"sha256"`
	Graph  *lineage.Manifest `json:"lineage,omitempty"`
	Agent  string            `json:"agent"`
	ID     string            `json:"id"`
	Path   string            `json:"path"`
	Bytes  int64             `json:"bytes"`
	Has    map[string]bool   `json:"has"`
	Mark   string            `json:"mark"` // the mark title, or ""
}

// AppendReq adds a user turn to a session file.
type AppendReq struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
	ID    string `json:"id"`
	Text  string `json:"text"`
}

// RemoveReq removes only a discovered fixture transcript, retaining its receipt.
// A missing original can then coexist with a surviving replica of its branch.
type RemoveReq struct {
	Agent  string `json:"agent"`
	ID     string `json:"id"`
	Marker string `json:"marker"`
}

func removeSession(r RemoveReq) error {
	if r.Marker == "" || r.ID == "" {
		return fmt.Errorf("remove requires a fixture marker and session ID")
	}
	found, err := find(FindReq{Marker: r.Marker})
	if err != nil {
		return err
	}
	for _, f := range found {
		if f.Agent == r.Agent && f.ID == r.ID {
			return os.Remove(f.Path)
		}
	}
	return fmt.Errorf("fixture session %s/%s not found", r.Agent, r.ID)
}

// HeadReq asks for a repository's commit.
type HeadReq struct {
	Dir string `json:"dir"`
}

func homeDir() string { h, _ := os.UserHomeDir(); return h }

// base is where this machine's scenario repositories live.
// It is resolved (no symlinks), as agents record the folders they run in.
func base() string {
	b := os.Getenv("HSMATRIX_BASE")
	if b == "" {
		b = filepath.Join(homeDir(), "hsm")
	}
	_ = os.MkdirAll(b, 0o755)
	if r, err := filepath.EvalSymlinks(b); err == nil {
		return r
	}
	return b
}

func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(homeDir(), ".claude")
}

func codexDir() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	return filepath.Join(homeDir(), ".codex")
}

func git(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// fixed makes the first commit identical on every machine (same hash), as a clone would be.
var fixed = []string{"GIT_AUTHOR_NAME=hsmatrix", "GIT_AUTHOR_EMAIL=hsmatrix@example.invalid", "GIT_COMMITTER_NAME=hsmatrix",
	"GIT_COMMITTER_EMAIL=hsmatrix@example.invalid", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z"}

func seed(r SeedReq) (SeedRes, error) {
	var res SeedRes
	if r.State == "none" {
		res.Cwd = filepath.Join(base(), "loose-"+r.ID[:8])
		if err := os.MkdirAll(res.Cwd, 0o755); err != nil {
			return res, err
		}
	} else {
		dir := filepath.Join(base(), r.Repo)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return res, err
			}
			if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+r.Repo+"\n"), 0o644); err != nil {
				return res, err
			}
			for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"add", "README.md"}, {"commit", "-q", "-m", "start"},
				{"remote", "add", "origin", "https://example.invalid/hsm/" + r.Repo + ".git"}} {
				if _, err := git(dir, fixed, a...); err != nil {
					return res, err
				}
			}
		}
		res.Cwd = dir
		who := []string{"GIT_AUTHOR_NAME=hsmatrix", "GIT_AUTHOR_EMAIL=hsmatrix@example.invalid", "GIT_COMMITTER_NAME=hsmatrix", "GIT_COMMITTER_EMAIL=hsmatrix@example.invalid"}
		switch r.State {
		case "unpushed":
			if err := os.WriteFile(filepath.Join(dir, "work-"+r.ID[:8]+".txt"), []byte(r.ID+"\n"), 0o644); err != nil {
				return res, err
			}
			if _, err := git(dir, who, "add", "-A"); err != nil {
				return res, err
			}
			if _, err := git(dir, who, "commit", "-q", "-m", "work on "+r.ID[:8]); err != nil {
				return res, err
			}
		case "uncommitted":
			if err := os.WriteFile(filepath.Join(dir, "draft-"+r.ID[:8]+".txt"), []byte("not committed\n"), 0o644); err != nil {
				return res, err
			}
		case "worktree":
			wt := filepath.Join(dir, ".claude", "worktrees", r.ID[:8])
			if _, err := git(dir, who, "worktree", "add", "-q", "-b", "feat/"+r.ID[:8], wt); err != nil {
				return res, err
			}
			res.Cwd = wt
		}
		h, err := git(res.Cwd, nil, "rev-parse", "HEAD")
		if err != nil {
			return res, err
		}
		res.Head = h
	}
	if !r.Session {
		// An empty native destination still needs the vendor's data directory.
		// Journey rows prepare every endpoint before its first import, without
		// seeding an extra conversation that could hide duplicate arrivals.
		var directory string
		switch r.Agent {
		case "claude":
			directory = claudeDir()
		case "codex":
			directory = filepath.Join(codexDir(), "sessions")
		case "":
		default:
			return res, fmt.Errorf("unknown agent %q", r.Agent)
		}
		if directory != "" {
			if err := os.MkdirAll(directory, 0700); err != nil {
				return res, err
			}
		}
		return res, nil
	}
	var err error
	switch r.Agent {
	case "claude":
		res.File, err = claudeSession(r, res.Cwd)
	case "codex":
		res.File, err = codexThread(r, res.Cwd)
	default:
		err = fmt.Errorf("unknown agent %q", r.Agent)
	}
	return res, err
}

func padding(r SeedReq) []string {
	if !r.Large {
		return nil
	}
	line := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40)
	var out []string
	for i := 0; i < 1200; i++ {
		out = append(out, fmt.Sprintf("step %d: %s", i, line))
	}
	return out
}

func writeJSONL(path string, recs []map[string]any, mtime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(path, mtime, mtime)
}

func claudeSession(r SeedReq, cwd string) (string, error) {
	t0 := time.Now().UTC().Add(-2 * time.Hour)
	ts := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	file := filepath.Join(cwd, "src", "main.go")
	rec := func(m map[string]any) map[string]any {
		m["sessionId"], m["cwd"], m["version"], m["entrypoint"] = r.ID, cwd, "2.1.284", "cli"
		return m
	}
	recs := []map[string]any{
		rec(map[string]any{"type": "user", "uuid": "u1", "parentUuid": nil, "timestamp": ts(0),
			"message": map[string]any{"role": "user", "content": r.Text + " (" + file + ")"}}),
		rec(map[string]any{"type": "assistant", "uuid": "a1", "parentUuid": "u1", "timestamp": ts(time.Minute),
			"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Edited " + file}}}}),
	}
	parent := "a1"
	for i, p := range padding(r) {
		id := fmt.Sprintf("p%d", i)
		recs = append(recs, rec(map[string]any{"type": "assistant", "uuid": id, "parentUuid": parent, "timestamp": ts(2 * time.Minute),
			"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": p}}}}))
		parent = id
	}
	recs = append(recs, map[string]any{"type": "custom-title", "customTitle": r.Title, "sessionId": r.ID})
	path := filepath.Join(claudeDir(), "projects", claude.Slug(cwd), r.ID+".jsonl")
	return path, writeJSONL(path, recs, t0.Add(time.Hour))
}

func codexThread(r SeedReq, cwd string) (string, error) {
	t0 := time.Now().UTC().Add(-2 * time.Hour)
	ts := func(d time.Duration) string { return t0.Add(d).Format("2006-01-02T15:04:05.000Z") }
	file := filepath.Join(cwd, "src", "main.go")
	recs := []map[string]any{
		{"timestamp": ts(0), "type": "session_meta", "payload": map[string]any{"id": r.ID, "timestamp": ts(0), "cwd": cwd,
			"originator": "codex_cli_rs", "cli_version": "0.153.2", "source": "cli", "model_provider": "openai", "history_mode": "paginated"}},
		{"timestamp": ts(time.Second), "type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "t1"}},
		{"timestamp": ts(2 * time.Second), "type": "turn_context", "payload": map[string]any{"cwd": cwd, "approval_policy": "on-request",
			"sandbox_policy": map[string]any{"type": "workspace-write", "writable_roots": []string{cwd}, "network_access": false}, "model": "gpt-5.6", "summary": "auto"}},
		{"timestamp": ts(3 * time.Second), "type": "response_item", "payload": map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": r.Text + " (" + file + ")"}}}},
		{"timestamp": ts(time.Minute), "type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "Edited " + file}}}},
	}
	for _, p := range padding(r) {
		recs = append(recs, map[string]any{"timestamp": ts(2 * time.Minute), "type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": p}}}})
	}
	recs = append(recs, map[string]any{"timestamp": ts(3 * time.Minute), "type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": "t1"}})
	dir := filepath.Join(codexDir(), "sessions", t0.Format("2006"), t0.Format("01"), t0.Format("02"))
	path := filepath.Join(dir, "rollout-"+t0.Format("2006-01-02T15-04-05")+"-"+r.ID+".jsonl")
	for i := range recs {
		recs[i]["ordinal"] = i
	}
	if err := writeJSONL(path, recs, t0.Add(time.Hour)); err != nil {
		return "", err
	}
	idx, err := os.OpenFile(filepath.Join(codexDir(), "session_index.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer idx.Close()
	b, _ := json.Marshal(map[string]any{"id": r.ID, "thread_name": r.Title, "updated_at": t0.Format(time.RFC3339)})
	_, err = idx.Write(append(b, '\n'))
	return path, err
}

// codexNames is each thread's current name (the index's last line for it wins).
func codexNames() map[string]string {
	names := map[string]string{}
	f, err := os.Open(filepath.Join(codexDir(), "session_index.jsonl"))
	if err != nil {
		return names
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.ID != "" {
			names[l.ID] = l.Name
		}
	}
	return names
}

// claudeMark is the last custom-title of a transcript when it is a mark.
func claudeMark(text string) string {
	mark := ""
	for _, line := range strings.Split(text, "\n") {
		var r struct {
			Type  string `json:"type"`
			Title string `json:"customTitle"`
		}
		if json.Unmarshal([]byte(line), &r) == nil && r.Type == "custom-title" {
			mark = ""
			if strings.HasPrefix(r.Title, "↪ ") {
				mark = r.Title
			}
		}
	}
	return mark
}

func find(r FindReq) ([]Found, error) {
	var out []Found
	names := codexNames()
	look := func(agent, root string, idOf func(path string) string) error {
		return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") || strings.HasSuffix(p, "session_index.jsonl") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil || !strings.Contains(string(b), r.Marker) {
				return nil
			}
			text := string(b)
			f := Found{SHA256: fmt.Sprintf("%x", sha256.Sum256(b)), Agent: agent, ID: idOf(p), Path: p, Bytes: int64(len(b)), Has: map[string]bool{}}
			for _, n := range r.Needles {
				f.Has[n] = strings.Contains(text, n) || strings.Contains(text, jsonEscape(n))
			}
			if agent == "claude" {
				f.Mark = claudeMark(text)
			} else if strings.HasPrefix(names[f.ID], "↪ ") {
				f.Mark = names[f.ID]
			}
			f.Graph, err = lineage.Read(host.LocalFS(), p)
			if err != nil {
				return fmt.Errorf("lineage beside %s: %w", p, err)
			}
			out = append(out, f)
			return nil
		})
	}
	if err := look("claude", filepath.Join(claudeDir(), "projects"), func(p string) string {
		return strings.TrimSuffix(filepath.Base(p), ".jsonl")
	}); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := look("codex", filepath.Join(codexDir(), "sessions"), func(p string) string {
		b := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		if len(b) >= 36 {
			return b[len(b)-36:]
		}
		return b
	}); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return out, nil
}

// jsonEscape is s as it appears inside a JSON string (paths on Windows have \\).
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func appendTurn(r AppendReq) error {
	var rec map[string]any
	now := time.Now().UTC()
	if r.Agent == "claude" {
		data, err := os.ReadFile(r.Path)
		if err != nil {
			return err
		}
		var parent string
		for _, line := range strings.Split(string(data), "\n") {
			var existing struct {
				Type      string `json:"type"`
				UUID      string `json:"uuid"`
				Leaf      string `json:"leafUuid"`
				Sidechain bool   `json:"isSidechain"`
			}
			if json.Unmarshal([]byte(line), &existing) != nil {
				continue
			}
			if (existing.Type == "user" || existing.Type == "assistant") && !existing.Sidechain {
				parent = existing.UUID
			}
			if existing.Type == "last-prompt" && existing.Leaf != "" {
				parent = existing.Leaf
			}
		}
		rec = map[string]any{"type": "user", "uuid": newID()[:8], "parentUuid": parent, "sessionId": r.ID, "timestamp": now.Format(time.RFC3339Nano),
			"message": map[string]any{"role": "user", "content": r.Text}}
	} else {
		rec = map[string]any{"timestamp": now.Format("2006-01-02T15:04:05.000Z"), "type": "response_item",
			"payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": r.Text}}}}
	}
	if r.Agent == "codex" {
		raw, err := os.ReadFile(r.Path)
		if err != nil {
			return err
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		var first struct {
			Payload struct {
				Mode string `json:"history_mode"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
			return err
		}
		if first.Payload.Mode == "paginated" {
			rec["ordinal"] = len(lines)
		}
	}
	b, _ := json.Marshal(rec)
	if r.Agent == "claude" {
		prompt, _ := json.Marshal(map[string]any{"type": "last-prompt", "leafUuid": rec["uuid"], "lastPrompt": r.Text, "sessionId": r.ID})
		b = append(append(b, '\n'), prompt...)
	}
	f, err := os.OpenFile(r.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// helperMain runs `hsmatrix agent <op>`: a request on stdin, the answer on stdout.
func helperMain(op string) error {
	dec := json.NewDecoder(os.Stdin)
	var out any
	var err error
	switch op {
	case "command":
		var r commandReq
		if err = dec.Decode(&r); err == nil {
			out, err = matrixCommand(r)
		}
	case "seed":
		var r SeedReq
		if err = dec.Decode(&r); err == nil {
			out, err = seed(r)
		}
	case "find":
		var r FindReq
		if err = dec.Decode(&r); err == nil {
			out, err = find(r)
		}
	case "append":
		var r AppendReq
		if err = dec.Decode(&r); err == nil {
			err = appendTurn(r)
			out = map[string]any{}
		}
	case "remove":
		var r RemoveReq
		if err = dec.Decode(&r); err == nil {
			err = removeSession(r)
			out = map[string]any{}
		}
	case "head":
		var r HeadReq
		if err = dec.Decode(&r); err == nil {
			var h string
			h, err = git(r.Dir, nil, "rev-parse", "HEAD")
			out = map[string]string{"head": h}
		}
	case "base":
		info := map[string]string{"base": base(), "home": homeDir(), "os": runtime.GOOS}
		if build, ok := debug.ReadBuildInfo(); ok {
			info["helperRevision"], info["helperModified"] = matrixBuildSource(build)
		}
		if path, e := exec.LookPath("hopsesh"); e == nil {
			if build, e := buildinfo.ReadFile(path); e == nil {
				info["appRevision"], info["appModified"] = matrixBuildSource(build)
			}
		}
		out = info
	default:
		err = fmt.Errorf("unknown helper op %q", op)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

func matrixBuildSource(build *debug.BuildInfo) (revision, modified string) {
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	return
}

// Only the disposable matrix helper exposes this operation. Structured argv avoids
// a second remote shell parsing paths and session text on different OS families.
type commandReq struct {
	Args []string `json:"args"`
}

func matrixCommand(r commandReq) (json.RawMessage, error) {
	if len(r.Args) == 0 {
		return nil, fmt.Errorf("missing matrix command")
	}
	switch r.Args[0] {
	case "hosts", "trust", "receive", "pull", "push":
	case "accounts":
		if len(r.Args) != 2 || r.Args[1] != "scan" {
			return nil, fmt.Errorf("only disposable account discovery is supported")
		}
	default:
		return nil, fmt.Errorf("unsupported matrix command %q", r.Args[0])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "hopsesh", r.Args...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("matrix command %s: %w: %s", r.Args[0], err, tail(string(b), 4000))
	}
	if r.Args[0] == "pull" || r.Args[0] == "push" {
		if !json.Valid(b) {
			return nil, fmt.Errorf("matrix movement returned invalid JSON: %s", tail(string(b), 4000))
		}
		return b, nil
	}
	return json.RawMessage(`{}`), nil
}
