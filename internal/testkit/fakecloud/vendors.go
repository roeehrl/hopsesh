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
// json] and --teleport <id>, and what hopsesh asks before using them: auth status --json
// and --help. handled is false for any other call (the stand-in claude answers those
// itself).
func Claude(p Proc) (handled bool, code int) {
	switch strings.Join(p.Args, " ") {
	case "auth status --json":
		return true, claudeAuth(p)
	case "--help":
		// The lines hopsesh looks for, as 2.1.284's help words them.
		fmt.Fprintln(p.Stdout, "Usage: claude [options] [command] [prompt]")
		fmt.Fprintln(p.Stdout, "  --cloud [description|session_id|url]  Create a cloud session, or send a message to one with -p")
		fmt.Fprintln(p.Stdout, "  --teleport [session]                  Resume a teleport session, optionally specify session ID")
		fmt.Fprintln(p.Stdout, "  -r, --resume [value]                  Resume a conversation by session ID")
		return true, 0
	}
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
	if p.Env("ANTHROPIC_API_KEY") != "" {
		// An API key in the environment takes the place of the claude.ai login.
		return true, claudeRefused(p, "signed-out")
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

// claudeAuth answers auth status --json in the fields hopsesh reads: a claude.ai Max login
// of the organisation $FAKE_CLAUDE_ORG (org-fake-0001 by default); signed-out is an API key
// login and not-eligible a free plan (both unverified wordings of the real output).
func claudeAuth(p Proc) int {
	org := p.Env("FAKE_CLAUDE_ORG")
	if org == "" {
		org = "org-fake-0001"
	}
	st := map[string]any{"loggedIn": true, "authMethod": "claude.ai", "apiProvider": "firstParty", "orgId": org, "subscriptionType": "max"}
	switch p.fail() {
	case "signed-out":
		st = map[string]any{"loggedIn": true, "authMethod": "api-key", "apiProvider": "firstParty"}
	case "not-eligible":
		st["subscriptionType"] = "free"
	}
	b, _ := json.Marshal(st)
	fmt.Fprintln(p.Stdout, string(b))
	return 0
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
	if p.fail() == "repo-mismatch" {
		// Unverified wording: a layout the bundle refuses (a submodule, say), on a remote the
		// cloud cannot clone.
		return p.errorf(1, "Error: Claude Code can't send this repository to the cloud: it is inside a submodule.")
	}
	if !strings.HasPrefix(repo, "github.com/") || p.Env("CCR_FORCE_BUNDLE") == "1" {
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
// checkout with the message an issue quotes. As the issues report, an inherited
// CLAUDE_CODE_CHILD_SESSION marker saves nothing and an ANTHROPIC_API_KEY fails it.
func claudeTeleport(p Proc, arg string) int {
	if arg == "" {
		return p.errorf(1, "Error: --teleport without a session opens a picker, which needs a terminal.")
	}
	if p.fail() == "signed-out" || p.Env("ANTHROPIC_API_KEY") != "" {
		// Documented: teleport with an API key fails so.
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
	if p.Env("CLAUDE_CODE_CHILD_SESSION") != "" {
		// Seen in anthropics/claude-code#93892: inside another session it saves nothing.
		fmt.Fprintln(p.Stdout, "Transcript saving is off — inherited CLAUDE_CODE_CHILD_SESSION marker")
		return 0
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

// Codex answers Codex's cloud commands as codex-cli prints them: codex cloud
// exec|list|status|diff|apply, codex apply and codex login status. The output shapes and
// wordings are read from openai/codex (codex-rs/cloud-tasks/src/lib.rs and cli.rs,
// cloud-tasks-client/src/api.rs and http.rs, cli/src/login.rs) on 2026-10-04; none was seen
// from a real task, so each is "from source", not verified. handled is false for any other
// call.
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
		return true, codexApply(p, p.Args[1:], false)
	case "login":
		if len(p.Args) == 2 && p.Args[1] == "status" {
			return true, codexLoginStatus(p)
		}
	}
	return false, 0
}

// codexLoginStatus answers `codex login status` on standard error, as login.rs does: a
// ChatGPT login, or (FAKE_CLOUD_FAIL=signed-out) an API key, shown masked; with
// FAKE_CODEX_LOGIN=none, not logged in.
func codexLoginStatus(p Proc) int {
	switch {
	case p.Env("FAKE_CODEX_LOGIN") == "none":
		return p.errorf(1, "Not logged in")
	case p.fail() == "signed-out":
		return p.errorf(0, "Logged in using an API key - sk-proj-***XXXX")
	}
	return p.errorf(0, "Logged in using ChatGPT")
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

// codexStatus is a task's status as `codex cloud list --json` spells it: the client's
// TaskStatus in kebab case (pending, ready, applied, error).
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

// codexEnvs are the environments the fake account has: FAKE_CODEX_ENVS, a comma-separated
// list of ids or id=label pairs ("" : any id is one, named after itself).
func codexEnvs(p Proc) map[string]string {
	v := p.Env("FAKE_CODEX_ENVS")
	if v == "" {
		return nil
	}
	out := map[string]string{}
	for _, e := range strings.Split(v, ",") {
		id, label, ok := strings.Cut(strings.TrimSpace(e), "=")
		if !ok {
			label = id
		}
		out[id] = label
	}
	return out
}

// codexEnv resolves --env as exec does: an id, else a label (case-insensitive).
func codexEnv(p Proc, want string) (id, label string, ok bool) {
	envs := codexEnvs(p)
	if p.fail() == "no-env" {
		return "", "", false
	}
	if envs == nil {
		return want, want, want != ""
	}
	if l, ok := envs[want]; ok {
		return want, l, true
	}
	for id, l := range envs {
		if strings.EqualFold(l, want) {
			return id, l, true
		}
	}
	return "", "", false
}

func codexCloud(p Proc, cmd string, args []string) int {
	if cmd == "--help" {
		// The commands as 0.153.2's help lists them (clap's layout).
		fmt.Fprint(p.Stdout, "[EXPERIMENTAL] Browse tasks from Codex Cloud and apply changes locally\n\nUsage: codex cloud [OPTIONS] [COMMAND]\n\nCommands:\n"+
			"  exec    Submit a new Codex Cloud task without launching the TUI\n  status  Show the status of a Codex Cloud task\n"+
			"  list    List Codex Cloud tasks\n  apply   Apply the diff for a Codex Cloud task locally\n  diff    Show the unified diff for a Codex Cloud task\n"+
			"  help    Print this message or the help of the given subcommand(s)\n")
		return 0
	}
	switch fail := p.fail(); fail {
	case "signed-out":
		// From source (init_backend): an API-key login is not a ChatGPT one either.
		return p.errorf(1, "Not signed in. Please run 'codex login' to sign in with ChatGPT, then re-run 'codex cloud'.")
	case "not-eligible":
		// Unverified: what a plan without cloud tasks gets (an HTTP error through the client).
		return p.errorf(1, "Error: http error: list_tasks failed: 403 Forbidden: Codex cloud is not available on your plan")
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
		attempts := 1
		if a := flags["--attempts"]; a != "" {
			n, err := strconv.Atoi(a)
			if err != nil || n < 1 || n > 4 {
				return p.errorf(2, "error: invalid value '%s' for '--attempts <ATTEMPTS>': attempts must be between 1 and 4", a)
			}
			attempts = n
		}
		envID, envLabel, ok := codexEnv(p, flags["--env"])
		if !ok {
			if p.fail() == "no-env" && p.Env("FAKE_CODEX_ENVS") == "none" {
				return p.errorf(1, "Error: no cloud environments are available for this workspace")
			}
			return p.errorf(1, "Error: environment '%s' not found; run `codex cloud` to list available environments", flags["--env"])
		}
		env := p.environ()
		repo, cloneURL, branch, _, err := repoOf(env, p.Dir)
		if err != nil {
			return p.errorf(1, "Error: %v", err)
		}
		if p.fail() == "repo-mismatch" || !strings.HasPrefix(repo, "github.com/") {
			// Unverified: the environment decides the repository; a branch it cannot find fails.
			return p.errorf(1, "Error: http error: create_task failed: 400 Bad Request: the environment's repository is not connected to %s", repo)
		}
		if b := flags["--branch"]; b != "" {
			branch = b
		}
		if !onRemote(env, p.Dir, branch) {
			return p.errorf(1, "Error: http error: create_task failed: 400 Bad Request: branch %s not found", branch) // unverified wording
		}
		base, err := git(env, p.Dir, "ls-remote", "origin", "refs/heads/"+branch)
		if err != nil {
			return p.errorf(1, "Error: %v", err)
		}
		prompt := strings.Join(pos, " ")
		if prompt == "" {
			return p.errorf(1, "Error: no query provided. Pass one as an argument or pipe it via stdin.")
		}
		s, err := st.Seed(Session{Cloud: CodexCloud, Title: clip(prompt, 60), Repo: repo, CloneURL: cloneURL, Branch: branch,
			Base: strings.Fields(base)[0], Code: "branch", Env: envID, EnvLabel: envLabel, Attempts: attempts, Diff: p.Env("CODEX_STARTING_DIFF"),
			Messages: []Message{{Role: "user", Text: prompt, Time: time.Now().UTC()}}})
		if err != nil {
			return p.errorf(1, "%v", err)
		}
		fmt.Fprintln(p.Stdout, s.URL()) // from source: util::task_url
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
		limit := 20
		if l := flags["--limit"]; l != "" {
			n, err := strconv.Atoi(l)
			if err != nil || n < 1 || n > 20 {
				return p.errorf(2, "error: invalid value '%s' for '--limit <N>': limit must be between 1 and 20", l)
			}
			limit = n
		}
		envID := ""
		if e := flags["--env"]; e != "" {
			id, _, ok := codexEnv(p, e)
			if !ok {
				return p.errorf(1, "Error: environment '%s' not found; run `codex cloud` to list available environments", e)
			}
			envID = id
		}
		var mine []Session
		for _, s := range all {
			if envID == "" || s.Env == envID {
				mine = append(mine, s)
			}
		}
		from := 0
		if c := flags["--cursor"]; c != "" {
			from, _ = strconv.Atoi(strings.TrimPrefix(c, "fake-cursor-"))
		}
		tasks := []any{}
		var cursor any
		for i := from; i < len(mine); i++ {
			if len(tasks) == limit {
				cursor = fmt.Sprintf("fake-cursor-%d", i)
				break
			}
			tasks = append(tasks, codexTask(mine[i]))
		}
		if p.fail() == "bad-record" {
			tasks = append(tasks, map[string]any{"id": 42, "status": []int{}})
		}
		if flags["--json"] == "" {
			if len(tasks) == 0 {
				fmt.Fprintln(p.Stdout, "No tasks found.")
			}
			for _, s := range mine[from:min(from+limit, len(mine))] {
				fmt.Fprintln(p.Stdout, s.URL())
				for _, l := range codexStatusLines(s) {
					fmt.Fprintln(p.Stdout, "  "+l)
				}
			}
			return 0
		}
		// From source: pretty-printed {"tasks": [...], "cursor": ...}.
		b, _ := json.MarshalIndent(map[string]any{"tasks": tasks, "cursor": cursor}, "", "  ")
		fmt.Fprintln(p.Stdout, string(b))
		return 0
	case "status":
		_, pos := codexFlags(args)
		if len(pos) == 0 {
			return p.errorf(2, "error: the following required arguments were not provided:\n  <TASK_ID>")
		}
		s, err := st.Get(codexTaskID(pos[0]))
		if err != nil || s.Cloud != CodexCloud {
			return p.errorf(1, "Error: http error: get_task_details failed: 404 Not Found")
		}
		for _, l := range codexStatusLines(s) {
			fmt.Fprintln(p.Stdout, l)
		}
		if codexStatus(s) != "ready" {
			return 1 // from source: status exits 1 unless the task is READY
		}
		return 0
	case "diff":
		flags, pos := codexFlags(args, "--attempt")
		if len(pos) == 0 {
			return p.errorf(2, "error: the following required arguments were not provided:\n  <TASK_ID>")
		}
		s, err := st.Get(codexTaskID(pos[0]))
		if err != nil || s.Cloud != CodexCloud {
			return p.errorf(1, "Error: http error: get_task_details failed: 404 Not Found")
		}
		if s.State == StateRunning || s.Diff == "" {
			return p.errorf(1, "Error: No diff available for task %s; it may still be running.", s.ID)
		}
		if a := flags["--attempt"]; a != "" && a != "1" {
			return p.errorf(1, "Error: Attempt %s not available; only 1 attempt(s) found", a)
		}
		fmt.Fprint(p.Stdout, s.Diff)
		return 0
	case "apply":
		return codexApply(p, args, true)
	}
	return p.errorf(2, "error: unrecognized subcommand '%s'", cmd)
}

// codexTaskID is a task id, or the id at the end of its link (parse_task_id).
func codexTaskID(raw string) string {
	raw, _, _ = strings.Cut(strings.TrimSpace(raw), "#")
	raw, _, _ = strings.Cut(raw, "?")
	return raw[strings.LastIndex(raw, "/")+1:]
}

// codexDiffSummary counts a unified diff's files and lines, as the backend's summary does.
func codexDiffSummary(diff string) (files, added, removed int) {
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			files++
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		case strings.HasPrefix(l, "+"):
			added++
		case strings.HasPrefix(l, "-"):
			removed++
		}
	}
	return files, added, removed
}

// codexStatusLines are a task's status lines as format_task_status_lines prints them
// without colour: "[READY] title", "label  •  3m ago", "+3/-1 • 2 files" (or "no diff").
func codexStatusLines(s Session) []string {
	label := s.EnvLabel
	if label == "" {
		label = s.Env
	}
	ago := time.Since(s.Updated)
	when := fmt.Sprintf("%ds ago", int(ago.Seconds()))
	if ago >= time.Minute {
		when = fmt.Sprintf("%dm ago", int(ago.Minutes()))
	}
	meta := when
	if label != "" {
		meta = label + "  •  " + when
	}
	stat := "no diff"
	if s.State != StateRunning {
		if f, a, r := codexDiffSummary(s.Diff); f+a+r > 0 {
			stat = fmt.Sprintf("+%d/-%d • %d file%s", a, r, f, map[bool]string{true: "", false: "s"}[f == 1])
		}
	}
	return []string{"[" + strings.ToUpper(codexStatus(s)) + "] " + s.Title, meta, stat}
}

// codexTask is a task in `codex cloud list --json`'s fields (from source: run_list_command).
func codexTask(s Session) map[string]any {
	files, added, removed := 0, 0, 0
	if s.State != StateRunning {
		files, added, removed = codexDiffSummary(s.Diff)
	}
	var envID, envLabel any
	if s.Env != "" {
		envID, envLabel = s.Env, nonEmpty(s.EnvLabel, s.Env)
	}
	return map[string]any{"id": s.ID, "url": s.URL(), "title": s.Title, "status": codexStatus(s),
		"updated_at": s.Updated.UTC().Format(time.RFC3339Nano), "environment_id": envID, "environment_label": envLabel,
		"summary":   map[string]any{"files_changed": files, "lines_added": added, "lines_removed": removed},
		"is_review": false, "attempt_total": s.Attempts}
}

// codexApply applies a task's diff to the folder with git apply, as `codex cloud apply` and
// `codex apply` do (both exit non-zero when git apply fails); cloud: the cloud command's
// wording.
func codexApply(p Proc, args []string, cloud bool) int {
	_, pos := codexFlags(args, "--attempt")
	if len(pos) == 0 {
		return p.errorf(2, "error: the following required arguments were not provided:\n  <TASK_ID>")
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	id := codexTaskID(pos[0])
	s, err := st.Get(id)
	if err != nil || s.Diff == "" || s.State == StateRunning {
		return p.errorf(1, "Error: No diff available for task %s; it may still be running.", id)
	}
	files, _, _ := codexDiffSummary(s.Diff)
	if err := applyDiff(p.environ(), p.Dir, s.Diff); err != nil {
		if cloud {
			fmt.Fprintf(p.Stdout, "Apply failed for task %s (applied=0, skipped=0, conflicts=%d)\n", id, files)
			return 1
		}
		return p.errorf(1, "Error: Git apply failed (applied=0, skipped=0, conflicts=%d)\nstderr:\n%v", files, err)
	}
	s.Applied = true
	_ = st.Put(s)
	if cloud {
		fmt.Fprintf(p.Stdout, "Applied task %s locally (%d files)\n", id, files)
	} else {
		fmt.Fprintln(p.Stdout, "Successfully applied diff")
	}
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
