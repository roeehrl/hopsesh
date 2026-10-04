package amp

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
	// Unverified: how the CLI words a repository with no Amp project.
	repoWords = regexp.MustCompile(`(?i)no (amp )?project|project\b[^\n]*(not found|does not exist|no access)|unknown project`)
)

// SendCloud starts an Amp thread in an orb with the briefing as its prompt:
// `amp -ox <briefing> --project <owner/repo> --title <title>`, in r.Dir. The orbs manual
// says `amp -ox` "creates a new thread whose agent runs in an orb on Amp's servers, prints
// the thread URL, and exits right away", and that --project takes a GitHub owner/repo.
// Which branch the orb clones is not documented, so the cloud declares BriefBranch and the
// briefing asks the agent to check out the handoff branch first.
//
// Unverified: the exact output (the thread link is read; failing that, the thread `amp
// threads list` shows that it did not show before) and the refusal wordings. The manual
// suggests AMP_API_KEY for scripts; hopsesh sets none and uses the CLI's own login.
func (m *Module) SendCloud(ctx context.Context, h agent.Host, _ agent.Install, r agent.SendRequest) (agent.CloudSession, error) {
	host, repo, _ := strings.Cut(r.Repo, "/")
	switch {
	case !strings.HasPrefix(r.Brief, agent.NotePrefix):
		return agent.CloudSession{}, fmt.Errorf("the briefing must start with %q", agent.NotePrefix)
	case r.Repo == "" || host != "github.com" || strings.Count(repo, "/") != 1:
		return agent.CloudSession{}, fmt.Errorf("%w: hopsesh hands Amp GitHub repositories only, not %s", agent.ErrRepoUnsupported, nonEmpty(r.Repo, "this one"))
	case r.Code != "" && r.Code != agent.ViaBranch:
		return agent.CloudSession{}, fmt.Errorf("%w: an orb takes the code on a branch, not as %s", agent.ErrUnsupported, r.Code)
	}
	listed, err := run(ctx, h, "threads", "list")
	if err != nil {
		return agent.CloudSession{}, err
	}
	rows, _ := parseThreads(string(listed), time.Now())
	before := map[string]bool{}
	for _, t := range rows {
		before[t.id] = true
	}
	argv := []string{"amp", "-ox", r.Brief, "--project", repo}
	if t := strings.TrimSpace(r.Title); t != "" {
		argv = append(argv, "--title", clipTitle(t))
	}
	res, err := h.Exec().Run(ctx, argv, agent.RunOptions{Dir: r.Dir, Timeout: 2 * time.Minute, Stdin: []byte{}})
	if err != nil {
		return agent.CloudSession{}, err
	}
	if res.Code != 0 {
		text := string(res.Stderr) + "\n" + string(res.Stdout)
		if !signedOutWords.MatchString(text) && repoWords.MatchString(text) {
			return agent.CloudSession{}, fmt.Errorf("%w: Amp has no project for %s: %s", agent.ErrRepoUnsupported, repo, firstLine(text))
		}
		_, err := mapped(res, "-ox")
		return agent.CloudSession{}, err
	}
	cs := agent.CloudSession{Key: agent.SessionKey{Agent: id}, Cloud: cloudName, Title: r.Title, Repo: strings.ToLower(r.Repo), Base: r.Base,
		State: agent.CloudRunning, Updated: time.Now().UTC()}
	if sid, ok := m.linkIn(string(res.Stdout)); ok {
		cs.Key.Session = sid
	} else {
		listed, err := run(ctx, h, "threads", "list")
		if err != nil {
			return agent.CloudSession{}, err
		}
		rows, _ := parseThreads(string(listed), time.Now())
		for _, t := range rows {
			if !before[t.id] {
				cs.Key.Session, cs.Title = agent.SessionID(t.id), nonEmpty(r.Title, t.title)
				break
			}
		}
	}
	if cs.Key.Session == "" {
		return agent.CloudSession{}, &agent.FormatError{Path: "amp -ox",
			Err: fmt.Errorf("the thread started, but hopsesh could not tell which; look for it in amp threads list: %q", firstLine(string(res.Stdout)))}
	}
	cs.URL = m.CloudURL(cloudName, cs.Key.Session)
	return cs, nil
}

// linkIn finds a thread's link in a command's output, on ampcode.com exactly:
// https://ampcode.com/threads/T-….
func (m *Module) linkIn(out string) (agent.SessionID, bool) {
	for _, u := range agent.LinksIn(out, "ampcode.com") {
		if p := agent.PathParts(u); len(p) == 2 && p[0] == "threads" && threadID.MatchString(p[1]) {
			return agent.SessionID(p[1]), true
		}
	}
	return "", false
}

// clipTitle keeps a thread's title short.
func clipTitle(s string) string {
	if r := []rune(s); len(r) > 80 {
		return string(r[:80])
	}
	return s
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
