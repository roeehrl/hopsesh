package fakecloud

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
)

// Claude answers Claude Code's cloud flags: --cloud, -p … --cloud [<id>] [--output-format
// json] and --teleport <id>. handled is false for any other call (the stand-in claude
// answers those itself).
func Claude(p Proc) (handled bool, code int) {
	var (
		print, jsonOut, cloud, teleport bool
		cloudArg, teleportArg           string
		positional                      []string
	)
	for i := 0; i < len(p.Args); i++ {
		a := p.Args[i]
		next := func() string {
			if i+1 < len(p.Args) && !strings.HasPrefix(p.Args[i+1], "-") {
				i++
				return p.Args[i]
			}
			return ""
		}
		switch a {
		case "-p", "--print":
			print = true
		case "--output-format":
			jsonOut = next() == "json"
		case "--cloud", "--remote":
			cloud, cloudArg = true, next()
		case "--teleport":
			teleport, teleportArg = true, next()
		default:
			if !strings.HasPrefix(a, "-") {
				positional = append(positional, a)
			}
		}
	}
	if !cloud && !teleport {
		return false, 0
	}
	if teleport {
		return true, claudeTeleport(p, teleportArg)
	}
	if fail := p.fail(); fail == "signed-out" || fail == "not-eligible" {
		return true, claudeRefused(p, fail)
	}
	if isClaudeID(cloudArg) {
		if !print {
			// Documented: an existing session can only be attached from the web.
			return true, p.errorf(1, "Attaching to an existing cloud session is not enabled for your account.")
		}
		return true, claudeFollowUp(p, claudeID(cloudArg), strings.Join(positional, " "), jsonOut)
	}
	prompt := strings.Join(positional, " ")
	if prompt == "" {
		prompt = cloudArg
	}
	return true, claudeCreate(p, prompt, jsonOut)
}

// claudeRefused prints why the cloud refused (unverified wording; the API-key case is the
// one Claude Code's docs quote for teleport).
func claudeRefused(p Proc, fail string) int {
	if fail == "signed-out" {
		return p.errorf(1, "Error: Unable to get organization UUID. Cloud sessions need a claude.ai login (/login).")
	}
	return p.errorf(1, "Error: Claude Code on the web is not available for your plan or organization.")
}

func isClaudeID(s string) bool {
	return strings.HasPrefix(s, "session_") || strings.HasPrefix(s, "cse_") || strings.HasPrefix(s, "https://claude.ai/code/")
}

// claudeID is a session id in its session_ form: cse_ is the same id ("Same id,
// different prefix"), and a link ends in it.
func claudeID(s string) string {
	s = strings.TrimPrefix(s, "https://claude.ai/code/")
	if rest, ok := strings.CutPrefix(s, "cse_"); ok {
		return "session_" + rest
	}
	return s
}

// claudeOut prints a cloud result: {ok, session_id, url} with --output-format json
// (documented for follow-ups), a line otherwise (unverified).
func claudeOut(p Proc, s Session, jsonOut bool, what string) int {
	if jsonOut {
		b, _ := json.Marshal(map[string]any{"ok": true, "session_id": s.ID, "url": s.URL()})
		fmt.Fprintln(p.Stdout, string(b))
		return 0
	}
	fmt.Fprintf(p.Stdout, "%s: %s\n", what, s.URL())
	return 0
}

// claudeCreate starts a cloud session from the folder's GitHub remote, at its current
// branch (which must be pushed), or as an uploaded bundle when there is no GitHub remote
// or CCR_FORCE_BUNDLE=1.
func claudeCreate(p Proc, prompt string, jsonOut bool) int {
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	env := p.environ()
	repo, cloneURL, branch, head, err := repoOf(env, p.Dir)
	if err != nil {
		return p.errorf(1, "Error: %v", err)
	}
	s := Session{Cloud: ClaudeCloud, Title: clip(prompt, 60), Repo: repo, CloneURL: cloneURL, Branch: branch, Base: head, Code: "branch",
		Messages: []Message{{Role: "user", Text: prompt, Time: time.Now().UTC()}}}
	if p.fail() == "repo-mismatch" || !strings.HasPrefix(repo, "github.com/") || p.Env("CCR_FORCE_BUNDLE") == "1" {
		s.Code = "bundle"
	} else if !onRemote(env, p.Dir, branch) {
		// Documented: the cloud clones "your current branch, not your local checkout, so
		// push first". The wording is unverified.
		return p.errorf(1, "Error: branch %s is not on the remote; push it first.", branch)
	}
	s, err = st.Seed(s)
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	return claudeOut(p, s, jsonOut, "Created a cloud session")
}

// claudeFollowUp queues one message in a session.
func claudeFollowUp(p Proc, id, text string, jsonOut bool) int {
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	s, err := st.Get(id)
	if err != nil {
		return p.errorf(1, "Error: %v", err)
	}
	if s.State == StateArchived || p.fail() == "archived" {
		return p.errorf(1, "Error: This session is archived and can't take new messages.") // unverified wording
	}
	now := time.Now().UTC()
	s.Messages = append(s.Messages, Message{Role: "user", Text: text, Time: now})
	s.State, s.Updated = StateRunning, now
	if err := st.Put(s); err != nil {
		return p.errorf(1, "%v", err)
	}
	return claudeOut(p, s, jsonOut, "Sent to the cloud session")
}

// claudeTeleport brings a cloud session here as Claude Code does: in a checkout of the same
// repository with a clean tree, it fetches and checks out the session's branch and writes
// the conversation as a new local transcript under projects/<slug of the folder>, with a
// teleported-from record (the record and its messageCount were seen in an issue; the local
// session id scheme is unverified, so it is a new UUID here). FAKE_CLOUD_FAIL=partial
// writes only the first message, empty none (both with the record), no-branch skips the
// checkout with the message an issue quotes.
func claudeTeleport(p Proc, arg string) int {
	if arg == "" {
		return p.errorf(1, "Error: --teleport without a session opens a picker, which needs a terminal.")
	}
	if p.fail() == "signed-out" {
		return p.errorf(1, "Error: Unable to get organization UUID")
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	s, err := st.Get(claudeID(arg))
	if err != nil {
		return p.errorf(1, "Error: %v", err)
	}
	env := p.environ()
	repo, _, _, _, err := repoOf(env, p.Dir)
	if err != nil || repo != s.Repo || p.fail() == "repo-mismatch" {
		return p.errorf(1, "Error: run --teleport from a checkout of %s.", s.Repo) // unverified wording
	}
	if dirty, _ := git(env, p.Dir, "status", "--porcelain", "--untracked-files=no"); dirty != "" {
		return p.errorf(1, "Error: the working tree has uncommitted changes; commit or stash them first.") // unverified wording
	}
	branch := s.Result
	if branch == "" {
		branch = s.Branch
	}
	if p.fail() == "no-branch" || s.Result == "" && s.Code == "bundle" {
		fmt.Fprintf(p.Stdout, "Session resumed without branch: Failed to checkout branch '%s'\n", "claude/web-session-"+strings.ToLower(s.ID[len(s.ID)-6:]))
	} else {
		if _, err := git(env, p.Dir, "fetch", "-q", "origin", branch); err != nil {
			return p.errorf(1, "Error: %v", err)
		}
		if _, err := git(env, p.Dir, "checkout", "-q", "-B", branch, "FETCH_HEAD"); err != nil {
			return p.errorf(1, "Error: %v", err)
		}
	}
	msgs := s.Messages
	switch p.fail() {
	case "partial":
		msgs = msgs[:min(1, len(msgs))]
	case "empty":
		msgs = nil
	}
	count := len(s.Messages)
	if p.fail() == "empty" {
		count = 0
	}
	cfg := p.Env("CLAUDE_CONFIG_DIR")
	if cfg == "" {
		cfg = filepath.Join(p.Env("HOME"), ".claude")
	}
	cwd := p.Dir
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	local := newUUID()
	path := filepath.Join(cfg, "projects", claude.Slug(cwd), local+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return p.errorf(1, "%v", err)
	}
	rec := func(v map[string]any) string { b, _ := json.Marshal(v); return string(b) }
	lines := []string{rec(map[string]any{"type": "teleported-from", "remoteSessionId": s.ID, "messageCount": count, "sessionId": local})}
	parent := any(nil)
	for i, m := range msgs {
		uuid := fmt.Sprintf("t%d-%s", i, local[:8])
		r := map[string]any{"type": m.Role, "uuid": uuid, "parentUuid": parent, "sessionId": local, "cwd": cwd, "version": "2.1.284",
			"gitBranch": branch, "timestamp": m.Time.UTC().Format(time.RFC3339)}
		if m.Role == "user" {
			r["message"] = map[string]any{"role": "user", "content": m.Text}
		} else {
			r["message"] = map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": m.Text}}}
		}
		lines = append(lines, rec(r))
		parent = uuid
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return p.errorf(1, "%v", err)
	}
	fmt.Fprintf(p.Stdout, "Teleported %s: %d message(s) restored (%s)\n", s.ID, len(msgs), local) // unverified wording
	return 0
}

// Codex answers Codex's cloud commands: codex cloud exec|list|status|diff|apply and codex
// apply. handled is false for any other call.
func Codex(p Proc) (handled bool, code int) {
	if len(p.Args) == 0 {
		return false, 0
	}
	switch p.Args[0] {
	case "cloud":
		if len(p.Args) < 2 {
			return true, p.errorf(1, "Error: `codex cloud` without a command opens a picker, which needs a terminal.")
		}
		return true, codexCloud(p, p.Args[1], p.Args[2:])
	case "apply", "a":
		return true, codexApply(p, p.Args[1:])
	}
	return false, 0
}

// codexFlags reads --name value flags and positional arguments.
func codexFlags(args []string, valued ...string) (map[string]string, []string) {
	flags, pos := map[string]string{}, []string(nil)
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(a, "=")
		isValued := false
		for _, v := range valued {
			if name == v {
				isValued = true
			}
		}
		switch {
		case isValued && hasVal:
			flags[name] = val
		case isValued && i+1 < len(args):
			flags[name] = args[i+1]
			i++
		case strings.HasPrefix(a, "--"):
			flags[a] = "true"
		default:
			pos = append(pos, a)
		}
	}
	return flags, pos
}

// codexStatus is a task's status as `codex cloud list --json` spells it (pending, ready,
// applied, error, from the client's TaskStatus; the JSON spelling is unverified).
func codexStatus(s Session) string {
	switch {
	case s.Applied:
		return "applied"
	case s.State == StateRunning:
		return "pending"
	case s.State == StateFailed:
		return "error"
	}
	return "ready"
}

func codexCloud(p Proc, cmd string, args []string) int {
	switch fail := p.fail(); fail {
	case "signed-out":
		// Documented: cloud tasks need a ChatGPT login. The wording is unverified.
		return p.errorf(1, "Error: Not signed in with ChatGPT. Run `codex login` to use Codex cloud.")
	case "not-eligible":
		return p.errorf(1, "Error: Codex cloud is not available on your plan.")
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	switch cmd {
	case "exec":
		flags, pos := codexFlags(args, "--env", "--branch", "--attempts")
		if flags["--env"] == "" {
			return p.errorf(2, "error: the following required arguments were not provided:\n  --env <ENV_ID>")
		}
		if ok := p.Env("FAKE_CODEX_ENVS"); p.fail() == "no-env" || ok != "" && !contains(strings.Split(ok, ","), flags["--env"]) {
			return p.errorf(1, "Error: environment %s not found", flags["--env"]) // unverified wording
		}
		env := p.environ()
		repo, cloneURL, branch, _, err := repoOf(env, p.Dir)
		if err != nil {
			return p.errorf(1, "Error: %v", err)
		}
		if p.fail() == "repo-mismatch" || !strings.HasPrefix(repo, "github.com/") {
			return p.errorf(1, "Error: the environment is not connected to %s", repo) // unverified wording
		}
		if b := flags["--branch"]; b != "" {
			branch = b
		}
		if !onRemote(env, p.Dir, branch) {
			return p.errorf(1, "Error: branch %s is not on the remote", branch) // unverified wording
		}
		base, err := git(env, p.Dir, "ls-remote", "origin", "refs/heads/"+branch)
		if err != nil {
			return p.errorf(1, "Error: %v", err)
		}
		attempts, _ := strconv.Atoi(flags["--attempts"])
		prompt := strings.Join(pos, " ")
		s, err := st.Seed(Session{Cloud: CodexCloud, Title: clip(prompt, 60), Repo: repo, CloneURL: cloneURL, Branch: branch,
			Base: strings.Fields(base)[0], Code: "branch", Env: flags["--env"], Attempts: max(attempts, 1), Diff: p.Env("CODEX_STARTING_DIFF"),
			Messages: []Message{{Role: "user", Text: prompt, Time: time.Now().UTC()}}})
		if err != nil {
			return p.errorf(1, "%v", err)
		}
		fmt.Fprintln(p.Stdout, s.URL()) // unverified: the task's link
		return 0
	case "list":
		flags, _ := codexFlags(args, "--env", "--limit", "--cursor")
		if p.fail() == "slow" {
			time.Sleep(5 * time.Second)
		}
		all, err := st.List(CodexCloud)
		if err != nil {
			return p.errorf(1, "%v", err)
		}
		limit, _ := strconv.Atoi(flags["--limit"])
		if limit <= 0 || limit > 20 {
			limit = 20
		}
		var tasks []any
		for _, s := range all {
			if e := flags["--env"]; e != "" && s.Env != e {
				continue
			}
			if len(tasks) == limit {
				break
			}
			tasks = append(tasks, codexTask(s))
		}
		if p.fail() == "bad-record" {
			tasks = append(tasks, map[string]any{"id": 42, "status": []int{}})
		}
		if flags["--json"] == "" {
			for _, t := range tasks {
				if m, ok := t.(map[string]any); ok {
					fmt.Fprintf(p.Stdout, "%v  %v  %v\n", m["id"], m["status"], m["title"]) // unverified layout
				}
			}
			return 0
		}
		// Documented: a tasks array and an optional cursor.
		b, _ := json.Marshal(map[string]any{"tasks": tasks, "cursor": nil})
		fmt.Fprintln(p.Stdout, string(b))
		return 0
	case "status":
		_, pos := codexFlags(args)
		if len(pos) == 0 {
			return p.errorf(2, "error: the following required arguments were not provided:\n  <TASK_ID>")
		}
		s, err := st.Get(pos[0])
		if err != nil {
			return p.errorf(1, "Error: task %s not found", pos[0])
		}
		// Unverified layout: the help names the command only.
		fmt.Fprintf(p.Stdout, "%s\nstatus: %s\n%s\n", s.Title, codexStatus(s), s.URL())
		return 0
	case "diff":
		_, pos := codexFlags(args, "--attempt")
		if len(pos) == 0 {
			return p.errorf(2, "error: the following required arguments were not provided:\n  <TASK_ID>")
		}
		s, err := st.Get(pos[0])
		if err != nil {
			return p.errorf(1, "Error: task %s not found", pos[0])
		}
		if s.State == StateRunning || s.Diff == "" {
			return p.errorf(1, "Error: task %s has no diff yet", s.ID) // unverified wording
		}
		fmt.Fprint(p.Stdout, s.Diff)
		return 0
	case "apply":
		return codexApply(p, args)
	}
	return p.errorf(2, "error: unrecognized subcommand '%s'", cmd)
}

// codexTask is a task in `codex cloud list --json`'s documented fields.
func codexTask(s Session) map[string]any {
	summary := ""
	if n := len(s.Messages); n > 0 && s.Messages[n-1].Role == "assistant" {
		summary = s.Messages[n-1].Text
	}
	return map[string]any{"id": s.ID, "url": s.URL(), "title": s.Title, "status": codexStatus(s),
		"updated_at": s.Updated.UTC().Format(time.RFC3339), "environment_id": s.Env, "environment_label": s.Env + " (fake)",
		"summary": summary, "is_review": false, "attempt_total": s.Attempts}
}

// codexApply applies a task's diff to the folder with git apply (documented: it exits
// non-zero when git apply fails).
func codexApply(p Proc, args []string) int {
	_, pos := codexFlags(args, "--attempt")
	if len(pos) == 0 {
		return p.errorf(2, "error: the following required arguments were not provided:\n  <TASK_ID>")
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	s, err := st.Get(pos[0])
	if err != nil || s.Diff == "" || s.State == StateRunning {
		return p.errorf(1, "Error: task %s has no diff to apply", pos[0])
	}
	if err := applyDiff(p.environ(), p.Dir, s.Diff); err != nil {
		return p.errorf(1, "Error: %v", err)
	}
	s.Applied = true
	_ = st.Put(s)
	fmt.Fprintln(p.Stdout, "Successfully applied diff") // unverified wording
	return 0
}

// Exit codes of `fakecloud remote`, so a module can map them without reading words.
const (
	ExitSignedOut    = 4
	ExitNotEligible  = 5
	ExitRepoMismatch = 6
	ExitNotFound     = 7
)

// Remote is the fake's own third-party-style cloud CLI, the shape the cloud-only vendors'
// CLIs share (a prompt, a repository and a branch up; a branch and the messages down):
//
//	fakecloud remote new --repo R --branch B [--title T] PROMPT   → the session as JSON
//	fakecloud remote list [--known id,id]                         → the sessions as JSON
//	fakecloud remote pull ID                                      → the session with its messages
//	fakecloud remote message ID TEXT                              → the session as JSON
//	fakecloud remote archive ID
func Remote(p Proc) int {
	if len(p.Args) == 0 {
		return p.errorf(2, "usage: fakecloud remote new|list|pull|message|archive")
	}
	switch p.fail() {
	case "signed-out":
		return p.errorf(ExitSignedOut, "not signed in")
	case "not-eligible":
		return p.errorf(ExitNotEligible, "your plan has no cloud sessions")
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	flags, pos := codexFlags(p.Args[1:], "--repo", "--branch", "--title", "--known")
	out := func(v any) int {
		b, _ := json.Marshal(v)
		fmt.Fprintln(p.Stdout, string(b))
		return 0
	}
	get := func() (Session, int) {
		if len(pos) == 0 {
			return Session{}, p.errorf(2, "a session id is needed")
		}
		s, err := st.Get(pos[0])
		if err != nil || s.Cloud != FakeCloud {
			return Session{}, p.errorf(ExitNotFound, "no session %s", pos[0])
		}
		return s, 0
	}
	switch p.Args[0] {
	case "new":
		if p.fail() == "repo-mismatch" || !strings.HasPrefix(flags["--repo"], "github.com/") {
			return p.errorf(ExitRepoMismatch, "repository %s is not connected", flags["--repo"])
		}
		prompt := strings.Join(pos, " ")
		_, cloneURL, _, _, _ := repoOf(p.environ(), p.Dir) // when the folder is a checkout here
		s, err := st.Seed(Session{Cloud: FakeCloud, Title: nonEmpty(flags["--title"], clip(prompt, 60)), Repo: flags["--repo"], Branch: flags["--branch"],
			CloneURL: cloneURL, Code: "branch", Messages: []Message{{Role: "user", Text: prompt, Time: time.Now().UTC()}}})
		if err != nil {
			return p.errorf(1, "%v", err)
		}
		return out(s)
	case "list":
		if p.fail() == "slow" {
			time.Sleep(5 * time.Second)
		}
		all, err := st.List(FakeCloud)
		if err != nil {
			return p.errorf(1, "%v", err)
		}
		list := []any{}
		for _, s := range all {
			list = append(list, s)
		}
		if p.fail() == "bad-record" {
			list = append(list, map[string]any{"id": 42})
		}
		return out(list)
	case "pull":
		s, code := get()
		if code != 0 {
			return code
		}
		return out(s)
	case "message":
		s, code := get()
		if code != 0 {
			return code
		}
		if s.State == StateArchived || p.fail() == "archived" {
			return p.errorf(1, "session %s is archived", s.ID)
		}
		now := time.Now().UTC()
		s.Messages = append(s.Messages, Message{Role: "user", Text: strings.Join(pos[1:], " "), Time: now})
		s.State, s.Updated = StateRunning, now
		if err := st.Put(s); err != nil {
			return p.errorf(1, "%v", err)
		}
		return out(s)
	case "archive":
		s, code := get()
		if code != 0 {
			return code
		}
		s.State = StateArchived
		if err := st.Put(s); err != nil {
			return p.errorf(1, "%v", err)
		}
		return 0
	}
	return p.errorf(2, "unknown command %s", p.Args[0])
}

// Main is the fakecloud program: `fakecloud work [--same-branch] <id>` plays the cloud
// agent on a session; `fakecloud remote …` is the fake's own cloud CLI; --version answers.
func Main(p Proc) int {
	p.Log("fakecloud " + strings.Join(p.Args, " "))
	if len(p.Args) == 0 {
		return p.errorf(2, "usage: fakecloud work [--same-branch] <id> | fakecloud remote …")
	}
	switch p.Args[0] {
	case "--version":
		fmt.Fprintln(p.Stdout, "fakecloud 1.0.0")
		return 0
	case "work":
		same := len(p.Args) > 1 && p.Args[1] == "--same-branch"
		args := p.Args[1:]
		if same {
			args = args[1:]
		}
		if len(args) != 1 {
			return p.errorf(2, "usage: fakecloud work [--same-branch] <id>")
		}
		if err := Work(p, args[0], same); err != nil {
			return p.errorf(1, "fakecloud work: %v", err)
		}
		return 0
	case "remote":
		p.Args = p.Args[1:]
		return Remote(p)
	}
	return p.errorf(2, "unknown command %s", p.Args[0])
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
