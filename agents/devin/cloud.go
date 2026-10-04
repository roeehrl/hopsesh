package devin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// cloudName is Devin's cloud as a location.
const cloudName = "devin"

// cloud declares Devin's cloud sessions as data. Down is the code only: the session's pull
// request branch, which the core fetches into a worktree. Devin's messages are in its API
// (which needs a key) and in the CLI's interactive sessions, so they stay in the cloud.
func cloud() agent.Cloud {
	return agent.Cloud{
		Name:   cloudName,
		Title:  "Devin",
		Driver: "devin",
		// The stand-in's version: no real Devin CLI ran (it installs from a download script),
		// so a real one reads as untested.
		Tested:   []string{"2026.9.24"},
		Hosts:    []string{"github.com"},
		Up:       agent.FidBrief,
		Down:     agent.FidCode,
		CodeUp:   []agent.CodeWay{agent.ViaBranch},
		CodeDown: []agent.CodeWay{agent.ViaPR},
		Needs:    []agent.Need{agent.NeedGitHub, agent.NeedPushedBranch},
		// Unverified: Devin names its branches devin/<timestamp>-<topic>.
		VendorPrefix: "devin/",
		Watch: agent.Watch{
			Surface: "Devin cloud sessions through `devin list --format json` (that it lists cloud sessions, and its fields, are unverified) and `devin auth status`, the CLI's cloud and handoff commands (`devin --cloud`, `/pickup`, `/handoff`), and the v3 REST API (its OpenAPI document) for the messages the CLI does not print",
			// The Devin CLI installs from a download script, not a package, so its help is not run.
			Docs: []string{devinOpenAPI, "https://docs.devin.ai/llms.txt", "https://docs.devin.ai/cli/handoff.md", "https://docs.devin.ai/cli/cloud.md",
				"https://docs.devin.ai/cli/reference/commands.md"},
			Feeds: []agent.Feed{{Kind: agent.FeedMarkdown, URL: "https://docs.devin.ai/cli/changelog/stable.md"}},
			Grep:  `session|handoff|pickup|cloud|--cloud|list|--format|auth status|teleport|pull|api|deprecat`,
		},
	}
}

const devinOpenAPI = "https://docs.devin.ai/v3-openapi.json"

var (
	_ agent.CloudLister  = (*Module)(nil)
	_ agent.CloudFetcher = (*Module)(nil)
	_ agent.CloudTester  = (*Module)(nil)
	_ agent.CloudLinker  = (*Module)(nil)
)

var (
	// Unverified: how the CLI words a missing login, a plan without Devin Cloud, and an
	// unknown session.
	signedOutWords   = regexp.MustCompile(`(?i)not logged in|auth login|log ?in first|unauthori[sz]ed|\b401\b|expired token|credentials`)
	notEligibleWords = regexp.MustCompile(`(?i)not enabled|not available|not included|upgrade|ACU limit|quota|\b403\b|forbidden`)
)

// run runs devin and maps a refusal onto the SDK's errors. The CLI holds the login;
// hopsesh never reads it.
func run(ctx context.Context, h agent.Host, args ...string) ([]byte, error) {
	r, err := h.Exec().Run(ctx, append([]string{"devin"}, args...), agent.RunOptions{Timeout: 30 * time.Second, Stdin: []byte{}})
	if err != nil {
		return nil, err
	}
	if r.Code != 0 {
		text := string(r.Stderr) + "\n" + string(r.Stdout)
		msg := firstLine(text)
		switch {
		case signedOutWords.MatchString(text):
			return nil, fmt.Errorf("%w: the Devin CLI here is not logged in (devin auth login)", agent.ErrSignedOut)
		case notEligibleWords.MatchString(text):
			return nil, fmt.Errorf("%w: Devin refused: %s", agent.ErrNotEligible, msg)
		}
		return nil, fmt.Errorf("devin %s: exit %d: %s", strings.Join(args[:min(2, len(args))], " "), r.Code, msg)
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

// record is one session in `devin list --format json`. The format is documented, its
// fields are not: these are the v3 API's names and their likely CLI spellings, all
// optional.
type record struct {
	ID        json.RawMessage `json:"id"`
	SessionID string          `json:"session_id"`
	DevinID   string          `json:"devin_id"`
	Title     string          `json:"title"`
	Name      string          `json:"name"`
	Status    string          `json:"status"`
	StatusEn  string          `json:"status_enum"`
	State     string          `json:"state"`
	Location  string          `json:"location"`
	Type      string          `json:"type"`
	Mode      string          `json:"mode"`
	Cloud     *bool           `json:"cloud"`
	IsCloud   *bool           `json:"is_cloud"`
	URL       string          `json:"url"`
	Created   json.RawMessage `json:"created_at"`
	Updated   json.RawMessage `json:"updated_at"`
	Repos     json.RawMessage `json:"repos"`
	Repo      json.RawMessage `json:"repo"`
	Branch    string          `json:"branch"`
	PR        *pullRequest    `json:"pull_request"`
	PRs       []pullRequest   `json:"pull_requests"`
}

type pullRequest struct {
	URL     string `json:"url"`
	PRURL   string `json:"pr_url"`
	Branch  string `json:"branch"`
	HeadRef string `json:"head_ref"`
	Number  int    `json:"number"`
}

var (
	devinID = regexp.MustCompile(`^devin-[0-9a-f]{16,64}$`)
	hexID   = regexp.MustCompile(`^[0-9a-f]{16,64}$`)
	prLink  = regexp.MustCompile(`^https://([^/]+)/([^/]+)/([^/]+)/pull/(\d+)`)
)

// sid is the record's session id in the devin-<hex> form ("" when it has none).
func (r record) sid() string {
	var s string
	_ = json.Unmarshal(r.ID, &s)
	for _, c := range []string{s, r.SessionID, r.DevinID} {
		switch {
		case devinID.MatchString(c):
			return c
		case hexID.MatchString(c):
			return "devin-" + c
		case c != "" && strings.Contains(r.URL, "app.devin.ai"):
			return c
		}
	}
	if s, ok := idOfLink(r.URL); ok {
		return s
	}
	return strings.TrimSpace(s)
}

// cloud reports whether a record is a cloud session: the list may hold local ones too
// (unverified), which have their own ids and no link to app.devin.ai.
func (r record) cloud() bool {
	loc := strings.ToLower(r.Location + " " + r.Type + " " + r.Mode)
	switch {
	case strings.Contains(loc, "local"):
		return false
	case strings.Contains(loc, "cloud"), r.Cloud != nil && *r.Cloud, r.IsCloud != nil && *r.IsCloud:
		return true
	}
	var s string
	_ = json.Unmarshal(r.ID, &s)
	return devinID.MatchString(s) || devinID.MatchString(r.SessionID) || strings.Contains(r.URL, "app.devin.ai/sessions/")
}

// repo is the record's first repository as owner/repo.
func (r record) repo() string {
	for _, raw := range []json.RawMessage{r.Repos, r.Repo} {
		var list []string
		var one string
		switch {
		case json.Unmarshal(raw, &list) == nil && len(list) > 0:
			one = list[0]
		case json.Unmarshal(raw, &one) == nil:
		default:
			var objs []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			}
			if json.Unmarshal(raw, &objs) == nil && len(objs) > 0 {
				one = nonEmpty(objs[0].Name, objs[0].URL)
			}
		}
		one = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(one, "https://"), "github.com/"), ".git")
		if strings.Count(one, "/") == 1 {
			return one
		}
	}
	for _, p := range r.prs() {
		if m := prLink.FindStringSubmatch(nonEmpty(p.URL, p.PRURL)); m != nil && m[1] == "github.com" {
			return m[2] + "/" + m[3]
		}
	}
	return ""
}

func (r record) prs() []pullRequest {
	if r.PR != nil {
		return append([]pullRequest{*r.PR}, r.PRs...)
	}
	return r.PRs
}

// branch and pr are the session's pull request's head branch and number.
func (r record) branch() (string, string) {
	for _, p := range r.prs() {
		b := nonEmpty(p.Branch, p.HeadRef)
		n := p.Number
		if m := prLink.FindStringSubmatch(nonEmpty(p.URL, p.PRURL)); m != nil && n == 0 {
			n, _ = strconv.Atoi(m[4])
		}
		pr := ""
		if n > 0 {
			pr = "#" + strconv.Itoa(n)
		}
		if b != "" || pr != "" {
			return nonEmpty(b, r.Branch), pr
		}
	}
	return r.Branch, ""
}

// state maps the session's status (the API's status_enum: working, blocked, finished,
// expired, suspended, …; the CLI's words are unverified).
func state(s string) agent.CloudState {
	switch k := strings.ToLower(strings.TrimSpace(s)); {
	case k == "":
		return agent.CloudUnknown
	case strings.Contains(k, "archiv"):
		return agent.CloudArchived
	case strings.Contains(k, "finish") || strings.Contains(k, "complete") || k == "done" || k == "exit" || k == "stopped":
		return agent.CloudDone
	case strings.Contains(k, "error") || strings.Contains(k, "fail") || strings.Contains(k, "expired"):
		return agent.CloudFailed
	case strings.Contains(k, "block") || strings.Contains(k, "suspend") || strings.Contains(k, "sleep") || strings.Contains(k, "wait"):
		return agent.CloudIdle
	case strings.Contains(k, "work") || strings.Contains(k, "run") || strings.Contains(k, "resum") || k == "new" || k == "claimed" || k == "queued":
		return agent.CloudRunning
	}
	return agent.CloudUnknown
}

// stamp reads a time as RFC 3339 text or Unix seconds.
func stamp(raw json.RawMessage) time.Time {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t
			}
		}
		return time.Time{}
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		if n > 1e12 {
			n /= 1000 // milliseconds
		}
		return time.Unix(int64(n), 0).UTC()
	}
	return time.Time{}
}

// list reads `devin list --format json`: an array of sessions, or an object holding one
// under "sessions" or "data" (unverified).
func list(ctx context.Context, h agent.Host) ([]record, []agent.SessionError, error) {
	out, err := run(ctx, h, "list", "--format", "json")
	if err != nil {
		return nil, nil, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		var wrapped struct {
			Sessions []json.RawMessage `json:"sessions"`
			Data     []json.RawMessage `json:"data"`
		}
		if json.Unmarshal(out, &wrapped) != nil {
			return nil, nil, &agent.FormatError{Path: "devin list --format json", Err: err}
		}
		raw = append(wrapped.Sessions, wrapped.Data...)
	}
	var recs []record
	var errs []agent.SessionError
	for i, b := range raw {
		var r record
		if err := json.Unmarshal(b, &r); err != nil {
			errs = append(errs, agent.SessionError{Path: fmt.Sprintf("devin list[%d]", i), Err: err})
			continue
		}
		if !r.cloud() {
			continue
		}
		if r.sid() == "" {
			errs = append(errs, agent.SessionError{Path: fmt.Sprintf("devin list[%d]", i), Err: errors.New("a cloud session without an id")})
			continue
		}
		recs = append(recs, r)
	}
	return recs, errs, nil
}

func (m *Module) session(r record) agent.CloudSession {
	sid := r.sid()
	cs := agent.CloudSession{Key: agent.SessionKey{Agent: id, Session: agent.SessionID(sid)}, Cloud: cloudName, Title: nonEmpty(r.Title, r.Name),
		State: state(nonEmpty(r.StatusEn, nonEmpty(r.Status, r.State))), Updated: stamp(r.Updated), URL: r.URL}
	if cs.Updated.IsZero() {
		cs.Updated = stamp(r.Created)
	}
	if cs.URL == "" {
		cs.URL = m.CloudURL(cloudName, agent.SessionID(sid))
	}
	if repo := r.repo(); repo != "" {
		cs.Repo = strings.ToLower("github.com/" + repo)
	}
	cs.Branch, cs.PR = r.branch()
	return cs
}

// ListCloud lists Devin's cloud sessions from `devin list --format json`, leaving out
// local ones.
func (m *Module) ListCloud(ctx context.Context, h agent.Host, _ agent.Install, q agent.CloudQuery) (agent.CloudListing, error) {
	recs, errs, err := list(ctx, h)
	if err != nil {
		return agent.CloudListing{}, err
	}
	l := agent.CloudListing{Errors: errs}
	for _, r := range recs {
		cs := m.session(r)
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

// FetchCloud brings a session's code: its pull request branch, as the listing names it.
// Devin's own way home (/pickup) runs only inside the CLI's interactive session, and the
// messages are not printed by any non-interactive command, so they stay in the cloud.
func (m *Module) FetchCloud(ctx context.Context, h agent.Host, _ agent.Install, sid agent.SessionID, _ agent.FetchTarget) (agent.Fetched, error) {
	recs, _, err := list(ctx, h)
	if err != nil {
		return agent.Fetched{}, err
	}
	for _, r := range recs {
		if r.sid() != string(sid) {
			continue
		}
		branch, pr := r.branch()
		if branch == "" {
			return agent.Fetched{}, fmt.Errorf("%w: Devin has not pushed a branch hopsesh can see for %s; the Devin CLI brings a cloud session home only interactively (/pickup in `devin --cloud -r %s`)",
				agent.ErrUnsupported, sid, sid)
		}
		return agent.Fetched{
			Code: agent.CodeResult{Way: agent.ViaPR, Branch: branch, PR: pr},
			Loss: []string{"Devin's messages stay in the cloud: the Devin CLI shows them only in its interactive session"},
		}, nil
	}
	return agent.Fetched{}, fmt.Errorf("%w: devin list has no cloud session %s", agent.ErrNotFound, sid)
}

// ParseCloudLink reads a session's link on app.devin.ai or its devin-… id.
func (m *Module) ParseCloudLink(s string) (string, agent.SessionID, bool) {
	s = strings.TrimSpace(s)
	if devinID.MatchString(s) {
		return cloudName, agent.SessionID(s), true
	}
	sid, ok := idOfLink(s)
	return cloudName, agent.SessionID(sid), ok
}

// idOfLink reads https://app.devin.ai/sessions/<hex> (unverified: that the link holds the
// id without its devin- prefix; both are read).
func idOfLink(s string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://"), "app.devin.ai/sessions/")
	if !ok {
		return "", false
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	switch {
	case devinID.MatchString(rest):
		return rest, true
	case hexID.MatchString(rest):
		return "devin-" + rest, true
	}
	return "", false
}

// CloudURL is the session's page on app.devin.ai.
func (m *Module) CloudURL(_ string, sid agent.SessionID) string {
	return "https://app.devin.ai/sessions/" + strings.TrimPrefix(string(sid), "devin-")
}

// TestCloud asks the CLI for its login (devin auth status; its wording is unverified, so
// only its exit code and first line are used), lists the sessions once to learn whether
// the account has Devin Cloud, and starts nothing.
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
	out, err := run(ctx, h, "auth", "status")
	if err != nil {
		if !errors.Is(err, agent.ErrNotEligible) && !errors.Is(err, agent.ErrNotInstalled) {
			err = fmt.Errorf("%w: the Devin CLI here is not logged in (devin auth login)", agent.ErrSignedOut)
		}
		return fail(err)
	}
	t.Account = clip(firstLine(string(out)), 80)
	t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: nonEmpty(t.Account, "logged in")})
	recs, _, err := list(ctx, h)
	if err != nil {
		return fail(err)
	}
	t.Checks = append(t.Checks, agent.CloudCheck{OK: true, Text: fmt.Sprintf("devin list shows %d cloud sessions", len(recs))})
	return t, nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
