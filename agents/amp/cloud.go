package amp

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// cloudName is Amp as a location.
const cloudName = "amp"

// cloud declares Amp's threads as data. Down is the thread as text (amp threads
// markdown, which includes the tool calls as text; its layout is unverified), which the
// core writes into a local agent. The code an orb wrote comes home through `amp sync
// <thread>`, which mirrors the orb into a checkout live and keeps running, so hopsesh does
// not run it yet: no code comes down. Up is a briefing through `amp -ox` into a new orb
// thread on the repository's project; which branch the orb clones is not documented, so the
// briefing asks for the handoff branch (BriefBranch).
func cloud() agent.Cloud {
	return agent.Cloud{
		Name:   cloudName,
		Title:  "Amp",
		Driver: "amp",
		// The npm release current when the module was written. Nothing ran against a real
		// Amp: the commands come from Amp's manual and a community SDK that wraps the CLI,
		// and the tests run the stand-in amp.
		Tested: []string{"0.0.1791107882"},
		Hosts:  []string{"github.com"},
		Up:     agent.FidBrief,
		Down:   agent.FidText,
		CodeUp: []agent.CodeWay{agent.ViaBranch},
		Needs:  []agent.Need{agent.NeedGitHub, agent.NeedPushedBranch},
		Limits: []string{
			"An orb's branch can't be chosen from the amp CLI: the briefing asks Amp to check out the handoff branch first",
			"The code an orb writes stays there: hopsesh does not run `amp sync`, which mirrors it live into a checkout",
		},
		BriefBranch: true,
		Watch: agent.Watch{
			Surface: "orb threads started with `amp -ox <prompt> --project <owner/repo> --title <title>` (the thread link it prints), threads through `amp threads list` (a table: title, last updated, visibility, messages, thread id) and `amp threads markdown <id>`, thread links (ampcode.com/threads/T-…), and orbs (`amp -ox`, `amp sync <thread>`)",
			Docs:    []string{"https://ampcode.com/manual.md", "https://ampcode.com/manual/orbs.md", "https://ampcode.com/manual/sdk.md", "https://ampcode.com/docs/cli/spawning-orbs"},
			Feeds:   []agent.Feed{{Kind: agent.FeedRSS, URL: "https://ampcode.com/news.rss"}},
			Grep:    `thread|orb|-ox|orb-execute|--project|sync|markdown|export|sdk|cloud|handoff|deprecat`,
			Help:    [][]string{{"amp", "--help"}, {"amp", "threads", "--help"}},
			Relies:  []string{"threads", "list", "markdown", "sync", "-x", "--orb-execute", "--project", "--title"},
		},
	}
}

var (
	_ agent.CloudLister  = (*Module)(nil)
	_ agent.CloudFetcher = (*Module)(nil)
	_ agent.CloudTester  = (*Module)(nil)
	_ agent.CloudLinker  = (*Module)(nil)
)

var (
	// Unverified: how the CLI words a missing login, a workspace without credits, and an
	// unknown thread.
	signedOutWords   = regexp.MustCompile(`(?i)not logged in|amp login|log ?in first|unauthori[sz]ed|\b401\b|invalid api key`)
	notEligibleWords = regexp.MustCompile(`(?i)credits|\b402\b|payment|not enabled|not available|\b403\b|forbidden`)
	notFoundWords    = regexp.MustCompile(`(?i)not found|\b404\b|no such thread`)
	threadID         = regexp.MustCompile(`^T-[A-Za-z0-9][A-Za-z0-9-]{3,}$`)
)

// run runs amp and maps a refusal onto the SDK's errors. The CLI holds the login;
// hopsesh never reads it.
func run(ctx context.Context, h agent.Host, args ...string) ([]byte, error) {
	r, err := h.Exec().Run(ctx, append([]string{"amp"}, args...), agent.RunOptions{Timeout: 30 * time.Second, Stdin: []byte{}})
	if err != nil {
		return nil, err
	}
	return mapped(r, strings.Join(args[:min(2, len(args))], " "))
}

// mapped is an amp run's output, or its refusal as one of the SDK's errors.
func mapped(r agent.Result, what string) ([]byte, error) {
	if r.Code != 0 {
		text := string(r.Stderr) + "\n" + string(r.Stdout)
		msg := firstLine(text)
		switch {
		case signedOutWords.MatchString(text):
			return nil, fmt.Errorf("%w: the amp CLI here is not logged in (amp login)", agent.ErrSignedOut)
		case notEligibleWords.MatchString(text):
			return nil, fmt.Errorf("%w: Amp refused: %s", agent.ErrNotEligible, msg)
		case notFoundWords.MatchString(text):
			return nil, fmt.Errorf("%w: %s", agent.ErrNotFound, msg)
		}
		return nil, fmt.Errorf("amp %s: exit %d: %s", what, r.Code, msg)
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

// ListCloud lists the threads (amp threads list). The CLI prints a table; its layout is
// not documented (a community SDK reads title, last updated, visibility, messages and
// thread id, split by runs of spaces), so each row is read by its cells' shapes: the
// T-… cell is the id, a number the message count, the first cell the title. Threads
// carry no repository or state in the list.
func (m *Module) ListCloud(ctx context.Context, h agent.Host, _ agent.Install, q agent.CloudQuery) (agent.CloudListing, error) {
	out, err := run(ctx, h, "threads", "list")
	if err != nil {
		return agent.CloudListing{}, err
	}
	var l agent.CloudListing
	if q.Repo != "" {
		return l, nil // the list does not say which repository a thread works on
	}
	rows, errs := parseThreads(string(out), time.Now())
	l.Errors = errs
	for _, r := range rows {
		l.Sessions = append(l.Sessions, agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(r.id)}, Cloud: cloudName,
			URL: m.CloudURL(cloudName, agent.SessionID(r.id)), Title: r.title, State: agent.CloudUnknown, Updated: r.updated})
		if q.Limit > 0 && len(l.Sessions) == q.Limit {
			break
		}
	}
	return l, nil
}

type thread struct {
	id, title string
	messages  int
	updated   time.Time
}

var (
	cellSplit = regexp.MustCompile(`\s{2,}|\t+|\s*│\s*`)
	rule      = regexp.MustCompile(`^[\s\-─━═=+|┼┬┴]+$`)
	agoCell   = regexp.MustCompile(`(?i)^(\d+)\s*(s|sec|secs|seconds?|m|min|mins|minutes?|h|hr|hrs|hours?|d|days?|w|weeks?|mo|months?)\s+ago$`)
)

// parseThreads reads amp threads list's table (unverified layout).
func parseThreads(out string, now time.Time) ([]thread, []agent.SessionError) {
	var rows []thread
	var errs []agent.SessionError
	for n, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.Trim(strings.TrimSpace(line), "│|")
		low := strings.ToLower(line)
		if line == "" || rule.MatchString(line) || strings.HasPrefix(low, "no records") || strings.HasPrefix(low, "no threads") ||
			strings.Contains(low, "thread id") {
			continue
		}
		var t thread
		var rest []string
		for _, c := range cellSplit.Split(strings.TrimSpace(line), -1) {
			c = strings.TrimSpace(c)
			switch k, err := strconv.Atoi(c); {
			case c == "":
			case t.id == "" && threadID.MatchString(c):
				t.id = c
			case err == nil && t.messages == 0:
				t.messages = k
			case t.updated.IsZero() && !when(c, now).IsZero():
				t.updated = when(c, now)
			default:
				rest = append(rest, c)
			}
		}
		if t.id == "" {
			errs = append(errs, agent.SessionError{Path: fmt.Sprintf("amp threads list line %d", n+1), Err: fmt.Errorf("no thread id in %q", line)})
			continue
		}
		if len(rest) > 0 {
			t.title = rest[0] // then the visibility
		}
		rows = append(rows, t)
	}
	return rows, errs
}

// when reads a "Last Updated" cell: "5m ago", "2 hours ago", "just now", "yesterday", or a
// date. Zero when it is none of those.
func when(s string, now time.Time) time.Time {
	switch strings.ToLower(s) {
	case "just now", "now":
		return now
	case "yesterday":
		return now.Add(-24 * time.Hour)
	}
	if m := agoCell.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		u := strings.ToLower(m[2])
		unit := map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[u[0]]
		if strings.HasPrefix(u, "mo") {
			unit = 30 * 24 * time.Hour
		}
		return now.Add(-time.Duration(n) * unit)
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02", "Jan 2, 2006", "Jan 2"} {
		if t, err := time.Parse(layout, s); err == nil {
			if t.Year() == 0 {
				t = t.AddDate(now.Year(), 0, 0)
			}
			return t
		}
	}
	return time.Time{}
}

// maxText bounds the thread hopsesh keeps.
const maxText = 8 << 20

// FetchCloud reads a thread as Markdown (amp threads markdown <id>) as the conversation in
// text. The orb's code stays in the orb.
func (m *Module) FetchCloud(ctx context.Context, h agent.Host, _ agent.Install, sid agent.SessionID, _ agent.FetchTarget) (agent.Fetched, error) {
	if !threadID.MatchString(string(sid)) {
		return agent.Fetched{}, fmt.Errorf("%w: %q is not an Amp thread id", agent.ErrNotFound, sid)
	}
	out, err := run(ctx, h, "threads", "markdown", string(sid))
	if err != nil {
		return agent.Fetched{}, err
	}
	return agent.Fetched{
		Segment: markdownSegment(string(sid), out),
		Loss: []string{
			"tool calls and their results come as the Markdown's text, not as tool calls",
			"the code the thread changed in an orb stays there: hopsesh does not run `amp sync`, which mirrors it live into a checkout",
		},
	}, nil
}

// turnHeading is a Markdown heading that starts a turn (unverified layout): "## User",
// "### Assistant", "## Amp", optionally followed by more words.
var turnHeading = regexp.MustCompile(`(?i)^#{1,4}\s+(user|human|you|assistant|amp|agent)\b`)

// markdownSegment is a thread's Markdown as the conversation: a "# Title" first, then
// turns under user and assistant headings; Markdown without such headings is one message
// from the agent.
func markdownSegment(sid string, b []byte) *ir.Segment {
	if len(b) > maxText {
		b = b[:maxText]
	}
	seg := &ir.Segment{Header: ir.Header{Agent: string(id), SessionID: sid}}
	var cur *ir.Node
	var loose []string
	flush := func() {
		if cur != nil {
			cur.Text = strings.TrimSpace(cur.Text)
			if cur.Text != "" {
				seg.Nodes = append(seg.Nodes, *cur)
			}
			cur = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if m := turnHeading.FindStringSubmatch(line); m != nil {
			flush()
			actor := ir.Agent
			switch strings.ToLower(m[1]) {
			case "user", "human", "you":
				actor = ir.User
			}
			cur = &ir.Node{Kind: ir.KindMessage, Actor: actor}
			continue
		}
		if t, ok := strings.CutPrefix(line, "# "); ok && seg.Header.Title == "" && cur == nil && len(seg.Nodes) == 0 {
			seg.Header.Title = strings.TrimSpace(t)
			continue
		}
		if cur != nil {
			cur.Text += line + "\n"
		} else {
			loose = append(loose, line)
		}
	}
	flush()
	if len(seg.Nodes) == 0 {
		if text := strings.TrimSpace(strings.Join(loose, "\n")); text != "" {
			seg.Nodes = append(seg.Nodes, ir.Node{Kind: ir.KindMessage, Actor: ir.Agent, Text: text})
		}
	}
	ir.Chain(seg.Nodes, "")
	return seg
}

// ParseCloudLink reads a thread's link (https://ampcode.com/threads/T-…) or its id.
func (m *Module) ParseCloudLink(s string) (string, agent.SessionID, bool) {
	s = strings.TrimSpace(s)
	if u, ok := agent.LinkOn(s, "ampcode.com"); ok {
		// https://ampcode.com/threads/<id>[.md][/…][?…]
		s = ""
		if p := agent.PathParts(u); len(p) >= 2 && p[0] == "threads" {
			s = strings.TrimSuffix(p[1], ".md")
		}
	}
	if !threadID.MatchString(s) {
		return cloudName, "", false
	}
	return cloudName, agent.SessionID(s), true
}

// CloudURL is the thread's page on ampcode.com.
func (m *Module) CloudURL(_ string, sid agent.SessionID) string {
	return "https://ampcode.com/threads/" + string(sid)
}

// TestCloud lists the threads once (read-only; it needs the login), and checks that the
// CLI still has the commands the module uses. Amp documents no status command. It starts
// nothing.
func (m *Module) TestCloud(ctx context.Context, h agent.Host, _ agent.Install, _ string) (agent.CloudTest, error) {
	var t agent.CloudTest
	out, err := run(ctx, h, "threads", "list")
	if err != nil {
		msg := err.Error()
		for _, e := range []error{agent.ErrSignedOut, agent.ErrNotEligible} {
			msg = strings.TrimPrefix(msg, e.Error()+": ")
		}
		t.Checks = append(t.Checks, agent.CloudCheck{Text: msg})
		return t, err
	}
	rows, _ := parseThreads(string(out), time.Now())
	t.Account = "Amp (amp login)"
	t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: fmt.Sprintf("logged in; amp threads list shows %d threads", len(rows))})
	r, err := h.Exec().Run(ctx, []string{"amp", "threads", "--help"}, agent.RunOptions{Timeout: 20 * time.Second, Stdin: []byte{}})
	if err != nil {
		return t, err
	}
	help := string(r.Stdout) + string(r.Stderr)
	for _, w := range []string{"list", "markdown"} {
		if strings.Contains(help, w) {
			t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: "amp threads " + w + " found"})
		} else {
			t.Checks = append(t.Checks, agent.CloudCheck{Text: "`amp threads --help` no longer lists " + w})
		}
	}
	return t, nil
}
