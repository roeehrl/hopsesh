package jules

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

var _ agent.CloudSender = (*Module)(nil)

var (
	// Unverified: what `jules remote new` prints. A session link, or the words "session" and
	// an id, are read; the reference shows neither.
	newWords  = regexp.MustCompile(`(?i)\bsession(?:\s+id)?[\s:#]+([0-9]{6,}|[A-Za-z0-9_-]*[0-9][A-Za-z0-9_-]{5,})\b`)
	repoWords = regexp.MustCompile(`(?i)repo(?:sitory)?\b[^\n]*(not connected|not found|not installed|no access|isn't connected|is not a source)|install the jules (?:github )?app`)
)

// SendCloud starts the session without a terminal (send) and returns it.
func (m *Module) SendCloud(ctx context.Context, h agent.Host, _ agent.Install, r agent.SendRequest) (agent.Sent, error) {
	cs, err := m.send(ctx, h, r)
	if err != nil {
		return agent.Sent{}, err
	}
	return agent.Sent{Session: cs}, nil
}

// send starts a Jules session with the briefing as its prompt:
// `jules remote new --repo <owner/repo> --session <briefing>`, in r.Dir (a worktree on the
// handoff branch). The CLI takes no starting branch: its reference lists --repo, --session
// and --parallel only, and the API's startingBranch needs an API key hopsesh does not use.
// So the cloud declares BriefBranch, and the briefing asks Jules to check out the handoff
// branch first.
//
// Unverified: the output. A session link or "session <id>" is read; failing both, the new
// session is the one `jules remote list --session` shows that it did not show before.
func (m *Module) send(ctx context.Context, h agent.Host, r agent.SendRequest) (agent.CloudSession, error) {
	host, repo, _ := strings.Cut(r.Repo, "/")
	switch {
	case !strings.HasPrefix(r.Brief, agent.NotePrefix):
		return agent.CloudSession{}, fmt.Errorf("the briefing must start with %q", agent.NotePrefix)
	case r.Repo == "" || host != "github.com" || strings.Count(repo, "/") != 1:
		return agent.CloudSession{}, fmt.Errorf("%w: Jules works on GitHub repositories, not %s", agent.ErrRepoUnsupported, nonEmpty(r.Repo, "this one"))
	case r.Code != "" && r.Code != agent.ViaBranch:
		return agent.CloudSession{}, fmt.Errorf("%w: Jules takes the code on a branch, not as %s", agent.ErrUnsupported, r.Code)
	}
	listed, err := run(ctx, h, 30*time.Second, "remote", "list", "--session")
	if err != nil {
		return agent.CloudSession{}, err
	}
	rows, _ := parseSessions(string(listed), time.Now())
	before := map[string]bool{}
	for _, x := range rows {
		before[x.id] = true
	}
	r2, err := h.Exec().Run(ctx, []string{"jules", "remote", "new", "--repo", repo, "--session", r.Brief},
		agent.RunOptions{Dir: r.Dir, Timeout: 2 * time.Minute, Stdin: []byte{}})
	if err != nil {
		return agent.CloudSession{}, err
	}
	if r2.Code != 0 {
		text := string(r2.Stderr) + "\n" + string(r2.Stdout)
		if !signedOutWords.MatchString(text) && repoWords.MatchString(text) {
			return agent.CloudSession{}, fmt.Errorf("%w: Jules can't work on %s: %s", agent.ErrRepoUnsupported, repo, firstLine(text))
		}
		_, err := mapped(r2, "remote new")
		return agent.CloudSession{}, err
	}
	out := string(r2.Stdout)
	sid := ""
	if id, ok := linkIn(out); ok {
		sid = id
	} else if mm := newWords.FindStringSubmatch(out); mm != nil {
		sid = mm[1]
	}
	title := r.Title
	if sid == "" {
		listed, err := run(ctx, h, 30*time.Second, "remote", "list", "--session")
		if err != nil {
			return agent.CloudSession{}, err
		}
		rows, _ := parseSessions(string(listed), time.Now())
		for _, x := range rows {
			if !before[x.id] && (x.repo == "" || strings.EqualFold(x.repo, repo)) {
				sid, title = x.id, nonEmpty(title, x.title)
				break
			}
		}
	}
	if !idCell.MatchString(sid) {
		return agent.CloudSession{}, &agent.FormatError{Path: "jules remote new",
			Err: fmt.Errorf("the session started, but hopsesh could not tell its id; look for it in jules remote list --session: %q", firstLine(out))}
	}
	return agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(sid)}, Cloud: cloudName, URL: URL(agent.SessionID(sid)), Title: title,
		Repo: strings.ToLower(r.Repo), Branch: r.Branch, Base: r.Base, State: agent.CloudRunning, Updated: time.Now().UTC()}, nil
}

// linkIn finds a session's link in a command's output, on jules.google.com (or
// jules.google) exactly: https://jules.google.com/session/<id>. Its id.
func linkIn(out string) (string, bool) {
	for _, u := range agent.LinksIn(out, "jules.google.com", "jules.google") {
		if p := agent.PathParts(u); len(p) == 2 && p[0] == "session" && idCell.MatchString(p[1]) {
			return p[1], true
		}
	}
	return "", false
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
