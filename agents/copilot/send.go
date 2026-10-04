package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

var _ agent.CloudSender = (*Module)(nil)

// createTimeout bounds gh agent-task create: it queues the task, then polls for the pull
// request for up to ten seconds (gh's own backoff) before it prints.
const createTimeout = 2 * time.Minute

var (
	// sessionLink is the agent session's page gh prints once the task has its pull request
	// (agentSessionWebURL in gh's pkg/cmd/agent-task/create); prLink, the same without a
	// session id; queued, what it prints when the pull request has not appeared yet.
	sessionLink = regexp.MustCompile(`https://github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+)/agent-sessions/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\b`)
	prOnlyLink  = regexp.MustCompile(`https://github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+)\b`)
	queued      = regexp.MustCompile(`(?i)job (\S+) queued`)
	// Unverified: how gh words a repository the cloud agent cannot work on, or a base branch
	// it cannot find.
	repoWords = regexp.MustCompile(`(?i)could not resolve to a repository|repository .*(not found|not accessible|is archived)|base (branch|ref) .*(not found|does not exist)|HTTP 422`)
)

// SendCloud starts a Copilot cloud agent task with the briefing as its description:
// `gh agent-task create -F - --base <handoff branch> -R <owner/repo>`, the briefing on
// standard input ("-F -", documented in gh 2.97's help). The agent starts from the base
// branch, which the core pushed, and opens its pull request against it.
//
// What gh prints comes from its source (pkg/cmd/agent-task/create, 2.97.0), not from a real
// run: the agent session's link https://github.com/OWNER/REPO/pull/N/agent-sessions/<uuid>
// once the pull request exists (it polls for up to ten seconds), the pull request's link
// when the job has no session id yet, or "job <id> queued. View progress: …" when the pull
// request has not appeared. Without a session id in the output, the new task is the one in
// gh agent-task list that was not there before the create.
func (m *Module) SendCloud(ctx context.Context, h agent.Host, _ agent.Install, r agent.SendRequest) (agent.CloudSession, error) {
	host, repo, _ := strings.Cut(r.Repo, "/")
	switch {
	case !strings.HasPrefix(r.Brief, agent.NotePrefix):
		return agent.CloudSession{}, fmt.Errorf("the briefing must start with %q", agent.NotePrefix)
	case r.Repo == "" || host != "github.com" || strings.Count(repo, "/") != 1:
		return agent.CloudSession{}, fmt.Errorf("%w: the Copilot cloud agent works on GitHub repositories, not %s", agent.ErrRepoUnsupported, nonEmpty(r.Repo, "this one"))
	case r.Branch == "":
		return agent.CloudSession{}, fmt.Errorf("no branch given: the Copilot cloud agent starts from a branch on GitHub")
	case r.Code != "" && r.Code != agent.ViaBranch:
		return agent.CloudSession{}, fmt.Errorf("%w: the Copilot cloud agent takes the code on a branch, not as %s", agent.ErrUnsupported, r.Code)
	}
	before, err := taskIDs(ctx, h)
	if err != nil {
		return agent.CloudSession{}, err
	}
	res, err := h.Exec().Run(ctx, []string{"gh", "agent-task", "create", "-F", "-", "--base", r.Branch, "-R", repo},
		agent.RunOptions{Dir: r.Dir, Stdin: []byte(r.Brief), Timeout: createTimeout})
	if err != nil {
		return agent.CloudSession{}, err
	}
	if res.Code != 0 {
		text := string(res.Stderr) + "\n" + string(res.Stdout)
		if res.Code != 4 && !signedOutWords.MatchString(text) && repoWords.MatchString(text) {
			return agent.CloudSession{}, fmt.Errorf("%w: GitHub refused the task for %s: %s", agent.ErrRepoUnsupported, repo, firstLine(text))
		}
		return agent.CloudSession{}, refusal(res, "gh agent-task create")
	}
	out := string(ansi.ReplaceAll(res.Stdout, nil))
	cs := agent.CloudSession{Key: agent.SessionKey{Agent: id}, Cloud: cloudName, Title: r.Title, Repo: strings.ToLower(r.Repo), Base: r.Base,
		State: agent.CloudRunning, Updated: time.Now().UTC()}
	pr := 0
	switch mm := sessionLink.FindStringSubmatch(out); {
	case mm != nil:
		cs.Key.Session, cs.URL = agent.SessionID(mm[4]), mm[0]
		pr, _ = strconv.Atoi(mm[3])
	default:
		if mm := prOnlyLink.FindStringSubmatch(out); mm != nil {
			pr, _ = strconv.Atoi(mm[3])
		}
		t, ok, err := newTask(ctx, h, before, repo, pr)
		if err != nil {
			return agent.CloudSession{}, err
		}
		if !ok {
			job := ""
			if mm := queued.FindStringSubmatch(out); mm != nil {
				job = " (job " + mm[1] + ")"
			}
			return agent.CloudSession{}, &agent.FormatError{Path: "gh agent-task create",
				Err: fmt.Errorf("GitHub took the task%s, but hopsesh could not find its session in gh agent-task list; look for it there: %q", job, firstLine(out))}
		}
		s := session(t, nil)
		cs.Key.Session, cs.URL = s.Key.Session, s.URL
		if p := t.pr(); p > 0 {
			pr = p
		}
	}
	if pr > 0 {
		cs.PR = "#" + strconv.Itoa(pr)
		if cs.URL == "" {
			cs.URL = fmt.Sprintf("https://github.com/%s/pull/%d/agent-sessions/%s", repo, pr, cs.Key.Session)
		}
	}
	return cs, nil
}

// taskIDs are the ids of the agent tasks gh lists now (the newest 100).
func taskIDs(ctx context.Context, h agent.Host) (map[string]bool, error) {
	out, err := run(ctx, h, 30*time.Second, "agent-task", "list", "--json", "id", "--limit", strconv.Itoa(maxLimit))
	if err != nil {
		return nil, err
	}
	var ts []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &ts); err != nil {
		return nil, &agent.FormatError{Path: "gh agent-task list", Err: err}
	}
	ids := map[string]bool{}
	for _, t := range ts {
		ids[t.ID] = true
	}
	return ids, nil
}

// newTask finds the task a create made: listed now, not before, in repo (and on pull request
// pr, when gh printed one), the newest first. gh's list may take a moment to show it, so it
// asks a few times.
func newTask(ctx context.Context, h agent.Host, before map[string]bool, repo string, pr int) (task, bool, error) {
	for try := 0; try < 4; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return task{}, false, ctx.Err()
			case <-time.After(listRetry):
			}
		}
		out, err := run(ctx, h, 30*time.Second, "agent-task", "list", "--json", taskFields, "--limit", "20")
		if err != nil {
			return task{}, false, err
		}
		var ts []task
		if err := json.Unmarshal(out, &ts); err != nil {
			return task{}, false, &agent.FormatError{Path: "gh agent-task list", Err: err}
		}
		var found *task
		for i, t := range ts {
			if t.ID == "" || before[t.ID] || !strings.EqualFold(t.repo(), repo) || pr > 0 && t.pr() > 0 && t.pr() != pr {
				continue
			}
			if found == nil || parseTime(t.CreatedAt).After(parseTime(found.CreatedAt)) {
				found = &ts[i]
			}
		}
		if found != nil {
			return *found, true, nil
		}
	}
	return task{}, false, nil
}

// listRetry is how long newTask waits between listings (shorter in tests).
var listRetry = 2 * time.Second
