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
		Watch: agent.Watch{
			Surface: "cloud sessions started with `claude --cloud \"<briefing>\"` in the user's terminal (its workspace-trust question, then the `View: https://claude.ai/code/session_…` and `Resume with: claude --teleport session_…` lines hopsesh reads, and its refusals without a terminal or with --print), brought back with `claude --teleport <id>`, Remote Control, cloud environments, and the transcript records a teleport or bridge leaves",
			Docs: append(docs("claude-code-on-the-web", "web-quickstart", "cloud-environments", "remote-control", "desktop",
				"sessions", "routines", "self-hosted-environments", "env-vars", "feature-availability", "data-usage", "legal-and-compliance"),
				"https://code.claude.com/docs/llms.txt"),
			Feeds: []agent.Feed{{Kind: agent.FeedMarkdown, URL: "https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md"}},
			// The canaries of the terminal step are in it: the lines ReadStep reads and the
			// refusals it maps.
			Grep: `teleport|--cloud|--remote|Remote Control|bridge|cloud session|cse_|self-hosted|Continue in|environment|sessions:|deprecat|` +
				`Created cloud session|View:|Resume with|interactive terminal|combined with --print|safety check|trust this folder|attach`,
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
	for _, p := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, p)
	}
	if rest, ok := strings.CutPrefix(s, "claude.ai/code/"); ok {
		s = rest
		if i := strings.IndexAny(s, "?#/"); i >= 0 {
			s = s[:i]
		}
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
	}, nil
}

// teleportRecord is what a teleport leaves at the top of the copy: the session it came
// from and how many messages the cloud sent (seen in anthropics/claude-code#95873; the
// rest of the record set is undocumented).
type teleportRecord struct {
	Type         string `json:"type"`
	Remote       string `json:"remoteSessionId"`
	MessageCount *int   `json:"messageCount"`
	UUID         string `json:"uuid"`
	IsMeta       bool   `json:"isMeta"`
	IsSidechain  bool   `json:"isSidechain"`
	Message      *struct {
		ID string `json:"id"`
	} `json:"message"`
}

// maxAdoptScan bounds how much of a candidate transcript Adopted reads.
const maxAdoptScan = 256 << 20

// Adopted finds the copy a teleport wrote in the worktree's project folder: a transcript
// written after a.Since whose teleported-from record names the session (or any such
// record when a.Session is empty: the picker chose). Failing that, the one transcript
// written there since (the worktree is hopsesh's own, so nothing else runs in it), with no
// stated count. It counts the messages the copy holds (a reply split over several records
// counts once).
func (m *Module) Adopted(_ context.Context, h agent.Host, in agent.Install, a agent.Adopt) (agent.Adoption, error) {
	pa, fsys := h.Path(), h.FS()
	dir := in.Root(a.Root)
	for _, part := range strings.Split(a.Dir, "/") {
		dir = pa.Join(dir, part)
	}
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		if isNotExist(err) {
			return agent.Adoption{}, fmt.Errorf("%w: nothing in %s yet", agent.ErrNotFound, dir)
		}
		return agent.Adoption{}, err
	}
	want := canonical(string(a.Session))
	type cand struct {
		file    string
		remote  string
		count   *int
		msgs    int
		mod     time.Time
		stubbed bool
	}
	var best, loose *cand
	nLoose := 0
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".jsonl") || e.ModTime().Before(a.Since.Add(-2*time.Second)) {
			continue
		}
		c := &cand{file: pa.Join(dir, n), mod: e.ModTime()}
		if err := scanTeleported(fsys, c.file, &c.remote, &c.count, &c.msgs); err != nil {
			continue
		}
		c.stubbed = c.remote != ""
		switch {
		case c.stubbed && (want == "" || canonical(c.remote) == want):
			if best == nil || c.mod.After(best.mod) {
				best = c
			}
		case !c.stubbed:
			nLoose++
			if loose == nil || c.mod.After(loose.mod) {
				loose = c
			}
		}
	}
	if best == nil && nLoose == 1 {
		best = loose
	}
	if best == nil {
		return agent.Adoption{}, fmt.Errorf("%w: no teleported copy in %s yet", agent.ErrNotFound, dir)
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
	out := agent.Adoption{Session: s.summary(), Remote: agent.SessionID(canonical(best.remote)), Restored: best.msgs}
	if out.Remote == "" {
		out.Remote = agent.SessionID(want)
	}
	if best.count != nil {
		out.Expected, out.Stated = *best.count, true
	}
	return out, nil
}

// scanTeleported reads a transcript for its teleported-from record and counts its
// messages.
func scanTeleported(fsys agent.FS, file string, remote *string, count **int, msgs *int) error {
	f, err := fsys.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	seen := map[string]bool{}
	var read int64
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
			if *remote == "" {
				*remote, *count = r.Remote, r.MessageCount
			}
		case "user", "assistant":
			if r.IsMeta || r.IsSidechain {
				continue
			}
			if r.Type == "assistant" && r.Message != nil && r.Message.ID != "" {
				if seen[r.Message.ID] {
					continue
				}
				seen[r.Message.ID] = true
			}
			*msgs++
		}
	}
	return sc.Err()
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
