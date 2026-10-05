package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Codex cloud tasks through the codex binary: `codex cloud list --json` lists them, `codex
// cloud exec` starts one from a briefing on a pushed branch (with CODEX_STARTING_DIFF for a
// small diff on a branch already pushed), `codex cloud status` and `codex cloud diff` bring
// one back as its title, its state and its diff (the core commits the diff on a branch of
// its own and writes the title and summary as a local thread). `codex login status` says
// which login codex uses. hopsesh never reads auth.json and never calls the ChatGPT
// backend: codex does, as the user.
//
// The CLI's verbs and flags were read from codex-cli 0.153.2's help. What they print was read
// from openai/codex's source (codex-rs/cloud-tasks, cloud-tasks-client, cli/src/login.rs) on
// 2026-10-04, not from a real task: every shape here is parsed defensively.

var (
	_ agent.CloudLister  = (*Module)(nil)
	_ agent.CloudSender  = (*Module)(nil)
	_ agent.CloudFetcher = (*Module)(nil)
	_ agent.CloudLinker  = (*Module)(nil)
	_ agent.CloudTester  = (*Module)(nil)
)

// cloudName is Codex cloud as a location.
const cloudName = "codex-cloud"

// taskBase is where a task's page is: util::task_url for the default backend.
const taskBase = "https://" + taskHost + "/codex/tasks/"

// taskHost is the only host a task's link is read from.
const taskHost = "chatgpt.com"

// maxStartingDiff bounds a diff sent with a task in CODEX_STARTING_DIFF: it travels in the
// environment of one process, and a larger change goes better on a branch.
const maxStartingDiff = 256 << 10

// cloudTimeout bounds one cloud command (a listing, a status, a diff).
const cloudTimeout = 90 * time.Second

// taskID is a task's id: task_e_… in the task links seen so far; any task_… id is read.
var taskID = regexp.MustCompile(`^task_[A-Za-z0-9_-]{4,120}$`)

// ParseCloudLink reads a task's link (chatgpt.com/codex/tasks/<id>) or its task_… id.
func (m *Module) ParseCloudLink(s string) (string, agent.SessionID, bool) {
	s = strings.TrimSpace(s)
	if u, ok := agent.LinkOn(s, taskHost); ok {
		s = taskOfLink(u)
	}
	if !taskID.MatchString(s) {
		return cloudName, "", false
	}
	return cloudName, agent.SessionID(s), true
}

// CloudURL is the task's page.
func (m *Module) CloudURL(_ string, id agent.SessionID) string { return taskBase + string(id) }

// cloudRun runs a codex cloud command here, without the variables the cloud must not
// inherit.
func cloudRun(ctx context.Context, h agent.Host, o agent.RunOptions, args ...string) (agent.Result, error) {
	o.Unset = append(o.Unset, cloud().Unset...)
	if o.Timeout == 0 {
		o.Timeout = cloudTimeout
	}
	return h.Exec().Run(ctx, append([]string{"codex"}, args...), o)
}

// NewCloudEnvs says why codex lists no cloud environment where the user made one.
const NewCloudEnvs = "Codex can't see any cloud environments from its command line. Environments made in today's Codex cloud (chatgpt.com) can't be used by the codex command yet; only older Codex cloud environments can."

// refused maps what codex printed when a cloud command failed onto the cloud sentinels.
// "Not signed in. Please run 'codex login' to sign in with ChatGPT" (also an API-key login)
// and the environment wordings are from source; a plan without cloud tasks and a repository
// the environment does not hold are guesses at the backend's words.
func refused(msg string) error {
	l := strings.ToLower(msg)
	first := strings.TrimPrefix(firstLine(msg), "Error: ")
	switch {
	case strings.Contains(l, "not signed in"), strings.Contains(l, "not logged in"), strings.Contains(l, "codex login"),
		httpCode(l, "401"), strings.Contains(l, "unauthorized"):
		return fmt.Errorf("%w: Codex here isn't signed in with ChatGPT. Cloud tasks need a ChatGPT login", agent.ErrSignedOut)
	case strings.Contains(l, "no cloud environments"):
		// Seen with codex 0.153.2 on 2026-10-05: an environment made in today's Codex cloud
		// on chatgpt.com is invisible to the command line.
		return fmt.Errorf("%w: %s", agent.ErrNoEnvironment, NewCloudEnvs)
	case strings.Contains(l, "environment") && (strings.Contains(l, "not found") || strings.Contains(l, "ambiguous")):
		return fmt.Errorf("%w: %s", agent.ErrNoEnvironment, first)
	case strings.Contains(l, "your plan"), strings.Contains(l, "upgrade"), httpCode(l, "403"), strings.Contains(l, "forbidden"),
		strings.Contains(l, "not enabled for"), strings.Contains(l, "workspace has disabled"):
		return fmt.Errorf("%w: Your plan doesn't include Codex cloud (%s)", agent.ErrNotEligible, first)
	case strings.Contains(l, "not connected"), strings.Contains(l, "repository") && strings.Contains(l, "not"):
		return fmt.Errorf("%w: %s", agent.ErrRepoUnsupported, first)
	}
	return fmt.Errorf("codex refused: %s", first)
}

// httpCode reports whether an HTTP status code appears in a message as a word (a task id
// may hold the same digits).
func httpCode(msg, code string) bool {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_])` + code + `([^A-Za-z0-9_]|$)`).MatchString(msg)
}

// output is what a failed command said: standard error, else standard output.
func output(r agent.Result) string {
	if s := strings.TrimSpace(stripANSI(string(r.Stderr))); s != "" {
		return s
	}
	return strings.TrimSpace(stripANSI(string(r.Stdout)))
}

// login asks codex which login it uses (`codex login status`, which prints to standard
// error; codex reads its own credentials, hopsesh never does) and refuses any but a
// ChatGPT one: Codex cloud "requires signing in with ChatGPT". An API key's line names the
// key (masked); it is never kept or shown.
func login(ctx context.Context, h agent.Host) error {
	r, err := h.Exec().Run(ctx, []string{"codex", "login", "status"}, agent.RunOptions{Timeout: 20 * time.Second, Unset: cloud().Unset})
	if err != nil {
		return err
	}
	l := strings.ToLower(string(r.Stdout) + string(r.Stderr))
	switch {
	case strings.Contains(l, "logged in using chatgpt"):
		return nil
	case strings.Contains(l, "api key"):
		return fmt.Errorf("%w: Codex here uses an API key. Cloud tasks need a ChatGPT login", agent.ErrSignedOut)
	case strings.Contains(l, "not logged in"):
		return fmt.Errorf("%w: Codex here isn't signed in with ChatGPT. Cloud tasks need a ChatGPT login", agent.ErrSignedOut)
	case strings.Contains(l, "logged in using"):
		// An access token, a personal access token, Bedrock: not a ChatGPT login.
		return fmt.Errorf("%w: Codex here isn't signed in with ChatGPT (%s). Cloud tasks need a ChatGPT login", agent.ErrSignedOut, loginKind(l))
	}
	return &agent.FormatError{Path: "codex login status", Err: fmt.Errorf("unexpected answer %q", firstLine(stripANSI(string(r.Stdout)+string(r.Stderr))))}
}

// loginKind names a login from `codex login status` without anything after a dash (an
// API key's masked value comes there).
func loginKind(l string) string {
	_, rest, _ := strings.Cut(firstLine(l), "logged in using ")
	rest, _, _ = strings.Cut(rest, " - ")
	return strings.TrimSpace(rest)
}

// listedTask is one task of `codex cloud list --json` (from source: run_list_command), read
// field by field so that one odd value spoils only its task.
type listedTask struct {
	ID         string
	URL        string
	Title      string
	Status     string
	Updated    time.Time
	EnvID      string
	EnvLabel   string
	Changes    string
	IsReview   bool
	Attempts   int
	unreadable error
}

// listPage is one page of `codex cloud list --json`.
type listPage struct {
	Tasks  []listedTask
	Cursor string
}

// parseList reads `codex cloud list --json`: {"tasks": [...], "cursor": "…" | null},
// pretty-printed; anything printed before the object is skipped.
func parseList(out []byte) (listPage, error) {
	i := bytes.IndexByte(out, '{')
	if i < 0 {
		return listPage{}, errors.New("no JSON object in its output")
	}
	var raw struct {
		Tasks  []json.RawMessage `json:"tasks"`
		Cursor json.RawMessage   `json:"cursor"`
	}
	if err := json.NewDecoder(bytes.NewReader(out[i:])).Decode(&raw); err != nil {
		return listPage{}, err
	}
	var p listPage
	_ = json.Unmarshal(raw.Cursor, &p.Cursor) // null or absent: no more pages
	for _, t := range raw.Tasks {
		p.Tasks = append(p.Tasks, parseTask(t))
	}
	return p, nil
}

func parseTask(b json.RawMessage) listedTask {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return listedTask{unreadable: err}
	}
	t := listedTask{ID: jsonStr(m["id"]), URL: jsonStr(m["url"]), Title: jsonStr(m["title"]), EnvID: jsonStr(m["environment_id"]),
		EnvLabel: jsonStr(m["environment_label"])}
	if t.ID == "" || !taskID.MatchString(t.ID) {
		t.unreadable = fmt.Errorf("a task without a readable id: %.80s", string(b))
		return t
	}
	t.Status = jsonStatus(m["status"])
	t.Updated = jsonTime(m["updated_at"])
	_ = json.Unmarshal(m["is_review"], &t.IsReview)
	if n, ok := jsonInt(m["attempt_total"]); ok {
		t.Attempts = n
	}
	t.Changes = jsonChanges(m["summary"])
	return t
}

// jsonStr is a string value ("" for null, absent or another type).
func jsonStr(b json.RawMessage) string {
	var s string
	if json.Unmarshal(b, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// jsonInt is a whole number, also when written as a string.
func jsonInt(b json.RawMessage) (int, bool) {
	var f float64
	if json.Unmarshal(b, &f) == nil {
		return int(f), true
	}
	if n, err := strconv.Atoi(jsonStr(b)); err == nil {
		return n, true
	}
	return 0, false
}

// jsonStatus is a task's status: the client's TaskStatus in kebab case ("pending", "ready",
// "applied", "error"), also read when an object carries it ({"state": …}).
func jsonStatus(b json.RawMessage) string {
	if s := jsonStr(b); s != "" {
		return strings.ToLower(s)
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) == nil {
		for _, k := range []string{"state", "status", "turn_status"} {
			if s := jsonStr(m[k]); s != "" {
				return strings.ToLower(s)
			}
		}
	}
	return ""
}

// jsonTime reads a time as RFC 3339 (chrono's form) or as seconds since 1970.
func jsonTime(b json.RawMessage) time.Time {
	if s := jsonStr(b); s != "" {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999Z07:00"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC()
			}
		}
		return time.Time{}
	}
	var f float64
	if json.Unmarshal(b, &f) == nil && f > 0 {
		sec := int64(f)
		return time.Unix(sec, int64((f-float64(sec))*1e9)).UTC()
	}
	return time.Time{}
}

// jsonChanges sums up a task's diff: the source prints {files_changed, lines_added,
// lines_removed}; the docs call the field a summary, so text is kept as it is.
func jsonChanges(b json.RawMessage) string {
	var d struct {
		Files   *int `json:"files_changed"`
		Added   *int `json:"lines_added"`
		Removed *int `json:"lines_removed"`
	}
	if json.Unmarshal(b, &d) == nil && d.Files != nil {
		added, removed := 0, 0
		if d.Added != nil {
			added = *d.Added
		}
		if d.Removed != nil {
			removed = *d.Removed
		}
		return changes(*d.Files, added, removed)
	}
	return clip(jsonStr(b))
}

// changes is a diff in short, for people: "+12 −3 · 2 files" ("" for no change).
func changes(files, added, removed int) string {
	if files == 0 && added == 0 && removed == 0 {
		return ""
	}
	unit := "files"
	if files == 1 {
		unit = "file"
	}
	return fmt.Sprintf("+%d −%d · %d %s", added, removed, files, unit)
}

// taskState maps a task's status onto the cloud states.
func taskState(status string) agent.CloudState {
	switch status {
	case "pending", "in_progress", "in-progress", "queued", "running":
		return agent.CloudRunning
	case "ready", "applied", "completed", "done":
		return agent.CloudDone
	case "error", "failed", "cancelled", "canceled":
		return agent.CloudFailed
	case "archived":
		return agent.CloudArchived
	}
	return agent.CloudUnknown
}

func (m *Module) session(t listedTask) agent.CloudSession {
	cs := agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(t.ID)}, Cloud: cloudName, URL: t.URL, Title: t.Title,
		State: taskState(t.Status), Updated: t.Updated, Attempts: t.Attempts, Env: t.EnvID, EnvLabel: t.EnvLabel, Changes: t.Changes}
	if !strings.HasPrefix(cs.URL, "https://") {
		cs.URL = m.CloudURL(cloudName, cs.Key.Session)
	}
	return cs
}

// maxPages and maxRefresh bound a listing: pages of `codex cloud list` (20 tasks each), and
// recorded tasks the pages left out, asked for one by one.
const (
	maxPages   = 5
	maxRefresh = 5
)

// ListCloud lists Codex cloud tasks with `codex cloud list --json` (newest first, 20 a page,
// on with --cursor up to q.Limit), only q.Env's with --env. Code reviews are left out (they
// bring nothing back). A recorded task the pages did not reach is asked for with `codex cloud
// status`. The tasks carry their environment, which is how hopsesh suggests one for a hand-off.
func (m *Module) ListCloud(ctx context.Context, h agent.Host, _ agent.Install, q agent.CloudQuery) (agent.CloudListing, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	var l agent.CloudListing
	seen := map[agent.SessionID]bool{}
	cursor := ""
	for page := 0; page < maxPages && len(l.Sessions) < limit; page++ {
		args := []string{"cloud", "list", "--json", "--limit", strconv.Itoa(min(20, limit-len(l.Sessions)))}
		if q.Env != "" {
			args = append(args, "--env", q.Env)
		}
		if cursor != "" {
			args = append(args, "--cursor", cursor)
		}
		r, err := cloudRun(ctx, h, agent.RunOptions{}, args...)
		if err != nil {
			return l, err
		}
		if r.Code != 0 {
			return l, refused(output(r))
		}
		p, err := parseList(r.Stdout)
		if err != nil {
			return l, &agent.FormatError{Path: "codex cloud list --json", Err: err}
		}
		for i, t := range p.Tasks {
			switch {
			case t.unreadable != nil:
				l.Errors = append(l.Errors, agent.SessionError{Path: fmt.Sprintf("codex cloud list/%d", page*20+i), Err: t.unreadable})
			case t.IsReview, seen[agent.SessionID(t.ID)]:
			default:
				seen[agent.SessionID(t.ID)] = true
				l.Sessions = append(l.Sessions, m.session(t))
			}
		}
		if cursor = p.Cursor; cursor == "" {
			break
		}
	}
	asked := 0
	for _, k := range q.Known {
		if seen[k] {
			continue
		}
		seen[k] = true
		if !taskID.MatchString(string(k)) {
			l.Errors = append(l.Errors, agent.SessionError{Path: "known/" + string(k), Err: errors.New("not a Codex cloud task id")})
			continue
		}
		if asked == maxRefresh || q.Env != "" {
			continue
		}
		asked++
		st, err := m.status(ctx, h, k)
		if err != nil {
			if errors.Is(err, agent.ErrSignedOut) || errors.Is(err, agent.ErrNotEligible) {
				return l, err
			}
			l.Errors = append(l.Errors, agent.SessionError{Path: "codex cloud status " + string(k), Err: err})
			continue
		}
		l.Sessions = append(l.Sessions, st.session(m))
	}
	return l, nil
}

// taskStatus is what `codex cloud status <id>` prints (from source:
// format_task_status_lines): "[READY] title", then "environment  •  3m ago", then
// "+12/-3 • 2 files" or "no diff". It exits 1 for any state but READY.
type taskStatus struct {
	ID      agent.SessionID
	Status  string // pending, ready, applied, error (lower case)
	Title   string
	Env     string // the environment's label (or id)
	Changes string
}

func (s taskStatus) session(m *Module) agent.CloudSession {
	return agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: s.ID}, Cloud: cloudName, URL: m.CloudURL(cloudName, s.ID), Title: s.Title,
		State: taskState(s.Status), EnvLabel: s.Env, Changes: s.Changes}
}

var (
	statusLine = regexp.MustCompile(`^\[([A-Za-z_ -]+)\]\s*(.*)$`)
	statLine   = regexp.MustCompile(`^\+(\d+)\s*/\s*-(\d+)\s*•\s*(\d+)\s+files?$`)
	ansi       = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")
)

func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

// status asks for one task. Its output, not its exit code, says whether it worked: status
// exits 1 for a task that is not READY.
func (m *Module) status(ctx context.Context, h agent.Host, tid agent.SessionID) (taskStatus, error) {
	r, err := cloudRun(ctx, h, agent.RunOptions{}, "cloud", "status", string(tid))
	if err != nil {
		return taskStatus{}, err
	}
	st, ok := parseStatus(string(r.Stdout))
	if !ok {
		if r.Code == 0 {
			return taskStatus{}, &agent.FormatError{Path: "codex cloud status", Err: fmt.Errorf("unexpected answer %q", firstLine(stripANSI(string(r.Stdout))))}
		}
		msg := output(r)
		if l := strings.ToLower(msg); httpCode(l, "404") || strings.Contains(l, "not found") && !strings.Contains(l, "environment") {
			return taskStatus{}, fmt.Errorf("%w: Codex cloud has no task %s", agent.ErrNotFound, tid)
		}
		return taskStatus{}, refused(msg)
	}
	st.ID = tid
	return st, nil
}

func parseStatus(out string) (taskStatus, bool) {
	var st taskStatus
	var lines []string
	for _, l := range strings.Split(stripANSI(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	for i, l := range lines {
		mm := statusLine.FindStringSubmatch(l)
		if mm == nil {
			continue
		}
		st.Status, st.Title = strings.ToLower(strings.TrimSpace(mm[1])), strings.TrimSpace(mm[2])
		for _, rest := range lines[i+1:] {
			switch sm := statLine.FindStringSubmatch(rest); {
			case sm != nil:
				added, _ := strconv.Atoi(sm[1])
				removed, _ := strconv.Atoi(sm[2])
				files, _ := strconv.Atoi(sm[3])
				st.Changes = changes(files, added, removed)
			case strings.Contains(rest, "•") && st.Env == "" && st.Changes == "":
				if env, _, ok := strings.Cut(rest, "•"); ok && !strings.HasSuffix(strings.TrimSpace(env), "ago") {
					st.Env = strings.TrimSpace(env)
				}
			}
		}
		return st, true
	}
	return st, false
}

// SendCloud starts a Codex cloud task with the briefing as its prompt:
// `codex cloud exec --env <env> --branch <branch> [--attempts N] <brief>`. The branch is
// already pushed. With r.Code ViaStartingDiff, the diff goes with the task in
// CODEX_STARTING_DIFF (on a branch that is already on the remote). It prints the task's link
// (from source: util::task_url). GitHub only; an environment is needed (`codex cloud` makes
// one; the CLI cannot).
func (m *Module) SendCloud(ctx context.Context, h agent.Host, _ agent.Install, r agent.SendRequest) (agent.Sent, error) {
	switch host, _, _ := strings.Cut(r.Repo, "/"); {
	case !strings.HasPrefix(r.Brief, agent.NotePrefix):
		return agent.Sent{}, fmt.Errorf("the briefing must start with %q", agent.NotePrefix)
	case r.Repo != "" && host != "github.com":
		return agent.Sent{}, fmt.Errorf("%w: this repository's remote is %s. Codex cloud needs GitHub", agent.ErrRepoUnsupported, host)
	case strings.TrimSpace(r.Env) == "":
		return agent.Sent{}, fmt.Errorf("%w: pick a Codex cloud environment for %s. If you have none, open `codex cloud` once to create one", agent.ErrNoEnvironment, nonEmpty(r.Repo, "this repository"))
	case r.Branch == "":
		return agent.Sent{}, errors.New("no branch given: Codex cloud starts a task from a branch on the remote")
	case r.Attempts < 0 || r.Attempts > 4:
		return agent.Sent{}, fmt.Errorf("attempts: Codex cloud runs 1 to 4, not %d", r.Attempts)
	}
	args := []string{"cloud", "exec", "--env", r.Env, "--branch", r.Branch}
	if r.Attempts > 1 {
		args = append(args, "--attempts", strconv.Itoa(r.Attempts))
	}
	args = append(args, r.Brief)
	o := agent.RunOptions{Dir: r.Dir, Timeout: 5 * time.Minute}
	switch r.Code {
	case agent.ViaStartingDiff:
		switch {
		case len(r.Diff) == 0:
			return agent.Sent{}, errors.New("a starting diff was asked for, but there is no diff")
		case len(r.Diff) > maxStartingDiff:
			return agent.Sent{}, fmt.Errorf("the changes are %d KB; a starting diff takes up to %d KB, so put them on a branch", len(r.Diff)>>10, maxStartingDiff>>10)
		}
		o.Env = append(o.Env, "CODEX_STARTING_DIFF="+string(r.Diff))
	case "", agent.ViaBranch:
	default:
		return agent.Sent{}, fmt.Errorf("%w: Codex cloud takes the code on a branch or as a starting diff, not as %s", agent.ErrUnsupported, r.Code)
	}
	res, err := cloudRun(ctx, h, o, args...)
	if err != nil {
		return agent.Sent{}, err
	}
	if res.Code != 0 {
		return agent.Sent{}, refused(output(res))
	}
	tid, url := createdTask(string(res.Stdout))
	if tid == "" {
		return agent.Sent{}, &agent.FormatError{Path: "codex cloud exec", Err: fmt.Errorf("no task link in its output: %q", firstLine(stripANSI(string(res.Stdout))))}
	}
	if url == "" {
		url = m.CloudURL(cloudName, agent.SessionID(tid))
	}
	return agent.Sent{Session: agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(tid)}, Cloud: cloudName, URL: url, Title: r.Title,
		Repo: r.Repo, Branch: r.Branch, Base: r.Base, State: agent.CloudRunning, Updated: time.Now().UTC(), Attempts: max(r.Attempts, 1), Env: r.Env}}, nil
}

// taskOfLink is the task a link on chatgpt.com names: /codex/tasks/<id>[/…] ("" otherwise).
func taskOfLink(u *url.URL) string {
	p := agent.PathParts(u)
	if len(p) < 3 || p[0] != "codex" || p[1] != "tasks" || !taskID.MatchString(p[2]) {
		return ""
	}
	return p[2]
}

// createdTask finds the new task in exec's output: its link on chatgpt.com (the last one;
// a link to any other host is not the task's), else a bare id.
func createdTask(out string) (tid, url string) {
	out = stripANSI(out)
	for _, u := range agent.LinksIn(out, taskHost) {
		if id := taskOfLink(u); id != "" {
			tid, url = id, taskBase+id
		}
	}
	if tid != "" {
		return tid, url
	}
	for _, f := range strings.Fields(out) {
		if taskID.MatchString(f) {
			tid = f
		}
	}
	return tid, ""
}

// FetchCloud brings a task back as what the codex CLI shows of it: `codex cloud status`
// (its title, state, environment and diff summary) and, once it is done, `codex cloud diff`
// (the first attempt's unified diff). The core applies the diff in its worktree and commits
// it; the title and summary come as a short conversation (FidCode). The task's messages and
// steps stay in the cloud: the CLI does not print them. A task still running brings no diff.
func (m *Module) FetchCloud(ctx context.Context, h agent.Host, _ agent.Install, sid agent.SessionID, _ agent.FetchTarget) (agent.Fetched, error) {
	if !taskID.MatchString(string(sid)) {
		return agent.Fetched{}, fmt.Errorf("%w: %q is not a Codex cloud task id", agent.ErrNotFound, sid)
	}
	st, err := m.status(ctx, h, sid)
	if err != nil {
		return agent.Fetched{}, err
	}
	f := agent.Fetched{Code: agent.CodeResult{Way: agent.ViaDiff}, Loss: []string{
		"the task's messages, steps and tool calls stay in Codex cloud: the codex command shows only its title, state and diff",
		"only the first attempt's diff comes back (best-of-N attempts stay in the cloud)",
	}}
	switch state := taskState(st.Status); state {
	case agent.CloudDone:
		r, err := cloudRun(ctx, h, agent.RunOptions{}, "cloud", "diff", string(sid))
		if err != nil {
			return agent.Fetched{}, err
		}
		switch msg := output(r); {
		case r.Code == 0:
			f.Code.Diff = unifiedDiff(r.Stdout)
		case strings.Contains(strings.ToLower(msg), "no diff available"):
			f.Loss = append(f.Loss, "Codex cloud has no diff for this task")
		default:
			return agent.Fetched{}, refused(msg)
		}
	case agent.CloudRunning:
		f.Loss = append(f.Loss, "the task is still running, so there is no diff yet")
	default:
		f.Loss = append(f.Loss, "the task ended with an error, so there is no diff")
	}
	f.Segment = taskSegment(sid, st, f.Code.Diff)
	return f, nil
}

// unifiedDiff is diff's output from its first file header on (anything printed before it is
// not part of the diff).
func unifiedDiff(out []byte) []byte {
	for _, h := range []string{"diff --git ", "--- "} {
		if i := bytes.Index(out, []byte(h)); i >= 0 && (i == 0 || out[i-1] == '\n') {
			return out[i:]
		}
	}
	return out
}

// taskSegment is a task as a short conversation: what was asked (the title Codex gave it,
// the CLI's only record of the prompt) and what came of it, in hopsesh's words.
func taskSegment(sid agent.SessionID, st taskStatus, diff []byte) *ir.Segment {
	now := time.Now().UTC()
	asked := st.Title
	if asked == "" {
		asked = "A Codex cloud task (" + string(sid) + ")"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%sCodex cloud ran this as task %s", agent.NotePrefix, sid)
	if st.Env != "" {
		fmt.Fprintf(&b, " in the environment %s", st.Env)
	}
	fmt.Fprintf(&b, "; its state is %s.", nonEmpty(strings.ToUpper(st.Status), "unknown"))
	files := diffFiles(diff)
	switch {
	case len(files) > 0:
		fmt.Fprintf(&b, " It changed %s", strings.Join(files[:min(len(files), 20)], ", "))
		if len(files) > 20 {
			fmt.Fprintf(&b, " and %d more files", len(files)-20)
		}
		if st.Changes != "" {
			fmt.Fprintf(&b, " (%s)", st.Changes)
		}
		b.WriteString("; hopsesh committed that diff here. Its messages and steps stay in Codex cloud.")
	default:
		b.WriteString(" It brought no diff. Its messages and steps stay in Codex cloud.")
	}
	seg := &ir.Segment{Header: ir.Header{Agent: string(id), SessionID: string(sid), Title: st.Title, Created: now}}
	seg.Nodes = []ir.Node{
		{Kind: ir.KindMessage, Actor: ir.User, Time: now, Text: asked},
		{Kind: ir.KindMessage, Actor: ir.Agent, Time: now, Text: b.String(), Generated: true},
	}
	ir.Chain(seg.Nodes, "")
	seg.Cursor = ir.Cursor{Head: seg.Nodes[len(seg.Nodes)-1].ID}
	return seg
}

// diffFiles are the files a unified diff changes, in order.
func diffFiles(diff []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(string(diff), "\n") {
		rest, ok := strings.CutPrefix(l, "diff --git ")
		if !ok {
			continue
		}
		if i := strings.LastIndex(rest, " b/"); i >= 0 {
			rest = rest[i+3:]
		}
		if !seen[rest] {
			seen[rest] = true
			out = append(out, rest)
		}
	}
	return out
}

// TestCloud checks, read-only, that codex is signed in with ChatGPT (`codex login status`),
// that `codex cloud --help` still has the commands this module uses, and that Codex cloud
// answers a listing (`codex cloud list --limit 1 --json`). It starts no task.
func (m *Module) TestCloud(ctx context.Context, h agent.Host, in agent.Install, _ string) (agent.CloudTest, error) {
	var t agent.CloudTest
	if err := login(ctx, h); err != nil {
		t.Checks = append(t.Checks, agent.CloudCheck{Text: trimSentinel(err)})
		return t, err
	}
	t.Account = "ChatGPT"
	if a, err := m.Account(ctx, h, in); err == nil {
		if plan := strings.TrimSpace(strings.TrimPrefix(a.Label, "ChatGPT")); plan != "" {
			t.Account = "ChatGPT (" + strings.ToUpper(plan[:1]) + plan[1:] + ")"
		}
	}
	t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: "signed in with " + t.Account})
	r, err := cloudRun(ctx, h, agent.RunOptions{Timeout: 20 * time.Second}, "cloud", "--help")
	if err != nil {
		return t, err
	}
	help := string(r.Stdout) + string(r.Stderr)
	var missing []string
	for _, c := range []string{"exec", "list", "status", "diff"} {
		if !regexp.MustCompile(`(?m)^\s+` + c + `\b`).MatchString(help) {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		t.Checks = append(t.Checks, agent.CloudCheck{Text: "`codex cloud --help` no longer lists " + strings.Join(missing, ", ")})
	} else {
		t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: "codex cloud exec, list, status and diff found"})
	}
	r, err = cloudRun(ctx, h, agent.RunOptions{}, "cloud", "list", "--limit", "1", "--json")
	if err != nil {
		return t, err
	}
	if r.Code != 0 {
		err := refused(output(r))
		t.Checks = append(t.Checks, agent.CloudCheck{Text: "codex cloud list: " + trimSentinel(err)})
		if errors.Is(err, agent.ErrSignedOut) || errors.Is(err, agent.ErrNotEligible) {
			return t, err
		}
		return t, nil
	}
	if _, err := parseList(r.Stdout); err != nil {
		t.Checks = append(t.Checks, agent.CloudCheck{Text: "codex cloud list --json printed something hopsesh can't read: " + err.Error()})
		return t, nil
	}
	t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: "codex cloud list answered"})
	return t, nil
}

// trimSentinel is an error's words without the sentinel's.
func trimSentinel(err error) string {
	msg := err.Error()
	for _, e := range []error{agent.ErrSignedOut, agent.ErrNotEligible, agent.ErrNoEnvironment, agent.ErrRepoUnsupported} {
		msg = strings.TrimPrefix(msg, e.Error()+": ")
	}
	return msg
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
