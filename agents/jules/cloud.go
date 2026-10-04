package jules

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// cloudName is Jules as a location.
const cloudName = "jules"

// cloud declares Jules as data. Down is the code only: the CLI prints a session's patch
// (jules remote pull); its plan, messages and command output are in the Jules API, which
// needs an API key, so they stay in the cloud. Up (jules remote new) comes with the
// handoff branch.
func cloud() agent.Cloud {
	return agent.Cloud{
		Name:   cloudName,
		Title:  "Jules",
		Driver: "jules",
		// 0.1.42 is the npm release current when the module was written; its commands come
		// from the CLI reference and scripts that drive it. Nothing ran against a real Jules:
		// the tests run the stand-in jules.
		Tested:   []string{"0.1.42"},
		Hosts:    []string{"github.com"},
		Up:       agent.FidBrief,
		Down:     agent.FidCode,
		CodeUp:   []agent.CodeWay{agent.ViaBranch},
		CodeDown: []agent.CodeWay{agent.ViaDiff},
		Needs:    []agent.Need{agent.NeedGitHub, agent.NeedPushedBranch},
		Watch: agent.Watch{
			Surface: "Jules sessions through `jules remote list --session` (a table: ID, description, repo, last active, status), `jules remote list --repo` and `jules remote pull --session <id>` (the session's patch), and the v1alpha REST API (its discovery document) for what the CLI leaves out",
			Docs:    []string{julesDiscovery, "https://jules.google/docs/cli/reference.md"},
			Feeds:   []agent.Feed{{Kind: agent.FeedMarkdown, URL: "https://jules.google/docs/changelog.md"}},
			Grep:    `session|activit|remote|pull|teleport|api|deprecat`,
			Help:    [][]string{{"jules", "--help"}, {"jules", "remote", "--help"}},
			Relies:  []string{"remote", "list", "pull", "--session", "--repo"},
		},
	}
}

const julesDiscovery = "https://jules.googleapis.com/$discovery/rest?version=v1alpha"

var (
	_ agent.CloudLister  = (*Module)(nil)
	_ agent.CloudFetcher = (*Module)(nil)
	_ agent.CloudTester  = (*Module)(nil)
)

var (
	// Unverified: how the CLI words a missing login, a plan or region without Jules, and an
	// unknown session.
	signedOutWords   = regexp.MustCompile(`(?i)not logged in|jules login|log ?in first|unauthenticated|unauthori[sz]ed|\b401\b|credentials`)
	notEligibleWords = regexp.MustCompile(`(?i)not available|not eligible|not enabled|quota|limit reached|\b403\b|permission denied|waitlist|region`)
	notFoundWords    = regexp.MustCompile(`(?i)not found|\b404\b|no such session|unknown session`)
)

// run runs jules and maps a refusal onto the SDK's errors. The CLI holds the Google
// login; hopsesh never reads it. Standard input is empty, as scripts that drive the CLI
// without a terminal advise.
func run(ctx context.Context, h agent.Host, timeout time.Duration, args ...string) ([]byte, error) {
	r, err := h.Exec().Run(ctx, append([]string{"jules"}, args...), agent.RunOptions{Timeout: timeout, Stdin: []byte{}})
	if err != nil {
		return nil, err
	}
	if r.Code != 0 {
		text := string(r.Stderr) + "\n" + string(r.Stdout)
		msg := firstLine(text)
		switch {
		case signedOutWords.MatchString(text):
			return nil, fmt.Errorf("%w: Jules here is not logged in (jules login)", agent.ErrSignedOut)
		case notEligibleWords.MatchString(text):
			return nil, fmt.Errorf("%w: Jules refused: %s", agent.ErrNotEligible, msg)
		case notFoundWords.MatchString(text):
			return nil, fmt.Errorf("%w: %s", agent.ErrNotFound, msg)
		}
		return nil, fmt.Errorf("jules %s: exit %d: %s", strings.Join(args[:min(2, len(args))], " "), r.Code, msg)
	}
	return r.Stdout, nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// URL is a session's page (unverified: the Jules API gives each session a url, and the
// console's links have this form).
func URL(sid agent.SessionID) string { return "https://jules.google.com/session/" + string(sid) }

// ListCloud lists Jules sessions (jules remote list --session). The CLI prints a table
// whose columns are split by two or more spaces; neither the layout nor its words are
// documented, so the parser finds the columns by the header's names when it can and by
// the shape of each cell otherwise. A row it cannot read goes in Errors.
func (m *Module) ListCloud(ctx context.Context, h agent.Host, _ agent.Install, q agent.CloudQuery) (agent.CloudListing, error) {
	out, err := run(ctx, h, 30*time.Second, "remote", "list", "--session")
	if err != nil {
		return agent.CloudListing{}, err
	}
	rows, errs := parseSessions(string(out), time.Now())
	l := agent.CloudListing{Errors: errs}
	for _, r := range rows {
		cs := agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(r.id)}, Cloud: cloudName, URL: URL(agent.SessionID(r.id)),
			Title: r.title, State: r.state, Updated: r.updated}
		if r.repo != "" {
			cs.Repo = strings.ToLower("github.com/" + r.repo)
		}
		if q.Repo != "" && cs.Repo != q.Repo {
			continue
		}
		l.Sessions = append(l.Sessions, cs)
		if q.Limit > 0 && len(l.Sessions) == q.Limit {
			break
		}
	}
	return l, nil
}

// row is one session in the listing.
type row struct {
	id, title, repo string
	state           agent.CloudState
	updated         time.Time
}

var (
	cellSplit = regexp.MustCompile(`\s{2,}|\t+`)
	idCell    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{3,}$`)
	repoCell  = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	agoCell   = regexp.MustCompile(`(?i)^(\d+)\s*(s|sec|secs|seconds?|m|min|mins|minutes?|h|hr|hrs|hours?|d|days?|w|weeks?)\s+ago$`)
	rule      = regexp.MustCompile(`^[\s\-─━=+|]+$`)
)

// parseSessions reads `jules remote list --session` (unverified layout, assumed from the
// scripts that read it: "ID  Description  Repo  Last active  Status", one session a row).
func parseSessions(out string, now time.Time) ([]row, []agent.SessionError) {
	col := map[string]int{"id": 0, "title": 1, "repo": 2, "updated": 3, "state": 4}
	header := false
	var rows []row
	var errs []agent.SessionError
	for n, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || rule.MatchString(line) {
			continue
		}
		cells := cellSplit.Split(line, -1)
		if !header && isHeader(cells) {
			header = true
			for i, c := range cells {
				switch k := strings.ToLower(c); {
				case k == "id" || strings.Contains(k, "session"):
					col["id"] = i
				case strings.Contains(k, "desc") || strings.Contains(k, "title") || strings.Contains(k, "prompt") || strings.Contains(k, "task"):
					col["title"] = i
				case strings.Contains(k, "repo") || strings.Contains(k, "source"):
					col["repo"] = i
				case strings.Contains(k, "active") || strings.Contains(k, "updat") || strings.Contains(k, "time") || strings.Contains(k, "date"):
					col["updated"] = i
				case strings.Contains(k, "status") || strings.Contains(k, "state"):
					col["state"] = i
				}
			}
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "no ") && strings.Contains(strings.ToLower(line), "session") {
			continue // "No sessions found." (unverified wording)
		}
		r, ok := rowOf(cells, col, now)
		if !ok {
			errs = append(errs, agent.SessionError{Path: fmt.Sprintf("jules remote list line %d", n+1), Err: fmt.Errorf("unreadable row %q", clip(line, 80))})
			continue
		}
		rows = append(rows, r)
	}
	return rows, errs
}

func isHeader(cells []string) bool {
	if len(cells) < 2 {
		return false
	}
	first := strings.ToLower(cells[0])
	last := strings.ToLower(cells[len(cells)-1])
	return (first == "id" || first == "session" || first == "session id") && (strings.Contains(last, "status") || strings.Contains(last, "state") || len(cells) >= 3)
}

// rowOf reads one row by the header's columns; a row with another number of cells (a
// description with two spaces in it, an empty column) is read by the cells' shapes: the
// id first, the status last, the repository and the time where they look like one.
func rowOf(cells []string, col map[string]int, now time.Time) (row, bool) {
	at := func(k string) string {
		if i := col[k]; i < len(cells) {
			return cells[i]
		}
		return ""
	}
	var r row
	if len(cells) == len(col) {
		r = row{id: at("id"), title: at("title"), repo: at("repo"), updated: when(at("updated"), now), state: state(at("state"))}
	} else {
		if len(cells) < 2 {
			return row{}, false
		}
		r.id, r.state = cells[0], state(cells[len(cells)-1])
		var rest []string
		for _, c := range cells[1 : len(cells)-1] {
			switch {
			case r.repo == "" && repoCell.MatchString(c):
				r.repo = c
			case r.updated.IsZero() && !when(c, now).IsZero():
				r.updated = when(c, now)
			default:
				rest = append(rest, c)
			}
		}
		r.title = strings.Join(rest, "  ")
	}
	if !idCell.MatchString(r.id) {
		return row{}, false
	}
	if !repoCell.MatchString(r.repo) {
		r.repo = ""
	}
	return r, true
}

// state maps a status as the list words it (Completed, In Progress, Planning, Awaiting
// User Feedback, Failed, …: the API's states in words; unverified).
func state(s string) agent.CloudState {
	k := strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(strings.TrimSpace(s)))
	switch {
	case strings.Contains(k, "complete") || k == "done" || k == "succeeded":
		return agent.CloudDone
	case strings.Contains(k, "fail") || strings.Contains(k, "cancel") || strings.Contains(k, "error"):
		return agent.CloudFailed
	case strings.Contains(k, "await") || strings.Contains(k, "paused") || strings.Contains(k, "waiting") || strings.Contains(k, "feedback"):
		return agent.CloudIdle
	case strings.Contains(k, "progress") || strings.Contains(k, "planning") || strings.Contains(k, "running") || strings.Contains(k, "queued") || strings.Contains(k, "pending"):
		return agent.CloudRunning
	case strings.Contains(k, "archived"):
		return agent.CloudArchived
	}
	return agent.CloudUnknown
}

// when reads a "Last active" cell: "5m ago", "2 hours ago", "just now", "yesterday", or a
// timestamp. Zero when it is none of those.
func when(s string, now time.Time) time.Time {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "just now", "now":
		return now
	case "yesterday":
		return now.Add(-24 * time.Hour)
	}
	if m := agoCell.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[strings.ToLower(m[2])[0]]
		return now.Add(-time.Duration(n) * unit)
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// FetchCloud brings a session's code: jules remote pull --session <id> without --apply
// prints the session's patch (unverified: whether anything precedes it), which the core
// applies in a new worktree and commits on a branch of its own. The plan, messages and
// command output stay in the cloud.
func (m *Module) FetchCloud(ctx context.Context, h agent.Host, _ agent.Install, sid agent.SessionID, _ agent.FetchTarget) (agent.Fetched, error) {
	if !idCell.MatchString(string(sid)) {
		return agent.Fetched{}, fmt.Errorf("%w: %q is not a Jules session id", agent.ErrNotFound, sid)
	}
	out, err := run(ctx, h, 2*time.Minute, "remote", "pull", "--session", string(sid))
	if err != nil {
		return agent.Fetched{}, err
	}
	diff := patchOf(out)
	if len(diff) == 0 {
		return agent.Fetched{}, fmt.Errorf("%w: Jules has no code changes for session %s yet", agent.ErrNotFound, sid)
	}
	return agent.Fetched{
		Code: agent.CodeResult{Way: agent.ViaDiff, Diff: diff},
		Loss: []string{
			"Jules's plan, messages and command output stay in the cloud: the jules CLI does not print them",
			"a pull request Jules opened stays on GitHub; the patch is the session's latest change set",
		},
	}, nil
}

// patchOf is the unified diff in a pull's output: from the first "diff --git" or "--- "
// line on (anything before it is the CLI's own words).
func patchOf(out []byte) []byte {
	s := string(out)
	for _, mark := range []string{"diff --git ", "--- "} {
		if strings.HasPrefix(s, mark) {
			return out
		}
		if i := strings.Index(s, "\n"+mark); i >= 0 {
			return out[i+1:]
		}
	}
	return nil
}

// TestCloud asks Jules for the repositories it is connected to (jules remote list --repo:
// read-only; it needs the login and an account with Jules) and checks that the CLI still
// has the commands the module uses. It starts nothing.
func (m *Module) TestCloud(ctx context.Context, h agent.Host, _ agent.Install, _ string) (agent.CloudTest, error) {
	var t agent.CloudTest
	out, err := run(ctx, h, 30*time.Second, "remote", "list", "--repo")
	if err != nil {
		msg := err.Error()
		for _, e := range []error{agent.ErrSignedOut, agent.ErrNotEligible} {
			msg = strings.TrimPrefix(msg, e.Error()+": ")
		}
		t.Checks = append(t.Checks, agent.CloudCheck{Text: msg})
		return t, err
	}
	n := 0
	for _, l := range strings.Split(string(out), "\n") {
		if repoCell.MatchString(strings.TrimSpace(strings.Fields(l + " x")[0])) {
			n++
		}
	}
	t.Account = "Google account (jules login)"
	t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: fmt.Sprintf("logged in; %d connected repositories", n)})
	r, err := h.Exec().Run(ctx, []string{"jules", "remote", "--help"}, agent.RunOptions{Timeout: 20 * time.Second, Stdin: []byte{}})
	if err != nil {
		return t, err
	}
	help := string(r.Stdout) + string(r.Stderr)
	for _, w := range []string{"list", "pull", "--session"} {
		if strings.Contains(help, w) {
			t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: "jules remote " + w + " found"})
		} else {
			t.Checks = append(t.Checks, agent.CloudCheck{Text: "`jules remote --help` no longer lists " + w})
		}
	}
	return t, nil
}
