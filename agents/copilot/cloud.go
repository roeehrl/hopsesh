package copilot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// cloudName is the Copilot cloud agent as a location.
const cloudName = "copilot-cloud"

// cloud declares the Copilot cloud agent as data. Down is the session log as text (gh
// agent-task view --log; its layout is unverified) and the pull request's branch; up (a
// briefing through gh agent-task create) comes with the handoff branch.
func cloud() agent.Cloud {
	return agent.Cloud{
		Name:   cloudName,
		Title:  "Copilot cloud agent",
		Driver: "gh",
		// The agent-task commands, flags and JSON fields were read from gh 2.97.0's help; they
		// run against the stand-in gh in the tests. No real task was listed or fetched.
		Tested:       []string{"2.97"},
		Hosts:        []string{"github.com"},
		Up:           agent.FidBrief,
		Down:         agent.FidText,
		CodeUp:       []agent.CodeWay{agent.ViaBranch},
		CodeDown:     []agent.CodeWay{agent.ViaPR},
		Needs:        []agent.Need{agent.NeedGitHub, agent.NeedPushedBranch},
		VendorPrefix: "copilot/",
		Watch: agent.Watch{
			Surface: "agent tasks through `gh agent-task list --json`, `gh agent-task view --json` and `view --log`, the pull requests on copilot/… branches through `gh pr list|view --json`, `gh auth status --json hosts`, and the REST agent-tasks API (its X-GitHub-Api-Version date)",
			Docs: githubDocs("rest/agent-tasks/agent-tasks", "copilot/how-tos/copilot-cli/use-copilot-cli/delegate-tasks-to-cca",
				"copilot/concepts/agents/coding-agent/about-coding-agent"),
			Feeds: []agent.Feed{
				{Kind: agent.FeedRSS, URL: "https://github.blog/changelog/feed/"},
				{Kind: agent.FeedRSS, URL: "https://github.com/cli/cli/releases.atom"},
			},
			Grep: `copilot|agent.task|agent-task|coding agent|cloud agent|X-GitHub-Api-Version`,
			Help: [][]string{{"gh", "agent-task", "--help"}, {"gh", "agent-task", "create", "--help"}, {"gh", "agent-task", "list", "--help"},
				{"gh", "agent-task", "view", "--help"}},
			Relies: []string{"create", "list", "view", "--json", "--log", "--limit", "id", "name", "state", "repository", "pullRequestNumber",
				"pullRequestUrl", "updatedAt"},
		},
	}
}

// githubDocs are docs.github.com articles as Markdown, through the docs site's own API.
func githubDocs(pages ...string) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = "https://docs.github.com/api/article/body?pathname=/en/" + p
	}
	return out
}

var (
	_ agent.CloudLister  = (*Module)(nil)
	_ agent.CloudFetcher = (*Module)(nil)
	_ agent.CloudTester  = (*Module)(nil)
)

// taskFields are the gh agent-task JSON fields the module reads (from gh's help).
const taskFields = "id,name,state,repository,pullRequestNumber,pullRequestUrl,pullRequestTitle,createdAt,updatedAt,completedAt"

// maxLimit bounds a listing; maxKnown, how many sessions hopsesh recorded that the
// listing left out it asks about one by one; maxRepos, how many repositories' pull
// requests it reads for the branches.
const (
	maxLimit = 100
	maxKnown = 20
	maxRepos = 4
)

// run runs gh and maps a refusal onto the SDK's errors. gh holds the login; hopsesh
// never reads it.
func run(ctx context.Context, h agent.Host, timeout time.Duration, args ...string) ([]byte, error) {
	r, err := h.Exec().Run(ctx, append([]string{"gh"}, args...), agent.RunOptions{Timeout: timeout})
	if err != nil {
		return nil, err
	}
	if r.Code != 0 {
		return nil, refusal(r, "gh "+strings.Join(args[:min(2, len(args))], " "))
	}
	return r.Stdout, nil
}

var (
	// gh exits 4 when a command needs a login (gh help exit-codes).
	signedOutWords = regexp.MustCompile(`(?i)gh auth login|not logged in|authentication required|HTTP 401|bad credentials`)
	// Unverified: how gh words a plan or a policy without the cloud agent.
	notEligibleWords = regexp.MustCompile(`(?i)HTTP 403|not enabled|not available|subscription|copilot (business|enterprise|pro)|policy|forbidden`)
	notFoundWords    = regexp.MustCompile(`(?i)HTTP 404|not found|could not resolve|no agent task|no session`)
)

// refusal is a failed gh call as an error.
func refusal(r agent.Result, what string) error {
	msg := firstLine(string(r.Stderr) + "\n" + string(r.Stdout))
	text := string(r.Stderr) + string(r.Stdout)
	switch {
	case r.Code == 4 || signedOutWords.MatchString(text):
		return fmt.Errorf("%w: the GitHub CLI here is not signed in (gh auth login)", agent.ErrSignedOut)
	case notEligibleWords.MatchString(text):
		return fmt.Errorf("%w: GitHub says the Copilot cloud agent is not available to this account: %s", agent.ErrNotEligible, msg)
	case notFoundWords.MatchString(text):
		return fmt.Errorf("%w: %s", agent.ErrNotFound, msg)
	}
	return fmt.Errorf("%s: exit %d: %s", what, r.Code, msg)
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// task is one agent task in gh's JSON. The field names are gh's (its help lists them);
// their value types are not documented, so repository and the pull request number are
// read in more than one shape.
type task struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	State       string          `json:"state"`
	Repository  json.RawMessage `json:"repository"`
	PRNumber    json.RawMessage `json:"pullRequestNumber"`
	PRURL       string          `json:"pullRequestUrl"`
	PRTitle     string          `json:"pullRequestTitle"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
	CompletedAt string          `json:"completedAt"`
}

// repo is the task's repository as owner/repo: a string, or an object with
// nameWithOwner, fullName/full_name, or name and owner (unverified which).
func (t task) repo() string {
	var s string
	if json.Unmarshal(t.Repository, &s) == nil {
		return strings.Trim(strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "github.com/"), "/")
	}
	var o struct {
		NameWithOwner string `json:"nameWithOwner"`
		FullName      string `json:"fullName"`
		FullName2     string `json:"full_name"`
		Name          string `json:"name"`
		Owner         struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if json.Unmarshal(t.Repository, &o) != nil {
		return ""
	}
	switch {
	case o.NameWithOwner != "":
		return o.NameWithOwner
	case o.FullName != "":
		return o.FullName
	case o.FullName2 != "":
		return o.FullName2
	case o.Name != "" && o.Owner.Login != "":
		return o.Owner.Login + "/" + o.Name
	}
	return ""
}

// pr is the task's pull request number (0: none yet), a number or a string; failing both,
// the end of its link.
func (t task) pr() int {
	var n int
	if json.Unmarshal(t.PRNumber, &n) == nil && n > 0 {
		return n
	}
	var s string
	if json.Unmarshal(t.PRNumber, &s) == nil {
		if n, err := strconv.Atoi(strings.TrimPrefix(s, "#")); err == nil {
			return n
		}
	}
	if i := strings.LastIndex(t.PRURL, "/pull/"); i >= 0 {
		if n, err := strconv.Atoi(strings.SplitN(t.PRURL[i+6:], "/", 2)[0]); err == nil {
			return n
		}
	}
	return 0
}

// state maps the REST API's task states (queued, in_progress, completed, failed, idle,
// waiting_for_user, timed_out, cancelled), in any case or spacing.
func state(s string) agent.CloudState {
	switch strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(strings.TrimSpace(s))) {
	case "queued", "in_progress", "running", "pending":
		return agent.CloudRunning
	case "idle", "waiting_for_user":
		return agent.CloudIdle
	case "completed", "complete", "succeeded":
		return agent.CloudDone
	case "failed", "timed_out", "cancelled", "canceled", "error":
		return agent.CloudFailed
	}
	return agent.CloudUnknown
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// session is a task as a cloud session; branches maps repo#number to the pull request's
// head branch.
func session(t task, branches map[string]string) agent.CloudSession {
	repo := t.repo()
	cs := agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(t.ID)}, Cloud: cloudName, Title: strings.TrimSpace(t.Name),
		State: state(t.State), Updated: parseTime(t.UpdatedAt)}
	if cs.Title == "" {
		cs.Title = t.PRTitle
	}
	if cs.Updated.IsZero() {
		cs.Updated = parseTime(nonEmpty(t.CompletedAt, t.CreatedAt))
	}
	if repo != "" {
		cs.Repo = strings.ToLower("github.com/" + repo)
	}
	if n := t.pr(); n > 0 {
		cs.PR = "#" + strconv.Itoa(n)
		cs.Branch = branches[strings.ToLower(repo)+"#"+strconv.Itoa(n)]
		prURL := t.PRURL
		if prURL == "" && repo != "" {
			prURL = fmt.Sprintf("https://github.com/%s/pull/%d", repo, n)
		}
		if prURL != "" {
			cs.URL = strings.TrimSuffix(prURL, "/") + "/agent-sessions/" + t.ID // gh agent-task's documented link form
		}
	}
	return cs
}

// ListCloud lists the agent tasks gh can see (gh agent-task list --json), refreshes the
// ones hopsesh recorded that the listing leaves out (view --json), and reads the branches
// of their pull requests (gh pr list --json, once per repository).
func (m *Module) ListCloud(ctx context.Context, h agent.Host, _ agent.Install, q agent.CloudQuery) (agent.CloudListing, error) {
	limit := q.Limit
	if limit <= 0 || limit > maxLimit {
		limit = 30
	}
	out, err := run(ctx, h, 30*time.Second, "agent-task", "list", "--json", taskFields, "--limit", strconv.Itoa(limit))
	if err != nil {
		return agent.CloudListing{}, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		return agent.CloudListing{}, &agent.FormatError{Path: "gh agent-task list", Err: err}
	}
	var l agent.CloudListing
	var tasks []task
	seen := map[string]bool{}
	for i, r := range raw {
		var t task
		if err := json.Unmarshal(r, &t); err != nil || t.ID == "" {
			if err == nil {
				err = errors.New("a task without an id")
			}
			l.Errors = append(l.Errors, agent.SessionError{Path: fmt.Sprintf("gh agent-task list[%d]", i), Err: err})
			continue
		}
		seen[t.ID] = true
		tasks = append(tasks, t)
	}
	asked := 0
	for _, k := range q.Known {
		if seen[string(k)] || asked == maxKnown {
			continue
		}
		seen[string(k)], asked = true, asked+1
		t, err := view(ctx, h, string(k))
		if err != nil {
			if errors.Is(err, agent.ErrSignedOut) || errors.Is(err, agent.ErrNotEligible) {
				return agent.CloudListing{}, err
			}
			l.Errors = append(l.Errors, agent.SessionError{Path: "known/" + string(k), Err: err})
			continue
		}
		tasks = append(tasks, t)
	}
	branches := prBranches(ctx, h, tasks)
	for _, t := range tasks {
		cs := session(t, branches)
		if q.Repo != "" && cs.Repo != q.Repo {
			continue
		}
		l.Sessions = append(l.Sessions, cs)
	}
	return l, nil
}

// view reads one task (gh agent-task view <session id> --json).
func view(ctx context.Context, h agent.Host, sid string) (task, error) {
	if strings.HasPrefix(sid, "-") {
		return task{}, fmt.Errorf("%w: %q is not a Copilot agent session id", agent.ErrNotFound, sid)
	}
	out, err := run(ctx, h, 30*time.Second, "agent-task", "view", sid, "--json", taskFields)
	if err != nil {
		return task{}, err
	}
	var t task
	if err := json.Unmarshal(out, &t); err != nil {
		return task{}, &agent.FormatError{Path: "gh agent-task view", Err: err}
	}
	if t.ID == "" {
		t.ID = sid
	}
	return t, nil
}

// pullRequest is a pull request in gh pr's documented JSON fields.
type pullRequest struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
}

// prBranches reads the head branches of the tasks' pull requests: one gh pr list per
// repository (the newest 100 pull requests, open or not), for at most maxRepos
// repositories and while the listing has time left. What it cannot read stays unknown:
// FetchCloud asks again.
func prBranches(ctx context.Context, h agent.Host, tasks []task) map[string]string {
	want := map[string]bool{}
	for _, t := range tasks {
		if r := t.repo(); r != "" && t.pr() > 0 {
			want[strings.ToLower(r)] = true
		}
	}
	repos := make([]string, 0, len(want))
	for r := range want {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	out := map[string]string{}
	for i, r := range repos {
		if i == maxRepos || ctx.Err() != nil {
			break
		}
		if d, ok := ctx.Deadline(); ok && time.Until(d) < 3*time.Second {
			break // the listing itself matters more than its branches
		}
		b, err := run(ctx, h, 20*time.Second, "pr", "list", "--repo", r, "--state", "all", "--limit", "100", "--json", "number,headRefName")
		if err != nil {
			continue
		}
		var prs []pullRequest
		if json.Unmarshal(b, &prs) != nil {
			continue
		}
		for _, p := range prs {
			if p.HeadRefName != "" {
				out[r+"#"+strconv.Itoa(p.Number)] = p.HeadRefName
			}
		}
	}
	return out
}

// maxLog bounds the session log hopsesh keeps.
const maxLog = 4 << 20

// FetchCloud reads a task, its pull request's branch (gh pr view) and its session log (gh
// agent-task view --log). The code comes home as the pull request's branch, which the
// core fetches into a worktree; the log is the conversation as text. Neither changes
// anything on GitHub.
func (m *Module) FetchCloud(ctx context.Context, h agent.Host, _ agent.Install, sid agent.SessionID, _ agent.FetchTarget) (agent.Fetched, error) {
	t, err := view(ctx, h, string(sid))
	if err != nil {
		return agent.Fetched{}, err
	}
	f := agent.Fetched{Loss: []string{
		"tool calls and their output come only as the session log's text",
		"review comments and the pull request's discussion stay on GitHub",
	}}
	repo, n := t.repo(), t.pr()
	if n > 0 && repo != "" {
		b, err := run(ctx, h, 20*time.Second, "pr", "view", strconv.Itoa(n), "--repo", repo, "--json", "number,headRefName")
		if err != nil {
			return agent.Fetched{}, err
		}
		var p pullRequest
		if err := json.Unmarshal(b, &p); err != nil {
			return agent.Fetched{}, &agent.FormatError{Path: "gh pr view", Err: err}
		}
		f.Code = agent.CodeResult{Way: agent.ViaPR, Branch: p.HeadRefName, PR: "#" + strconv.Itoa(n)}
	} else {
		f.Loss = append(f.Loss, "the agent has not opened a pull request yet, so there is no code to bring")
	}
	logOut, err := run(ctx, h, time.Minute, "agent-task", "view", string(sid), "--log")
	switch {
	case errors.Is(err, agent.ErrSignedOut), errors.Is(err, agent.ErrNotEligible):
		return agent.Fetched{}, err
	case err != nil:
		f.Loss = append(f.Loss, "the session log could not be read: "+err.Error())
	default:
		f.Segment = logSegment(t, logOut)
	}
	return f, nil
}

// logSegment is a session log as the conversation: the task's request and the log's text
// as the agent's message. The log's layout is unverified: what gh prints for a terminal is
// kept as it is; raw server-sent events ("data: {…}" chat-completion chunks) are reduced
// to their text.
func logSegment(t task, log []byte) *ir.Segment {
	text := logText(log)
	created := parseTime(t.CreatedAt)
	seg := &ir.Segment{Header: ir.Header{Agent: string(id), SessionID: t.ID, Title: t.Name, Created: created}}
	if t.Name != "" {
		seg.Nodes = append(seg.Nodes, ir.Node{Kind: ir.KindMessage, Actor: ir.User, Time: created, Text: t.Name})
	}
	if text != "" {
		seg.Nodes = append(seg.Nodes, ir.Node{Kind: ir.KindMessage, Actor: ir.Agent, Time: parseTime(nonEmpty(t.UpdatedAt, t.CreatedAt)), Text: text})
	}
	ir.Chain(seg.Nodes, "")
	return seg
}

// logText is the readable text of a log: server-sent chat-completion chunks joined, or
// the text itself, without terminal colour codes, at most maxLog bytes.
func logText(b []byte) string {
	if len(b) > maxLog {
		b = b[:maxLog]
	}
	b = ansi.ReplaceAll(b, nil)
	var sse strings.Builder
	chunks := 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), maxLog)
	for sc.Scan() {
		line, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "data:")
		if !ok || !strings.HasPrefix(strings.TrimSpace(line), "{") {
			continue
		}
		var c struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(line), &c) != nil {
			continue
		}
		chunks++
		for _, ch := range c.Choices {
			sse.WriteString(ch.Delta.Content)
			sse.WriteString(ch.Message.Content)
		}
	}
	if chunks > 0 {
		return strings.TrimSpace(sse.String())
	}
	return strings.TrimSpace(string(b))
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// TestCloud asks gh which GitHub login it uses (gh auth status --json hosts; never the
// token), asks for one task to learn whether the account has the cloud agent, and checks
// that gh still has the agent-task commands the module uses. It starts nothing.
func (m *Module) TestCloud(ctx context.Context, h agent.Host, _ agent.Install, _ string) (agent.CloudTest, error) {
	var t agent.CloudTest
	fail := func(err error) (agent.CloudTest, error) {
		msg := err.Error()
		for _, e := range []error{agent.ErrSignedOut, agent.ErrNotEligible} {
			msg = strings.TrimPrefix(msg, e.Error()+": ")
		}
		t.Checks = append(t.Checks, agent.CloudCheck{Text: msg})
		return t, err
	}
	out, err := run(ctx, h, 20*time.Second, "auth", "status", "--json", "hosts", "--hostname", "github.com")
	if err != nil {
		return fail(err)
	}
	login, err := activeLogin(out)
	if err != nil {
		return fail(err)
	}
	t.Account = "github.com · " + login
	t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: t.Account + " login"})
	if _, err := run(ctx, h, 30*time.Second, "agent-task", "list", "--limit", "1", "--json", "id"); err != nil {
		return fail(err)
	}
	t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: "the Copilot cloud agent answers for this account"})
	for _, c := range []struct{ argv, want []string }{
		{[]string{"agent-task", "--help"}, []string{"list", "view"}},
		{[]string{"agent-task", "view", "--help"}, []string{"--log", "--json"}},
	} {
		r, err := h.Exec().Run(ctx, append([]string{"gh"}, c.argv...), agent.RunOptions{Timeout: 20 * time.Second})
		if err != nil {
			return t, err
		}
		help := string(r.Stdout) + string(r.Stderr)
		for _, w := range c.want {
			if strings.Contains(help, w) {
				t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: "gh " + c.argv[0] + " " + w + " found"})
			} else {
				t.Checks = append(t.Checks, agent.CloudCheck{Text: "`gh " + strings.Join(c.argv, " ") + "` no longer lists " + w})
			}
		}
	}
	return t, nil
}

// activeLogin reads gh auth status --json hosts (the shape in gh's source: hosts → a host
// → its accounts, each with state, active and login) for github.com's active account.
// The token field is not read (gh leaves it out without --show-token).
func activeLogin(b []byte) (string, error) {
	var st struct {
		Hosts map[string][]struct {
			State  string `json:"state"`
			Active bool   `json:"active"`
			Login  string `json:"login"`
			Error  string `json:"error"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return "", &agent.FormatError{Path: "gh auth status", Err: err}
	}
	for _, a := range st.Hosts["github.com"] {
		if !a.Active {
			continue
		}
		if a.State != "" && a.State != "success" {
			return "", fmt.Errorf("%w: the GitHub CLI's login for %s does not work (%s); run gh auth login", agent.ErrSignedOut, a.Login, nonEmpty(a.Error, a.State))
		}
		return nonEmpty(a.Login, "signed in"), nil
	}
	return "", fmt.Errorf("%w: the GitHub CLI here is not signed in to github.com (gh auth login)", agent.ErrSignedOut)
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
