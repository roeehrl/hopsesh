package fakecloud

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The second-wave vendors' CLIs, as far as hopsesh asks them: GitHub's gh (agent-task,
// pr, auth status), Jules Tools (jules remote), the Devin CLI (devin list, auth status)
// and Amp's CLI (amp threads). Their sessions are the store's, by cloud. What the vendors
// document is followed; the rest (most output layouts) is marked unverified where it is
// printed, and the modules parse it defensively.

// Gh answers gh --version, auth status --json hosts, agent-task list|view [--log] and the
// help hopsesh reads, and pr list|view for the branches of the agents' pull requests.
func Gh(p Proc) int {
	args := p.Args
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(p.Stdout, "gh version 2.97.0 (2026-07-31)")
		fmt.Fprintln(p.Stdout, "https://github.com/cli/cli/releases/tag/v2.97.0")
		return 0
	}
	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		return ghAuth(p)
	}
	if len(args) >= 1 && (args[0] == "agent-task" || args[0] == "agent-tasks") {
		return ghAgentTask(p, args[1:])
	}
	if len(args) >= 2 && args[0] == "pr" {
		return ghPR(p, args[1], args[2:])
	}
	return p.errorf(1, "unknown command %q for \"gh\"", strings.Join(args, " "))
}

// ghAuth answers `auth status --json hosts` in the shape of gh's source (hosts → host →
// accounts with state, active, login, tokenSource; the token only with --show-token,
// which hopsesh never passes). Signed out: no host at all.
func ghAuth(p Proc) int {
	if !contains(p.Args, "--json") {
		return p.errorf(1, "the fake answers only auth status --json hosts")
	}
	hosts := map[string]any{}
	if p.fail() != "signed-out" {
		hosts["github.com"] = []map[string]any{{"state": "success", "active": true, "host": "github.com", "login": "example-user",
			"tokenSource": "keyring", "scopes": "gist, read:org, repo, workflow", "gitProtocol": "https"}}
	}
	b, _ := json.Marshal(map[string]any{"hosts": hosts})
	fmt.Fprintln(p.Stdout, string(b))
	return 0
}

// ghRefused is how gh fails before an agent-task call: exit 4 without a login (gh's
// documented exit code for "requires authentication"), and a 403 when Copilot's cloud
// agent is not on the account's plan (unverified wording).
func ghRefused(p Proc) (int, bool) {
	switch p.fail() {
	case "signed-out":
		return p.errorf(4, "To get started with GitHub CLI, please run:  gh auth login\nAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token."), true
	case "not-eligible":
		return p.errorf(1, "failed to list agent tasks: HTTP 403: Copilot coding agent is not enabled for this account"), true
	}
	return 0, false
}

func ghAgentTask(p Proc, args []string) int {
	if len(args) == 0 || args[0] == "--help" {
		// The commands hopsesh relies on, as gh 2.97.0's help names them.
		fmt.Fprintln(p.Stdout, "Working with agent tasks in the GitHub CLI is in preview and\nsubject to change without notice.")
		fmt.Fprintln(p.Stdout, "\nAVAILABLE COMMANDS\n  create:        Create an agent task (preview)\n  list:          List agent tasks (preview)\n  view:          View an agent task session (preview)")
		return 0
	}
	if len(args) > 1 && args[1] == "--help" && args[0] == "create" {
		// gh 2.97.0's help for create, the flags hopsesh uses.
		fmt.Fprintln(p.Stdout, "FLAGS\n  -b, --base string              Base branch for the pull request (use default branch if not provided)\n      --follow                   Follow agent session logs")
		fmt.Fprintln(p.Stdout, "  -F, --from-file file           Read task description from file (use \"-\" to read from standard input)\n  -R, --repo [HOST/]OWNER/REPO   Select another repository using the [HOST/]OWNER/REPO format")
		return 0
	}
	if len(args) > 1 && args[1] == "--help" {
		fmt.Fprintln(p.Stdout, "FLAGS\n      --json fields       Output JSON with the specified fields\n      --log               Show agent session logs\n  -L, --limit int         Maximum number of agent tasks to fetch (default 30)")
		fmt.Fprintln(p.Stdout, "\nJSON FIELDS\n  completedAt, createdAt, id, name, pullRequestNumber, pullRequestState,\n  pullRequestTitle, pullRequestUrl, repository, state, updatedAt, user")
		return 0
	}
	if code, refused := ghRefused(p); refused {
		return code
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	flags, pos := codexFlags(args[1:], "--json", "--limit", "-L", "--repo", "-R", "--jq", "-q")
	fields := strings.Split(flags["--json"], ",")
	switch args[0] {
	case "create":
		return ghCreate(p, st, args[1:])
	case "list":
		if p.fail() == "slow" {
			time.Sleep(5 * time.Second)
		}
		all, err := st.List(CopilotCloud)
		if err != nil {
			return p.errorf(1, "%v", err)
		}
		limit, _ := strconv.Atoi(nonEmpty(flags["--limit"], flags["-L"]))
		if limit <= 0 {
			limit = 30
		}
		list := []any{}
		for _, s := range all {
			if len(list) == limit {
				break
			}
			list = append(list, pick(ghTask(s), fields))
		}
		if p.fail() == "bad-record" {
			list = append(list, map[string]any{"id": 42, "state": []int{}})
		}
		if flags["--json"] == "" {
			for _, t := range list {
				m, _ := t.(map[string]any)
				fmt.Fprintf(p.Stdout, "%v\t%v\t%v\n", m["name"], m["repository"], m["state"]) // unverified layout
			}
			return 0
		}
		b, _ := json.Marshal(list)
		fmt.Fprintln(p.Stdout, string(b))
		return 0
	case "view":
		if len(pos) == 0 {
			return p.errorf(1, "a session ID, pull request number, URL or branch is required when not running interactively")
		}
		s, err := st.Get(pos[0])
		if err != nil || s.Cloud != CopilotCloud {
			return p.errorf(1, "no agent task found for %s", pos[0]) // unverified wording
		}
		if flags["--log"] != "" {
			// Unverified: gh renders the session's log for a terminal; the fake prints the
			// turns as plain paragraphs, the way the module reads any text it gets.
			for _, m := range s.Messages {
				if m.Role == "user" {
					fmt.Fprintf(p.Stdout, "> %s\n\n", m.Text)
				} else {
					fmt.Fprintf(p.Stdout, "%s\n\n", m.Text)
				}
			}
			return 0
		}
		if flags["--json"] == "" {
			fmt.Fprintf(p.Stdout, "%s\n%s\n%s\n", s.Title, ghState(s), s.URL()) // unverified layout
			return 0
		}
		b, _ := json.Marshal(pick(ghTask(s), fields))
		fmt.Fprintln(p.Stdout, string(b))
		return 0
	}
	return p.errorf(1, "unknown command %q for \"gh agent-task\"", args[0])
}

// ghState is a task's state in the REST API's words (queued, in_progress, completed,
// failed, idle, waiting_for_user, timed_out, cancelled); gh's JSON is assumed to pass them
// through (unverified).
func ghState(s Session) string {
	switch s.State {
	case StateRunning:
		return "in_progress"
	case StateFailed:
		return "failed"
	case StateIdle:
		return "waiting_for_user"
	case StateArchived:
		return "cancelled"
	}
	return "completed"
}

// ghTask is a task in gh agent-task's JSON fields (the names from its help; their value
// types are unverified: repository as "owner/repo", user as {login}).
func ghTask(s Session) map[string]any {
	t := map[string]any{"id": s.ID, "name": s.Title, "state": ghState(s), "repository": strings.TrimPrefix(s.Repo, "github.com/"),
		"createdAt": s.Created.UTC().Format(time.RFC3339), "updatedAt": s.Updated.UTC().Format(time.RFC3339),
		"user": map[string]any{"login": "example-user"}, "pullRequestNumber": nil, "pullRequestUrl": nil, "pullRequestState": nil, "pullRequestTitle": nil}
	if s.State == StateDone {
		t["completedAt"] = s.Updated.UTC().Format(time.RFC3339)
	}
	if s.PR != 0 {
		t["pullRequestNumber"], t["pullRequestState"], t["pullRequestTitle"] = s.PR, "OPEN", s.Title
		t["pullRequestUrl"] = fmt.Sprintf("https://%s/pull/%d", s.Repo, s.PR)
	}
	return t
}

// pick keeps the fields asked for, as gh does with --json.
func pick(m map[string]any, fields []string) map[string]any {
	if len(fields) == 0 || fields[0] == "" {
		return m
	}
	out := map[string]any{}
	for _, f := range fields {
		if v, ok := m[strings.TrimSpace(f)]; ok {
			out[strings.TrimSpace(f)] = v
		}
	}
	return out
}

// ghPR answers pr list and pr view for the pull requests the agents opened (the fake's
// copilot and devin sessions), in gh's documented JSON fields.
func ghPR(p Proc, cmd string, args []string) int {
	if p.fail() == "signed-out" {
		code, _ := ghRefused(p)
		return code
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	flags, pos := codexFlags(args, "--json", "--limit", "-L", "--repo", "-R", "--state", "-s", "--app", "--search", "-S")
	repo := "github.com/" + strings.TrimPrefix(nonEmpty(flags["--repo"], flags["-R"]), "github.com/")
	fields := strings.Split(flags["--json"], ",")
	var prs []map[string]any
	for _, c := range []string{CopilotCloud, DevinCloud} {
		all, _ := st.List(c)
		for _, s := range all {
			if s.PR == 0 || s.Repo != repo {
				continue
			}
			prs = append(prs, map[string]any{"number": s.PR, "headRefName": s.Result, "baseRefName": s.Branch, "baseRefOid": s.Base,
				"title": s.Title, "state": "OPEN", "url": fmt.Sprintf("https://%s/pull/%d", s.Repo, s.PR)})
		}
	}
	sort.Slice(prs, func(i, j int) bool { return prs[i]["number"].(int) > prs[j]["number"].(int) })
	switch cmd {
	case "list":
		out := []any{}
		for _, pr := range prs {
			out = append(out, pick(pr, fields))
		}
		b, _ := json.Marshal(out)
		fmt.Fprintln(p.Stdout, string(b))
		return 0
	case "view":
		if len(pos) == 0 {
			return p.errorf(1, "argument required when not running interactively")
		}
		for _, pr := range prs {
			if fmt.Sprint(pr["number"]) == pos[0] {
				b, _ := json.Marshal(pick(pr, fields))
				fmt.Fprintln(p.Stdout, string(b))
				return 0
			}
		}
		return p.errorf(1, "GraphQL: Could not resolve to a PullRequest with the number of %s. (repository.pullRequest)", pos[0])
	}
	return p.errorf(1, "unknown command %q for \"gh pr\"", cmd)
}

// Jules answers Jules Tools: version, remote list --session|--repo (a table, columns split
// by two or more spaces, as scripts that read it describe; unverified), remote pull
// --session ID [--apply] (the session's patch; --apply applies it in the folder) and the
// help hopsesh reads.
func Jules(p Proc) int {
	args := p.Args
	switch strings.Join(args, " ") {
	case "version", "--version":
		fmt.Fprintln(p.Stdout, "0.1.42") // unverified layout
		return 0
	case "--help", "remote --help", "remote pull --help", "remote list --help", "remote new --help":
		fmt.Fprintln(p.Stdout, "Usage:\n  jules remote [command]\n\nAvailable Commands:\n  list        List remote repos or sessions\n  new         Create a remote session\n  pull        Pull the result of a remote session")
		fmt.Fprintln(p.Stdout, "\nFlags:\n      --apply            Apply the patch to the local repository\n      --parallel int     Number of parallel sessions\n      --repo             The repository (owner/repo, or . for this folder's)\n      --session string   The session ID, or the new session's prompt")
		return 0
	}
	if len(args) < 2 || args[0] != "remote" {
		return p.errorf(1, "Error: unknown command %q for \"jules\"", strings.Join(args, " "))
	}
	switch p.fail() {
	case "signed-out":
		return p.errorf(1, "Error: you are not logged in. Run `jules login` to log in.") // unverified wording
	case "not-eligible":
		return p.errorf(1, "Error: Jules is not available for this account.") // unverified wording
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	flags, _ := codexFlags(args[2:], "--session", "--repo", "--parallel")
	switch args[1] {
	case "new":
		return julesNew(p, st, flags)
	case "list":
		all, err := st.List(JulesCloud)
		if err != nil {
			return p.errorf(1, "%v", err)
		}
		if flags["--repo"] != "" && flags["--session"] == "" {
			fmt.Fprintln(p.Stdout, "Repo")
			seen := map[string]bool{}
			for _, s := range all {
				if r := strings.TrimPrefix(s.Repo, "github.com/"); !seen[r] {
					seen[r] = true
					fmt.Fprintln(p.Stdout, r)
				}
			}
			return 0
		}
		if p.fail() == "slow" {
			time.Sleep(5 * time.Second)
		}
		rows := [][]string{{"ID", "Description", "Repo", "Last active", "Status"}}
		for _, s := range all {
			rows = append(rows, []string{s.ID, clip(s.Title, 40), strings.TrimPrefix(s.Repo, "github.com/"), ago(s.Updated), julesStatus(s)})
		}
		if p.fail() == "bad-record" {
			rows = append(rows, []string{"…", "(could not load)"})
		}
		table(p, rows)
		return 0
	case "pull":
		id := flags["--session"]
		s, err := st.Get(id)
		if id == "" || err != nil || s.Cloud != JulesCloud {
			return p.errorf(1, "Error: session %s not found", id) // unverified wording
		}
		if s.Diff == "" {
			return p.errorf(1, "Error: session %s has no changes to pull yet", id) // unverified wording
		}
		if flags["--apply"] != "" {
			if err := applyDiff(p.environ(), p.Dir, s.Diff); err != nil {
				return p.errorf(1, "Error: %v", err)
			}
			fmt.Fprintln(p.Stdout, "Applied the patch.") // unverified wording
			return 0
		}
		fmt.Fprint(p.Stdout, s.Diff)
		return 0
	}
	return p.errorf(1, "Error: unknown command %q for \"jules remote\"", args[1])
}

// julesStatus is a session's state as the list's last column words it (Completed,
// In Progress, Planning, Awaiting User Feedback, Failed; unverified beyond the scripts'
// examples).
func julesStatus(s Session) string {
	switch s.State {
	case StateRunning:
		return "In Progress"
	case StateIdle:
		return "Awaiting User Feedback"
	case StateFailed:
		return "Failed"
	}
	return "Completed"
}

// Devin answers the Devin CLI: version, auth status, list --format json (documented; that
// it holds cloud sessions, and its fields, are unverified) and --help.
func Devin(p Proc) int {
	switch strings.Join(p.Args, " ") {
	case "version", "--version":
		fmt.Fprintln(p.Stdout, "devin 2026.9.24") // unverified layout
		return 0
	case "--help":
		fmt.Fprintln(p.Stdout, "Usage: devin [OPTIONS] [PROMPT] [COMMAND]\n\nCommands:\n  auth  Manage authentication\n  list  List sessions (interactive picker by default)\n\nOptions:\n  --cloud   Drive Devin Cloud sessions instead of the local agent\n  -p, --print [PROMPT]  Print response and exit (non-interactive mode)\n  --respect-workspace-trust [true|false]  Whether to respect workspace trust settings\n  --format <FORMAT>  Output format for list: json or csv")
		return 0
	case "auth status":
		if p.fail() == "signed-out" {
			return p.errorf(1, "Not logged in. Run `devin auth login` to log in.") // unverified wording
		}
		fmt.Fprintln(p.Stdout, "Logged in as example-user (example-org)") // unverified wording
		return 0
	}
	if contains(p.Args, "--cloud") {
		return devinCloud(p)
	}
	if len(p.Args) == 0 || p.Args[0] != "list" && p.Args[0] != "ls" {
		return p.errorf(2, "error: unrecognized subcommand '%s'", strings.Join(p.Args, " "))
	}
	switch p.fail() {
	case "signed-out":
		return p.errorf(1, "Error: not logged in. Run `devin auth login`.") // unverified wording
	case "not-eligible":
		return p.errorf(1, "Error: Devin Cloud is not enabled for your organization.") // unverified wording
	}
	flags, _ := codexFlags(p.Args[1:], "--format")
	if flags["--format"] != "json" {
		return p.errorf(1, "Error: the session picker needs a terminal")
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	if p.fail() == "slow" {
		time.Sleep(5 * time.Second)
	}
	all, err := st.List(DevinCloud)
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	// A local session first: the list mixes them in (unverified), and the module keeps the
	// cloud ones.
	list := []any{map[string]any{"id": "0b6f8f1e-2c55-4a0e-9a59-3e4f1d2c7a10", "title": "a local session", "location": "local",
		"updated_at": time.Now().UTC().Format(time.RFC3339)}}
	for _, s := range all {
		status := map[string]string{StateRunning: "running", StateIdle: "blocked", StateFailed: "error", StateArchived: "archived"}[s.State]
		d := map[string]any{"id": s.ID, "title": s.Title, "status": nonEmpty(status, "finished"), "location": "cloud", "url": s.URL(),
			"created_at": s.Created.UTC().Format(time.RFC3339), "updated_at": s.Updated.UTC().Format(time.RFC3339)}
		if s.Repo != "" {
			d["repos"] = []string{strings.TrimPrefix(s.Repo, "github.com/")}
		}
		if s.PR != 0 {
			d["pull_request"] = map[string]any{"url": fmt.Sprintf("https://%s/pull/%d", s.Repo, s.PR), "branch": s.Result}
		}
		list = append(list, d)
	}
	if p.fail() == "bad-record" {
		list = append(list, map[string]any{"id": []int{1}, "location": "cloud"})
	}
	b, _ := json.Marshal(list)
	fmt.Fprintln(p.Stdout, string(b))
	return 0
}

// Amp answers Amp's CLI: --version, threads list (a table: title, last updated,
// visibility, messages, thread id, as a community SDK parses it; unverified), threads
// markdown T-… (the thread as Markdown; its layout is unverified) and the help hopsesh
// reads.
func Amp(p Proc) int {
	switch strings.Join(p.Args, " ") {
	case "--version", "version":
		fmt.Fprintln(p.Stdout, "0.0.1791107882-gfe04cc")
		return 0
	case "--help", "threads --help":
		fmt.Fprintln(p.Stdout, "Usage: amp [options] [command]\n\nOptions:\n  -x, --execute [message]   Execute mode: run the prompt and exit\n  --orb-execute             With -x: run the thread in an orb (-ox)\n  --project <project>       The orb's project (GitHub owner/repo)\n  --title <title>           The thread's title\n\nCommands:\n  threads list              List your threads\n  threads markdown <id>     Print a thread as Markdown\n  sync <thread>             Mirror an orb thread's changes to this checkout")
		return 0
	}
	if contains(p.Args, "-ox") || contains(p.Args, "--orb-execute") {
		return ampOrb(p)
	}
	if len(p.Args) < 2 || p.Args[0] != "threads" && p.Args[0] != "t" {
		return p.errorf(1, "error: unknown command '%s'", strings.Join(p.Args, " "))
	}
	switch p.fail() {
	case "signed-out":
		return p.errorf(1, "Error: You are not logged in. Run `amp login` first.") // unverified wording
	case "not-eligible":
		return p.errorf(1, "Error: Your workspace has no credits left; threads are paused. (402 Payment Required)") // unverified wording
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	switch p.Args[1] {
	case "list", "ls":
		if p.fail() == "slow" {
			time.Sleep(5 * time.Second)
		}
		all, err := st.List(AmpCloud)
		if err != nil {
			return p.errorf(1, "%v", err)
		}
		rows := [][]string{{"Title", "Last Updated", "Visibility", "Messages", "Thread ID"}, {"─────", "────────────", "──────────", "────────", "─────────"}}
		for _, s := range all {
			rows = append(rows, []string{clip(s.Title, 40), ago(s.Updated), "Private", strconv.Itoa(len(s.Messages)), s.ID})
		}
		if p.fail() == "bad-record" {
			rows = append(rows, []string{"(broken)", "?", "?", "?", "not-a-thread"})
		}
		if len(all) == 0 {
			fmt.Fprintln(p.Stdout, "No records found.")
			return 0
		}
		table(p, rows)
		return 0
	case "markdown", "export":
		if len(p.Args) < 3 {
			return p.errorf(1, "error: missing required argument 'threadId'")
		}
		s, err := st.Get(p.Args[2])
		if err != nil || s.Cloud != AmpCloud {
			return p.errorf(1, "Error: thread %s not found", p.Args[2]) // unverified wording
		}
		fmt.Fprintf(p.Stdout, "# %s\n\n", s.Title)
		for _, m := range s.Messages {
			who := "Assistant"
			if m.Role == "user" {
				who = "User"
			}
			fmt.Fprintf(p.Stdout, "## %s\n\n%s\n\n", who, m.Text)
		}
		return 0
	}
	return p.errorf(1, "error: unknown command 'threads %s'", p.Args[1])
}

// table prints rows with columns padded and separated by at least two spaces.
func table(p Proc, rows [][]string) {
	w := map[int]int{}
	for _, r := range rows {
		for i, c := range r {
			w[i] = max(w[i], len([]rune(c)))
		}
	}
	for _, r := range rows {
		var b strings.Builder
		for i, c := range r {
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", w[i]-len([]rune(c))+2))
			}
		}
		fmt.Fprintln(p.Stdout, strings.TrimRight(b.String(), " "))
	}
}

// ago is a time as a list's "Last active" column words it ("5m ago"; unverified).
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// The create verbs: a hand-off's start in each vendor. Each new session records where it
// starts: the repository's clone URL and the branch and commit it starts from, read from
// the folder the CLI runs in (a worktree on the handoff branch) when that is a checkout.

// briefedBranch is the branch a briefing asks the agent to check out first ("git fetch
// origin B && git checkout B"), which the fake agent does, as a real one following the
// briefing would; "" when it asks for none.
func briefedBranch(prompt string) string {
	if mm := checkoutAsk.FindStringSubmatch(prompt); mm != nil {
		return mm[1]
	}
	return ""
}

var checkoutAsk = regexp.MustCompile("git checkout ([^`\\s]+)`")

// startFrom fills a new session's clone URL and starting commit from the folder the CLI
// runs in, when it is a checkout of repo whose origin has branch; ok is false when the folder
// is that checkout but the branch is not on origin.
func startFrom(p Proc, s *Session, branch string) bool {
	env := p.environ()
	identity, cloneURL, _, _, err := repoOf(env, p.Dir)
	if err != nil || p.Dir == "" || identity != s.Repo {
		return true // not a checkout here (the conformance kit's made-up folder)
	}
	s.CloneURL, s.Branch = cloneURL, branch
	out, err := git(env, p.Dir, "ls-remote", "origin", "refs/heads/"+branch)
	if err != nil || out == "" {
		return false
	}
	s.Base = strings.Fields(out)[0]
	return true
}

// title is a task's title from its prompt: the first line, without hopsesh's prefix.
func title(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	return clip(strings.TrimPrefix(line, "[hopsesh] "), 60)
}

// ghCreate answers gh agent-task create [-F file|-] [--base B] [-R OWNER/REPO]
// [<description>]. Copilot opens a draft pull request when it starts, so the fake names its
// copilot/… branch and pull request at once, and gh prints the agent session's link (its
// source's agentSessionWebURL); with FAKE_CLOUD_FAIL=queued, the job has no pull request
// yet and gh prints "job <id> queued. View progress: …" instead.
func ghCreate(p Proc, st Store, args []string) int {
	flags, pos := codexFlags(args, "-F", "--from-file", "--base", "-b", "--repo", "-R", "--custom-agent", "-a")
	prompt := strings.Join(pos, " ")
	switch f := nonEmpty(flags["-F"], flags["--from-file"]); f {
	case "":
	case "-":
		prompt = p.input()
	default:
		b, err := os.ReadFile(f)
		if err != nil {
			return p.errorf(1, "could not read task description file: %v", err)
		}
		prompt = string(b)
	}
	if strings.TrimSpace(prompt) == "" {
		return p.errorf(1, "a task description or -F is required when running non-interactively")
	}
	repo := strings.TrimPrefix(nonEmpty(flags["--repo"], flags["-R"]), "github.com/")
	if repo == "" {
		if id, _, _, _, err := repoOf(p.environ(), p.Dir); err == nil {
			repo = strings.TrimPrefix(id, "github.com/")
		}
	}
	if repo == "" {
		return p.errorf(1, "a repository is required; re-run in a repository or supply one with --repo owner/name")
	}
	if p.fail() == "repo-mismatch" {
		return p.errorf(1, "failed to create agent task: HTTP 404: Could not resolve to a Repository with the name '%s'.", repo) // unverified wording
	}
	base := nonEmpty(nonEmpty(flags["--base"], flags["-b"]), "main")
	s := Session{Cloud: CopilotCloud, Title: title(prompt), Repo: "github.com/" + repo, Branch: base, Code: "branch",
		Messages: []Message{{Role: "user", Text: prompt, Time: time.Now().UTC()}}}
	if !startFrom(p, &s, base) {
		return p.errorf(1, "failed to create agent task: HTTP 422: base branch %s not found", base) // unverified wording
	}
	id := newID(CopilotCloud)
	s.ID = id
	if p.fail() != "queued" {
		s.PR, s.Result = st.nextPR(), "copilot/"+slug(s.Title, "work")
	}
	s, err := st.Seed(s)
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	if p.fail() == "queued" {
		fmt.Fprintf(p.Stdout, "job %s queued. View progress: https://github.com/copilot/agents\n", newUUID())
		return 0
	}
	fmt.Fprintln(p.Stdout, s.URL())
	return 0
}

// julesNew answers jules remote new --repo OWNER/REPO --session PROMPT [--parallel N]. It
// takes no branch: the session starts on the repository's default branch, or (the fake's
// agent following the briefing) the one the briefing asks for. Unverified: what it prints;
// the fake prints a line with the id and the session's link.
func julesNew(p Proc, st Store, flags map[string]string) int {
	prompt, repo := flags["--session"], strings.TrimPrefix(flags["--repo"], "github.com/")
	if repo == "." || repo == "" {
		if id, _, _, _, err := repoOf(p.environ(), p.Dir); err == nil {
			repo = strings.TrimPrefix(id, "github.com/")
		}
	}
	switch {
	case strings.TrimSpace(prompt) == "":
		return p.errorf(1, "Error: a prompt is required (--session)") // unverified wording
	case repo == "":
		return p.errorf(1, "Error: no repository given (--repo)") // unverified wording
	case p.fail() == "repo-mismatch":
		return p.errorf(1, "Error: repository %s is not connected to Jules. Install the Jules GitHub app on it first.", repo) // unverified wording
	}
	branch := nonEmpty(briefedBranch(prompt), "main")
	s := Session{Cloud: JulesCloud, Title: title(prompt), Repo: "github.com/" + repo, Branch: branch, Code: "branch",
		Messages: []Message{{Role: "user", Text: prompt, Time: time.Now().UTC()}}}
	if !startFrom(p, &s, branch) {
		return p.errorf(1, "Error: branch %s not found in %s", branch, repo) // unverified wording
	}
	s, err := st.Seed(s)
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	fmt.Fprintf(p.Stdout, "Created session %s for %s.\n%s\n", s.ID, repo, s.URL()) // unverified layout
	return 0
}

// devinCloud answers devin --cloud [--respect-workspace-trust false] -p [--] PROMPT: it
// starts a cloud session on the folder's repository and branch (as /handoff carries them;
// unverified for -p), or the branch the briefing asks for, and prints Devin's first reply,
// with no session id in it (unverified: the docs say only that it prints the response).
// Without -p it refuses: the interactive session needs a terminal.
func devinCloud(p Proc) int {
	print := false
	var pos []string
	for i := 0; i < len(p.Args); i++ {
		switch a := p.Args[i]; a {
		case "--cloud", "--":
		case "-p", "--print":
			print = true
		case "--respect-workspace-trust", "--model", "--permission-mode", "--prompt-file", "-r", "--resume":
			i++
		default:
			pos = append(pos, a)
		}
	}
	if !print {
		return p.errorf(1, "Error: the interactive session needs a terminal")
	}
	switch p.fail() {
	case "signed-out":
		return p.errorf(1, "Error: not logged in. Run `devin auth login`.") // unverified wording
	case "not-eligible":
		return p.errorf(1, "Error: Devin Cloud is not enabled for your organization.") // unverified wording
	case "repo-mismatch":
		return p.errorf(1, "Error: repository example/demo is not connected to Devin.") // unverified wording
	}
	prompt := strings.Join(pos, " ")
	if strings.TrimSpace(prompt) == "" {
		return p.errorf(1, "Error: -p needs a prompt")
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	s := Session{Cloud: DevinCloud, Title: title(prompt), Code: "branch", Messages: []Message{{Role: "user", Text: prompt, Time: time.Now().UTC()}}}
	if id, _, branch, _, err := repoOf(p.environ(), p.Dir); err == nil {
		s.Repo = id
		if !startFrom(p, &s, nonEmpty(briefedBranch(prompt), branch)) {
			return p.errorf(1, "Error: branch not found on origin")
		}
	}
	reply := "I'll start by checking out the branch and reading the code."
	s.Messages = append(s.Messages, Message{Role: "assistant", Text: reply, Time: time.Now().UTC()})
	if _, err := st.Seed(s); err != nil {
		return p.errorf(1, "%v", err)
	}
	fmt.Fprintln(p.Stdout, reply)
	return 0
}

// ampOrb answers amp -ox PROMPT [--project OWNER/REPO] [--title T] [--orb-size S]: a new
// thread in an orb on the project, printing its link (documented) and returning at once.
func ampOrb(p Proc) int {
	flags, pos := codexFlags(p.Args, "--project", "--title", "--orb-size", "--mode", "-x", "--execute")
	prompt := nonEmpty(flags["-x"], flags["--execute"])
	for _, a := range pos {
		if a != "-ox" && a != "-o" {
			prompt = strings.TrimSpace(prompt + " " + a)
		}
	}
	switch p.fail() {
	case "signed-out":
		return p.errorf(1, "Error: You are not logged in. Run `amp login` first.") // unverified wording
	case "not-eligible":
		return p.errorf(1, "Error: Your workspace has no credits left; threads are paused. (402 Payment Required)") // unverified wording
	case "repo-mismatch":
		return p.errorf(1, "Error: no Amp project matches %s; create one with project: create", flags["--project"]) // unverified wording
	}
	if prompt == "" {
		return p.errorf(1, "error: -x needs a message")
	}
	st, err := p.store()
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	s := Session{Cloud: AmpCloud, Title: nonEmpty(flags["--title"], title(prompt)), Code: "branch",
		Messages: []Message{{Role: "user", Text: prompt, Time: time.Now().UTC()}}}
	if r := flags["--project"]; r != "" {
		s.Repo = "github.com/" + strings.TrimPrefix(r, "github.com/")
		startFrom(p, &s, nonEmpty(briefedBranch(prompt), "main"))
	}
	s, err = st.Seed(s)
	if err != nil {
		return p.errorf(1, "%v", err)
	}
	fmt.Fprintln(p.Stdout, s.URL())
	return 0
}
