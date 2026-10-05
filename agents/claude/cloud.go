package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// cloudName is Claude Code's cloud (Claude Code on the web) as a location.
const cloudName = "claude-cloud"

// cloud declares Claude Code's cloud sessions (Claude Code on the web) as data: the
// module reaches them through the claude binary only (--cloud "<briefing>" in the user's
// terminal to start one, --teleport <id> to bring one here), never through Anthropic's web
// backend.
func cloud() agent.Cloud {
	return agent.Cloud{
		Name:   cloudName,
		Title:  "Claude Code cloud",
		Driver: "claude",
		// The cloud flags were read from 2.1.284's and 2.1.289's help, and --cloud's
		// terminal behaviour seen in 2.1.284; they run against the stand-in cloud in the
		// tests, and scripts/cloud-smoke.sh checks a real hand-off and teleport by hand.
		Tested: []string{"2.1"},
		Hosts:  []string{"github.com"},
		// Up is always a briefing: Claude Code cannot push a terminal session to the cloud.
		// Down is the native conversation through --teleport.
		Up:       agent.FidBrief,
		Down:     agent.FidNative,
		CodeUp:   []agent.CodeWay{agent.ViaBranch, agent.ViaBundle},
		CodeDown: []agent.CodeWay{agent.ViaBranch},
		Needs: []agent.Need{agent.NeedSubscriptionLogin, agent.NeedGitHub, agent.NeedPushedBranch, agent.NeedCleanTree,
			agent.NeedSameAccount, agent.NeedTerminal},
		// The cloud's own branches (claude/web-session-…, seen in #94836; self-hosted
		// guidance restricts pushes to claude/*).
		VendorPrefix: "claude/",
		// Teleport restores only the first turn of some sessions, and nothing of Remote
		// Control sessions since about 2026-09-12; the count check tells.
		Problems: map[string]string{
			"partial": "https://github.com/anthropics/claude-code/issues/94836",
			"empty":   "https://github.com/anthropics/claude-code/issues/95873",
		},
		// Run from inside another Claude Code session, teleport inherits its marker and saves
		// no transcript (anthropics/claude-code#93892); an API key in the environment takes
		// the place of the claude.ai login teleport needs.
		Unset:      []string{"CLAUDE_CODE_CHILD_SESSION", "ANTHROPIC_API_KEY"},
		NoFollowUp: noFollowUp,
		// Claude Code's own sign-in (it opens claude.ai in the browser); the app runs it in a
		// sign-in tab, which it never reads.
		SignIn: []string{"auth", "login"},
		Watch: agent.Watch{
			Surface: "cloud sessions started with `claude --cloud \"<briefing>\"` in the user's terminal (its workspace-trust question, then the `View: https://claude.ai/code/session_…` and `Resume with: claude --teleport session_…` lines hopsesh reads, and its refusals without a terminal or with --print), brought back with `claude --teleport <id>` (which saves its copy only after the user sends a message in it, marked by an isMeta \"continued from another machine\" record), Remote Control, cloud environments, and the transcript records a teleport or bridge leaves",
			Docs: append(docs("claude-code-on-the-web", "web-quickstart", "cloud-environments", "remote-control", "desktop",
				"sessions", "routines", "self-hosted-environments", "env-vars", "feature-availability", "data-usage", "legal-and-compliance"),
				"https://code.claude.com/docs/llms.txt"),
			Feeds: []agent.Feed{{Kind: agent.FeedMarkdown, URL: "https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md"}},
			// The canaries of the terminal step are in it: the lines ReadStep reads and the
			// refusals it maps.
			Grep: `teleport|--cloud|--remote|Remote Control|bridge|cloud session|cse_|self-hosted|Continue in|environment|sessions:|deprecat|` +
				`Created cloud session|View:|Resume with|interactive terminal|combined with --print|safety check|trust this folder|attach|` +
				`continued from another machine|teleported-from|resumed without branch`,
			// `claude remote-control --help` needs a claude.ai login, so it is not run.
			Help:   [][]string{{"claude", "--help"}},
			Relies: []string{"--cloud", "--teleport", "--environment", "--remote-control", "--session-id", "--fork-session", "--resume"},
			Issues: []string{
				"anthropics/claude-code#66373", // local → cloud handoff from the CLI
				"anthropics/claude-code#97813", // attach to a running cloud session
				"anthropics/claude-code#97446", // archive a cloud session from the CLI
				"anthropics/claude-code#93892", // Remote Control teleport
				"anthropics/claude-code#95873", // Remote Control teleport
				"anthropics/claude-code#94836", // partial teleport
				"anthropics/claude-code#92734", // web → desktop, prompt history
			},
			Searches: []string{"repo:anthropics/claude-code is:issue teleport", "repo:anthropics/claude-code is:issue \"cloud session\""},
		},
	}
}

// docs are Claude Code documentation pages as Markdown.
func docs(pages ...string) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = "https://code.claude.com/docs/en/" + p + ".md"
	}
	return out
}

var (
	_ agent.CloudLister  = (*Module)(nil)
	_ agent.CloudFetcher = (*Module)(nil)
	_ agent.CloudAdopter = (*Module)(nil)
	_ agent.CloudLinker  = (*Module)(nil)
	_ agent.CloudTester  = (*Module)(nil)
)

// sessionID is a cloud session's id in its session_ form. The docs give the id as
// session_…, cse_… ("Same id, different prefix") or its claude.ai/code/<id> link.
var sessionID = regexp.MustCompile(`^session_[A-Za-z0-9]{6,64}$`)

// canonical is an id in its session_ form ("" when it is not one).
func canonical(id string) string {
	if rest, ok := strings.CutPrefix(id, "cse_"); ok {
		id = "session_" + rest
	}
	if !sessionID.MatchString(id) {
		return ""
	}
	return id
}

// ParseCloudLink reads a claude.ai/code link, a session_… id or its cse_… form.
func (m *Module) ParseCloudLink(s string) (string, agent.SessionID, bool) {
	s = strings.TrimSpace(s)
	if u, ok := agent.LinkOn(s, "claude.ai"); ok {
		// https://claude.ai/code/<id>[/…][?…]
		p := agent.PathParts(u)
		if len(p) < 2 || p[0] != "code" {
			return cloudName, "", false
		}
		s = p[1]
	}
	id := canonical(s)
	return cloudName, agent.SessionID(id), id != ""
}

// CloudURL is the session's page on claude.ai.
func (m *Module) CloudURL(_ string, id agent.SessionID) string {
	return "https://claude.ai/code/" + string(id)
}

// login asks claude which login it uses (claude auth status --json; claude reads its own
// credentials, hopsesh never does) and refuses one the cloud cannot use: not logged in, an
// API key, another provider (Bedrock, Vertex, Foundry), or a plan without cloud sessions.
func login(ctx context.Context, h agent.Host) (authStatus, error) {
	r, err := h.Exec().Run(ctx, []string{"claude", "auth", "status", "--json"}, agent.RunOptions{Timeout: 20 * time.Second})
	if err != nil {
		return authStatus{}, err
	}
	var a authStatus
	if err := json.Unmarshal(r.Stdout, &a); err != nil {
		if !strings.Contains(string(r.Stdout)+string(r.Stderr), "{") && r.Code != 0 {
			// Not logged in at all: some versions print a line, not JSON.
			return a, fmt.Errorf("%w: Claude Code here is not logged in", agent.ErrSignedOut)
		}
		return a, &agent.FormatError{Path: "claude auth status", Err: err}
	}
	switch {
	case !a.LoggedIn:
		return a, fmt.Errorf("%w: Claude Code here is not logged in", agent.ErrSignedOut)
	case a.Provider != "" && a.Provider != "firstParty" || a.Method != "" && !strings.EqualFold(a.Method, "claude.ai"):
		return a, fmt.Errorf("%w: Claude Code here uses an API key or another provider. Cloud sessions need a claude.ai login", agent.ErrSignedOut)
	case strings.EqualFold(a.Subscription, "free"):
		// Cloud sessions need a Pro, Max, Team or Enterprise seat (the docs); the plan name
		// "free" is an assumption about auth status's wording.
		return a, fmt.Errorf("%w: your Claude plan doesn't include cloud sessions", agent.ErrNotEligible)
	}
	return a, nil
}

// loginLabel is a login for people: "claude.ai · max".
func loginLabel(a authStatus) string {
	if a.Subscription == "" {
		return "claude.ai"
	}
	return "claude.ai · " + a.Subscription
}

// ListCloud lists what hopsesh can know of Claude Code's cloud without a list command
// (Claude Code has none; `claude --teleport` without an id is an interactive picker): the
// sessions hopsesh recorded or was given (q.Known), and the local sessions Remote Control
// mirrors on claude.ai, as Mirror entries. It first checks the login the cloud needs. The
// listing is always Partial, and it cannot tell a session's state, so that is unknown.
func (m *Module) ListCloud(ctx context.Context, h agent.Host, in agent.Install, q agent.CloudQuery) (agent.CloudListing, error) {
	if _, err := login(ctx, h); err != nil {
		return agent.CloudListing{}, err
	}
	l := agent.CloudListing{Partial: true}
	at := map[agent.SessionID]int{}
	for _, k := range q.Known {
		cid := agent.SessionID(canonical(string(k)))
		if cid == "" {
			l.Errors = append(l.Errors, agent.SessionError{Path: "known/" + string(k), Err: errors.New("not a Claude Code cloud session id")})
			continue
		}
		if _, dup := at[cid]; dup {
			continue
		}
		at[cid] = len(l.Sessions)
		l.Sessions = append(l.Sessions, agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: cid}, Cloud: cloudName,
			URL: m.CloudURL(cloudName, cid), State: agent.CloudUnknown})
	}
	local := q.Local
	if local == nil {
		ls, err := m.List(ctx, h, in)
		if err != nil {
			return l, err
		}
		local = ls.Sessions
	}
	for _, s := range local {
		mr := s.Mirror
		if mr == nil || mr.Cloud != cloudName || mr.ID == "" {
			continue
		}
		cs := agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: mr.ID}, Cloud: cloudName, URL: mr.URL, Title: s.Title,
			Branch: s.GitBranch, State: agent.CloudUnknown, Updated: s.LastActivity, Mirror: true, Local: s.Key.Session, Account: mr.Account}
		if i, dup := at[mr.ID]; dup {
			l.Sessions[i] = cs
			continue
		}
		at[mr.ID] = len(l.Sessions)
		l.Sessions = append(l.Sessions, cs)
	}
	return l, nil
}

// FetchCloud brings a cloud session into the worktree the core made (t.Dir) with
// `claude --teleport <id>`, which needs the user's terminal: it checks the login first,
// then returns the command and where Claude Code will write its copy of the conversation
// (the project folder of the worktree, under the config folder). An empty id leaves the
// choice to Claude Code's own picker. Teleport fetches and checks out the session's
// branch itself.
func (m *Module) FetchCloud(ctx context.Context, h agent.Host, in agent.Install, sid agent.SessionID, t agent.FetchTarget) (agent.Fetched, error) {
	if _, err := login(ctx, h); err != nil {
		return agent.Fetched{}, err
	}
	argv := []string{"claude", "--teleport"}
	cid := ""
	if sid != "" {
		if cid = canonical(string(sid)); cid == "" {
			return agent.Fetched{}, fmt.Errorf("%w: %q is not a Claude Code cloud session id", agent.ErrNotFound, sid)
		}
		argv = append(argv, cid)
	}
	dir := t.Dir
	if real, err := h.FS().RealPath(dir); err == nil {
		dir = real
	}
	return agent.Fetched{
		Code:  agent.CodeResult{Way: agent.ViaBranch},
		Run:   &agent.Command{Argv: argv, Dir: t.Dir, Unset: cloud().Unset},
		Adopt: &agent.Adopt{Root: home, Dir: "projects/" + Slug(dir), Since: time.Now(), Session: agent.SessionID(cid)},
		Loss: []string{
			"changes the cloud session did not commit and push stay in the cloud",
			"prompts typed on the web don't join this machine's prompt history",
		},
		Note: CopyNote,
	}, nil
}

// teleportRecord is a record of a teleported copy, as far as adopting it needs. Claude Code
// 2.1.289 writes no teleported-from record: the copy is a new session whose records carry
// this machine's folder, with an isMeta user record "This session is being continued from
// another machine…" after the cloud's conversation (seen on a real teleport, 2026-10-04).
// A teleported-from record with the cloud session's id and its messageCount is still read
// where one appears (seen in anthropics/claude-code#95873, on another surface).
type teleportRecord struct {
	Type         string `json:"type"`
	Remote       string `json:"remoteSessionId"`
	MessageCount *int   `json:"messageCount"`
	UUID         string `json:"uuid"`
	IsMeta       bool   `json:"isMeta"`
	IsSidechain  bool   `json:"isSidechain"`
	Message      *struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// continuedMarker starts the isMeta user record a teleported copy holds where the cloud's
// conversation ends and this machine's begins.
const continuedMarker = "This session is being continued from another machine"

// CopyNote is what the user must know at the teleport: Claude Code writes the copy only
// once a message is sent in it.
const CopyNote = "Claude Code saves its copy only after you send a message in it: send one (even \"ok\"), and hopsesh picks the copy up"

// maxAdoptScan bounds how much of a candidate transcript Adopted reads.
const maxAdoptScan = 256 << 20

// teleported is what a candidate transcript says about itself.
type teleported struct {
	file    string
	mod     time.Time
	remote  string // the teleported-from record's session ("" : none)
	count   *int   // its messageCount
	marked  bool   // it holds the "continued from another machine" record
	msgs    int    // the cloud's messages: those before the marker (all of them without one)
	replies int    // assistant messages among them
	first   string // the first user message's text
}

// Adopted finds the copy a teleport wrote in the worktree's project folder: a transcript
// written after a.Since that holds the "continued from another machine" record, or a
// teleported-from record naming the session (any such record when a.Session is empty: the
// picker chose). Failing both, the one transcript written there since (the worktree is
// hopsesh's own, so nothing else runs in it). It counts the cloud's messages (a reply
// split over several records counts once; the user's own new turn after the marker does
// not count), and reads the first user message, so the core can check the copy begins
// where the hand-off began. Only a teleported-from record states a count.
func (m *Module) Adopted(_ context.Context, h agent.Host, in agent.Install, a agent.Adopt) (agent.Adoption, error) {
	pa, fsys := h.Path(), h.FS()
	dir := in.Root(a.Root)
	for _, part := range strings.Split(a.Dir, "/") {
		dir = pa.Join(dir, part)
	}
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		if isNotExist(err) {
			return agent.Adoption{}, fmt.Errorf("%w: nothing in %s yet (%s)", agent.ErrNotFound, dir, CopyNote)
		}
		return agent.Adoption{}, err
	}
	want := canonical(string(a.Session))
	var stubbed, marked, loose *teleported
	nLoose := 0
	later := func(best, c *teleported) *teleported {
		if best == nil || c.mod.After(best.mod) {
			return c
		}
		return best
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".jsonl") || e.ModTime().Before(a.Since.Add(-2*time.Second)) {
			continue
		}
		c := &teleported{file: pa.Join(dir, n), mod: e.ModTime()}
		if err := scanTeleported(fsys, c); err != nil {
			continue
		}
		switch {
		case c.remote != "":
			if want == "" || canonical(c.remote) == want {
				stubbed = later(stubbed, c)
			}
		case c.marked:
			marked = later(marked, c)
		default:
			nLoose++
			loose = later(loose, c)
		}
	}
	best := stubbed
	if best == nil {
		best = marked
	}
	if best == nil && nLoose == 1 {
		best = loose
	}
	if best == nil {
		return agent.Adoption{}, fmt.Errorf("%w: no teleported copy in %s yet (%s)", agent.ErrNotFound, dir, CopyNote)
	}
	fi, err := fsys.Stat(best.file)
	if err != nil {
		return agent.Adoption{}, err
	}
	side := true
	s, err := summarize(fsys, pa, best.file, fi, &side)
	if err != nil {
		return agent.Adoption{}, err
	}
	out := agent.Adoption{Session: s.summary(), Remote: agent.SessionID(canonical(best.remote)), Restored: best.msgs, Replies: best.replies, First: best.first}
	if t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(best.first), agent.NotePrefix)); t != "" {
		// The cloud conversation's first prompt names it, not the turn the user sent here.
		out.Title = clip(oneLine(t))
	}
	if out.Remote == "" {
		out.Remote = agent.SessionID(want)
	}
	if best.count != nil {
		out.Expected, out.Stated = *best.count, true
	}
	return out, nil
}

// scanTeleported reads a transcript for what marks it a teleported copy, and counts the
// cloud's messages in it.
func scanTeleported(fsys agent.FS, c *teleported) error {
	f, err := fsys.Open(c.file)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	seen := map[string]bool{}
	var read int64
	firstSeen := false
	for sc.Scan() {
		line := sc.Bytes()
		if read += int64(len(line)) + 1; read > maxAdoptScan {
			break
		}
		var r teleportRecord
		if json.Unmarshal(line, &r) != nil {
			continue
		}
		switch r.Type {
		case "teleported-from":
			if c.remote == "" {
				c.remote, c.count = r.Remote, r.MessageCount
			}
		case "user", "assistant":
			if r.IsSidechain {
				continue
			}
			if r.IsMeta {
				if r.Type == "user" && strings.HasPrefix(strings.TrimSpace(recordText(r)), continuedMarker) {
					c.marked = true // what follows is this machine's, not the cloud's
				}
				continue
			}
			if c.marked {
				continue
			}
			if r.Type == "assistant" && r.Message != nil && r.Message.ID != "" {
				if seen[r.Message.ID] {
					continue
				}
				seen[r.Message.ID] = true
			}
			if r.Type == "user" && !firstSeen {
				if t := recordText(r); t != "" {
					c.first, firstSeen = t, true
				}
			}
			if r.Type == "assistant" {
				c.replies++
			}
			c.msgs++
		}
	}
	return sc.Err()
}

// recordText is a record's message text ("" for none).
func recordText(r teleportRecord) string {
	if r.Message == nil {
		return ""
	}
	return contentText(r.Message.Content)
}

// TestCloud checks the login teleport needs (claude auth status --json) and that
// `claude --help` still has the flags this module uses. It starts no session.
func (m *Module) TestCloud(ctx context.Context, h agent.Host, _ agent.Install, _ string) (agent.CloudTest, error) {
	var t agent.CloudTest
	a, err := login(ctx, h)
	if err != nil {
		msg := err.Error()
		for _, e := range []error{agent.ErrSignedOut, agent.ErrNotEligible} {
			msg = strings.TrimPrefix(msg, e.Error()+": ")
		}
		t.Checks = append(t.Checks, agent.CloudCheck{Text: msg})
		return t, err
	}
	t.Account = loginLabel(a)
	t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: t.Account + " login"})
	r, err := h.Exec().Run(ctx, []string{"claude", "--help"}, agent.RunOptions{Timeout: 20 * time.Second})
	if err != nil {
		return t, err
	}
	help := string(r.Stdout) + string(r.Stderr)
	for _, f := range []string{"--teleport", "--cloud"} {
		if strings.Contains(help, f) {
			t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: f + " found"})
		} else {
			t.Checks = append(t.Checks, agent.CloudCheck{Text: "`claude --help` no longer lists " + f})
		}
	}
	return t, nil
}
