package devin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

var _ agent.CloudSender = (*Module)(nil)

// sendWait is how long hopsesh waits for `devin --cloud -p` to print Devin's first reply.
// The session goes on in the cloud either way ("The session persists"), so a reply that
// takes longer only means hopsesh finds the session in the listing.
var sendWait = 3 * time.Minute

var (
	// Unverified: how the CLI words a repository Devin cannot reach.
	repoWords = regexp.MustCompile(`(?i)repo(?:sitory)?\b[^\n]*(not connected|not found|no access|not indexed|isn't connected|not set up)|connect (?:the|your) repo`)
	devinWord = regexp.MustCompile(`\b(devin-[0-9a-f]{16,64})\b`)
)

// SendCloud starts the session without a terminal (send) and returns it.
func (m *Module) SendCloud(ctx context.Context, h agent.Host, _ agent.Install, r agent.SendRequest) (agent.Sent, error) {
	cs, err := m.send(ctx, h, r)
	if err != nil {
		return agent.Sent{}, err
	}
	return agent.Sent{Session: cs}, nil
}

// send starts a Devin Cloud session with the briefing as its first prompt:
// `devin --cloud --respect-workspace-trust false -p -- <briefing>` in r.Dir, a worktree on
// the handoff branch. The docs say `--cloud -p` "starts a session, sends one prompt, prints
// Devin's response to stdout, and exits. The session persists". `--respect-workspace-trust
// false` is their advice for scripts: print mode cannot show the trust prompt, and the
// worktree is one hopsesh just made.
//
// Unverified, and marked so for the user (Limits): which repository and branch a session
// started this way works on (the docs choose them interactively, with /repo), so the
// briefing names both (BriefBranch); and what -p prints. A session link or devin-… id in
// the reply is read; otherwise the session is the cloud session `devin list --format json`
// (in r.Dir) shows that it did not show before. A reply that takes longer than sendWait is
// not waited for: the session is looked up the same way.
func (m *Module) send(ctx context.Context, h agent.Host, r agent.SendRequest) (agent.CloudSession, error) {
	host, repo, _ := strings.Cut(r.Repo, "/")
	switch {
	case !strings.HasPrefix(r.Brief, agent.NotePrefix):
		return agent.CloudSession{}, fmt.Errorf("the briefing must start with %q", agent.NotePrefix)
	case r.Repo == "" || host != "github.com" || strings.Count(repo, "/") != 1:
		return agent.CloudSession{}, fmt.Errorf("%w: hopsesh hands Devin GitHub repositories only, not %s", agent.ErrRepoUnsupported, nonEmpty(r.Repo, "this one"))
	case r.Code != "" && r.Code != agent.ViaBranch:
		return agent.CloudSession{}, fmt.Errorf("%w: Devin takes the code on a branch, not as %s", agent.ErrUnsupported, r.Code)
	}
	recs, _, err := listIn(ctx, h, r.Dir)
	if err != nil {
		return agent.CloudSession{}, err
	}
	before := map[string]bool{}
	for _, x := range recs {
		before[x.sid()] = true
	}
	res, err := h.Exec().Run(ctx, []string{"devin", "--cloud", "--respect-workspace-trust", "false", "-p", "--", r.Brief},
		agent.RunOptions{Dir: r.Dir, Timeout: sendWait, Stdin: []byte{}})
	if err != nil {
		return agent.CloudSession{}, err
	}
	text := string(res.Stderr) + "\n" + string(res.Stdout)
	if res.Code != 0 {
		switch {
		case signedOutWords.MatchString(text):
			return agent.CloudSession{}, fmt.Errorf("%w: the Devin CLI here is not logged in (devin auth login)", agent.ErrSignedOut)
		case repoWords.MatchString(text):
			return agent.CloudSession{}, fmt.Errorf("%w: Devin can't work on %s: %s", agent.ErrRepoUnsupported, repo, firstLine(text))
		case notEligibleWords.MatchString(text):
			return agent.CloudSession{}, fmt.Errorf("%w: Devin refused: %s", agent.ErrNotEligible, firstLine(text))
		}
		// Stopped waiting for the reply, or a failure in words hopsesh does not know: the
		// listing says whether a session started.
	}
	cs := agent.CloudSession{Key: agent.SessionKey{Agent: id}, Cloud: cloudName, Title: r.Title, Repo: strings.ToLower(r.Repo), Branch: "",
		Base: r.Base, State: agent.CloudRunning, Updated: time.Now().UTC()}
	if sid, ok := sessionIn(string(res.Stdout)); ok {
		cs.Key.Session = sid
	} else {
		recs, _, err := listIn(ctx, h, r.Dir)
		if err != nil {
			return agent.CloudSession{}, err
		}
		for _, x := range recs {
			if !before[x.sid()] {
				s := m.session(x)
				cs.Key.Session, cs.URL, cs.Title = s.Key.Session, s.URL, nonEmpty(r.Title, s.Title)
				break
			}
		}
	}
	if cs.Key.Session == "" {
		if res.Code != 0 {
			return agent.CloudSession{}, fmt.Errorf("devin --cloud -p: exit %d: %s", res.Code, firstLine(text))
		}
		return agent.CloudSession{}, &agent.FormatError{Path: "devin --cloud -p",
			Err: fmt.Errorf("the session started, but hopsesh could not find the new session in devin list; look for it on app.devin.ai: %q", clip(firstLine(text), 120))}
	}
	if cs.URL == "" {
		cs.URL = m.CloudURL(cloudName, cs.Key.Session)
	}
	return cs, nil
}

// sessionIn finds the new session in Devin's reply: its link on app.devin.ai exactly
// (https://app.devin.ai/sessions/<hex>), else a devin-<hex> id.
func sessionIn(out string) (agent.SessionID, bool) {
	for _, u := range agent.LinksIn(out, "app.devin.ai") {
		if p := agent.PathParts(u); len(p) == 2 && p[0] == "sessions" {
			if sid, ok := idOfLink("https://app.devin.ai/sessions/" + p[1]); ok {
				return agent.SessionID(sid), true
			}
		}
	}
	if mm := devinWord.FindStringSubmatch(out); mm != nil {
		return agent.SessionID(mm[1]), true
	}
	return "", false
}
