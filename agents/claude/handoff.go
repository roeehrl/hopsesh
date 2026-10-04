package claude

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Handing a session to Claude Code's cloud. Claude Code cannot push a terminal session to
// the cloud, so a hand-off starts a new cloud session with the briefing hopsesh wrote as
// its first prompt: `claude --cloud "<briefing>"`, which clones "your current branch" (the
// core runs it in its hand-off folder, on the handoff branch), or, with CCR_FORCE_BUNDLE=1,
// uploads the repository as a git bundle and pushes nothing.
//
// Claude Code 2.1.28x starts a cloud session only from a terminal (live probes,
// 2026-10-04): `claude -p … --cloud` exits with "Error: --cloud cannot be combined with
// --print.", and without a TTY `claude --cloud` exits with "Error: --cloud requires an
// interactive terminal. …". In a terminal, it first asks whether the folder is trusted
// (in a folder it has not seen: "Quick safety check: Is this a project you created or one
// you trust?"), then prints
//
//	Created cloud session: Session ready
//	View: https://claude.ai/code/session_01…?from=cli&m=0
//	Resume with: claude --teleport session_01…
//
// and exits. So SendCloud returns that command as a terminal step for the user, and
// ReadStep reads the session from what it printed. The user answers the trust question;
// hopsesh never answers it, never writes Claude Code's settings to skip it, and never
// types into Claude Code.
//
// No follow-up: there is no non-interactive form (the -p one is refused), and attaching
// to a running session (`claude --cloud <id>`) is "not enabled" on accounts per the docs
// (anthropics/claude-code#97813 asks for it), so the cloud declares NoFollowUp.

var _ agent.CloudSender = (*Module)(nil)
var _ agent.CloudStepReader = (*Module)(nil)

// What Claude Code prints around `claude --cloud`, as 2.1.284 and 2.1.289 word it. They
// are also the drift check's canaries (Cloud.Watch.Grep).
const (
	refusedPrint = "--cloud cannot be combined with --print"
	refusedNoTTY = "--cloud requires an interactive terminal"
	trustAsked   = "Quick safety check"
)

// noFollowUp is said where a follow-up would be.
const noFollowUp = "hopsesh can't send a Claude Code cloud session a message: Claude Code 2.1 has no command that does it outside its own terminal session. Open the session on claude.ai to write to it."

// SendCloud checks the login, then returns the command that starts the cloud session with
// the briefing as its first prompt: `claude --cloud <brief>` in r.Dir, whose current
// branch is the handoff branch (claude clones the remote's copy of it). With r.Code
// ViaBundle it sets CCR_FORCE_BUNDLE=1, and Claude Code uploads the repository instead.
// The command runs without CLAUDE_CODE_CHILD_SESSION and ANTHROPIC_API_KEY.
func (m *Module) SendCloud(ctx context.Context, h agent.Host, _ agent.Install, r agent.SendRequest) (agent.Sent, error) {
	if _, err := login(ctx, h); err != nil {
		return agent.Sent{}, err
	}
	if !strings.HasPrefix(r.Brief, agent.NotePrefix) {
		return agent.Sent{}, fmt.Errorf("the briefing must start with %q", agent.NotePrefix)
	}
	if r.Dir == "" {
		return agent.Sent{}, fmt.Errorf("claude --cloud runs in a checkout of the repository; none was given")
	}
	c := &agent.Command{Argv: []string{"claude", "--cloud", r.Brief}, Dir: r.Dir, Unset: cloud().Unset}
	if r.Code == agent.ViaBundle {
		c.Env = append(c.Env, "CCR_FORCE_BUNDLE=1")
	}
	return agent.Sent{Run: c}, nil
}

// ReadStep reads the cloud session `claude --cloud` started from what it printed: the
// session's link (View: …), checked against the teleport command it suggests (Resume
// with: …), or its refusal. A line as wide as the terminal may go on in the next (Claude
// Code breaks long lines), so the link is read across such a break.
func (m *Module) ReadStep(cl string, out agent.StepOutput) (agent.CloudSession, error) {
	if cl != cloudName {
		return agent.CloudSession{}, fmt.Errorf("%w: claude reaches %s, not %s", agent.ErrUnsupported, cloudName, cl)
	}
	sid, refusal, trust := readCloudOutput(out.Text, out.Width)
	if sid != "" {
		return agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(sid)}, Cloud: cloudName,
			URL: m.CloudURL(cloudName, agent.SessionID(sid)), State: agent.CloudRunning, Updated: time.Now().UTC()}, nil
	}
	if refusal != "" {
		return agent.CloudSession{}, refused(refusal)
	}
	switch {
	case out.Code == 130:
		return agent.CloudSession{}, fmt.Errorf("%w: Claude Code was stopped before it started a cloud session", agent.ErrNoSession)
	case trust:
		return agent.CloudSession{}, fmt.Errorf("%w: Claude Code asked whether you trust hopsesh's hand-off folder and ended without starting a cloud session (it starts one only after a yes)", agent.ErrNoSession)
	case out.Code > 0:
		return agent.CloudSession{}, fmt.Errorf("%w: Claude Code ended (exit status %d) without printing a session link; what it said is in the terminal", agent.ErrNoSession, out.Code)
	}
	return agent.CloudSession{}, fmt.Errorf("%w: Claude Code ended without printing a session link", agent.ErrNoSession)
}

// readCloudOutput finds the session id `claude --cloud` printed, else the line of its
// refusal; trust says whether it asked about the folder.
func readCloudOutput(text string, width int) (sid, refusal string, trust bool) {
	lines := unwrap(strings.Split(text, "\n"), width)
	view, resume := "", ""
	for _, l := range lines {
		if strings.Contains(l, trustAsked) {
			trust = true
		}
		for _, u := range linksIn(l, "claude.ai") {
			if id := sessionOf(u); id != "" {
				view = id
			}
		}
		if id := teleportID(l); id != "" {
			resume = id
		}
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "Error:") || strings.Contains(t, refusedNoTTY) || strings.Contains(t, refusedPrint) {
			refusal = t
		}
	}
	switch {
	case view == "":
		sid = resume
	case resume == "", view == resume, strings.HasPrefix(view, resume):
		sid = view
	case strings.HasPrefix(resume, view):
		sid = resume // the link was cut where a line broke
	default:
		sid = view
	}
	return sid, refusal, trust
}

// teleportID is the session in a "claude --teleport <id>" command on the line ("" for
// none): the word after a --teleport word.
func teleportID(line string) string {
	f := strings.Fields(line)
	id := ""
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "--teleport" {
			id = canonical(strings.TrimRight(f[i+1], ".,;:)]}>\"'"))
		}
	}
	return id
}

// unwrap joins a line exactly as wide as the terminal with the next one: where a program
// broke a long line, the text goes on there.
func unwrap(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	var out []string
	cur, joining := "", false
	for _, l := range lines {
		if joining {
			cur += l
		} else {
			cur = l
		}
		joining = utf8.RuneCountInString(strings.TrimRight(l, " ")) >= width
		if !joining {
			out = append(out, cur)
		}
	}
	if joining {
		out = append(out, cur)
	}
	return out
}

// refused maps a refusal's words onto the cloud sentinels. The API-key wording is the
// docs' (teleport) and the terminal ones were seen (2.1.284); the others are unverified
// guesses at the CLI's.
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
	case strings.Contains(l, strings.ToLower(refusedNoTTY)):
		return fmt.Errorf("claude found no terminal to start the cloud session in (%s)", firstLine(msg))
	}
	return fmt.Errorf("claude refused: %s", firstLine(msg))
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
