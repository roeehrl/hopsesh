package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Handing a session to Claude Code's cloud, and sending one a follow-up. Claude Code
// cannot push a terminal session to the cloud, so a hand-off starts a new cloud session
// with the briefing hopsesh wrote as its first prompt: `claude --cloud` clones "your
// current branch" (the core runs it in a worktree on the handoff branch), or, with
// CCR_FORCE_BUNDLE=1, uploads the repository as a git bundle and pushes nothing.

var (
	_ agent.CloudSender   = (*Module)(nil)
	_ agent.CloudFollower = (*Module)(nil)
)

// sendTimeout bounds one cloud verb: creating a session uploads a bundle at worst.
const sendTimeout = 5 * time.Minute

// cloudReply is what `claude -p … --cloud … --output-format json` prints. The docs give
// {ok, session_id, url} for a follow-up; that a new session prints the same is unverified.
type cloudReply struct {
	OK        *bool  `json:"ok"`
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	Error     string `json:"error"`
}

// sessionLink finds a session's link in text output (the reply when it is not JSON).
var sessionLink = regexp.MustCompile(`https://claude\.ai/code/((?:session|cse)_[A-Za-z0-9]{6,64})`)

// SendCloud starts a cloud session with the briefing as its first prompt:
// `claude -p <brief> --cloud --output-format json` in r.Dir, whose current branch is the
// handoff branch (claude clones the remote's copy of it). With r.Code ViaBundle it sets
// CCR_FORCE_BUNDLE=1, and Claude Code uploads the repository instead. It checks the login
// first; the driver runs without CLAUDE_CODE_CHILD_SESSION and ANTHROPIC_API_KEY.
//
// Unverified: that -p with a bare --cloud starts a new session from the prompt and prints
// the follow-up's JSON shape. The docs show `claude --cloud "<task>"` for a new session and
// `claude -p "<msg>" --cloud <id> --output-format json` for a follow-up; local help reads
// "--cloud [description|session_id|url]  Create a cloud session with the given
// description, or attach to an existing one". A reply that is not JSON is read for the
// session's link.
func (m *Module) SendCloud(ctx context.Context, h agent.Host, _ agent.Install, r agent.SendRequest) (agent.CloudSession, error) {
	if _, err := login(ctx, h); err != nil {
		return agent.CloudSession{}, err
	}
	if !strings.HasPrefix(r.Brief, agent.NotePrefix) {
		return agent.CloudSession{}, fmt.Errorf("the briefing must start with %q", agent.NotePrefix)
	}
	o := agent.RunOptions{Dir: r.Dir, Unset: cloud().Unset, Timeout: sendTimeout}
	if r.Code == agent.ViaBundle {
		o.Env = append(o.Env, "CCR_FORCE_BUNDLE=1")
	}
	res, err := h.Exec().Run(ctx, []string{"claude", "-p", r.Brief, "--cloud", "--output-format", "json"}, o)
	if err != nil {
		return agent.CloudSession{}, err
	}
	cs, err := m.reply(res, "")
	if err != nil {
		return cs, err
	}
	cs.Title, cs.Repo, cs.Branch, cs.Base = r.Title, r.Repo, r.Branch, r.Base
	if r.Code == agent.ViaBundle {
		cs.Branch = "" // uploaded: the cloud has no branch of it on the remote
	}
	return cs, nil
}

// FollowUp queues one message in a cloud session and returns at once:
// `claude -p <text> --cloud <id> --output-format json` (documented, with that reply).
// The message starts a model turn there, on the user's plan.
func (m *Module) FollowUp(ctx context.Context, h agent.Host, _ agent.Install, sid agent.SessionID, text string) (agent.CloudSession, error) {
	cid := canonical(string(sid))
	if cid == "" {
		return agent.CloudSession{}, fmt.Errorf("%w: %q is not a Claude Code cloud session id", agent.ErrNotFound, sid)
	}
	if strings.TrimSpace(text) == "" {
		return agent.CloudSession{}, fmt.Errorf("a follow-up needs some text")
	}
	if _, err := login(ctx, h); err != nil {
		return agent.CloudSession{}, err
	}
	res, err := h.Exec().Run(ctx, []string{"claude", "-p", text, "--cloud", cid, "--output-format", "json"},
		agent.RunOptions{Unset: cloud().Unset, Timeout: sendTimeout})
	if err != nil {
		return agent.CloudSession{}, err
	}
	return m.reply(res, cid)
}

// reply reads the driver's answer, or its refusal mapped onto the cloud sentinels.
func (m *Module) reply(res agent.Result, want string) (agent.CloudSession, error) {
	out := strings.TrimSpace(string(res.Stdout))
	var rp cloudReply
	jsonErr := json.Unmarshal(lastJSON(out), &rp)
	if res.Code != 0 || jsonErr == nil && rp.OK != nil && !*rp.OK {
		msg := strings.TrimSpace(string(res.Stderr))
		if msg == "" {
			msg = nonEmptyStr(rp.Error, out)
		}
		return agent.CloudSession{}, refused(msg)
	}
	sid, url := "", ""
	if jsonErr == nil {
		sid, url = canonical(rp.SessionID), rp.URL
	}
	if sid == "" {
		if mm := sessionLink.FindStringSubmatch(out); mm != nil {
			sid, url = canonical(mm[1]), mm[0]
		}
	}
	if sid == "" {
		sid = want
	}
	if sid == "" {
		return agent.CloudSession{}, &agent.FormatError{Path: "claude --cloud", Err: fmt.Errorf("no session id in its reply: %q", firstLine(out))}
	}
	if url == "" {
		url = m.CloudURL(cloudName, agent.SessionID(sid))
	}
	return agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(sid)}, Cloud: cloudName, URL: url,
		State: agent.CloudRunning, Updated: time.Now().UTC()}, nil
}

// refused maps a refusal's words onto the cloud sentinels. The API-key wording is the
// docs' (teleport); the others are unverified guesses at the CLI's.
func refused(msg string) error {
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "organization uuid"), strings.Contains(l, "/login"), strings.Contains(l, "not logged in"), strings.Contains(l, "api key"):
		return fmt.Errorf("%w: Claude Code here uses an API key or another provider. Cloud sessions need a claude.ai login (%s)", agent.ErrSignedOut, firstLine(msg))
	case strings.Contains(l, "not available for your"), strings.Contains(l, "not enabled for your"), strings.Contains(l, "allow_remote_sessions"),
		strings.Contains(l, "organization turned off"), strings.Contains(l, "your plan"):
		return fmt.Errorf("%w: %s", agent.ErrNotEligible, firstLine(msg))
	case strings.Contains(l, "can't send this repository"), strings.Contains(l, "cannot send this repository"), strings.Contains(l, "submodule"),
		strings.Contains(l, "bundle is too large"), strings.Contains(l, "not supported for this repository"):
		return fmt.Errorf("%w: %s", agent.ErrRepoUnsupported, firstLine(msg))
	case strings.Contains(l, "archived"):
		return fmt.Errorf("this session is archived and can't take new messages (%s)", firstLine(msg))
	}
	return fmt.Errorf("claude refused: %s", firstLine(msg))
}

// lastJSON is the last line of output that looks like a JSON object (anything printed
// before it is ignored).
func lastJSON(out string) []byte {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "{") {
			return []byte(l)
		}
	}
	return []byte(out)
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

func nonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
