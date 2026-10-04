package fakecloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Stub is a cloud-only module that reaches the fake's own cloud (fake-cloud) through the
// fakecloud program, with all five cloud capabilities: what a second-wave cloud module
// looks like. The conformance kit and the core's tests use it; hopsesh never registers it.
func Stub() agent.Module { return stub{} }

type stub struct{ agent.NoLocal }

var (
	_ agent.CloudLister   = stub{}
	_ agent.CloudSender   = stub{}
	_ agent.CloudFetcher  = stub{}
	_ agent.CloudFollower = stub{}
	_ agent.CloudArchiver = stub{}
)

func (stub) Spec() agent.Spec {
	return agent.Spec{
		ID: "fakecloud", Name: "Fake Cloud Agent", Vendor: "Example", Stability: agent.Experimental, Tested: []string{"1.0"},
		Binaries: []agent.Binary{{Name: "fakecloud", VersionArgs: []string{"--version"}}},
		Clouds: []agent.Cloud{{
			Name: FakeCloud, Title: "Fake cloud", Driver: "fakecloud", Tested: []string{"1.0"}, Hosts: []string{"github.com"},
			Up: agent.FidBrief, Down: agent.FidText, CodeUp: []agent.CodeWay{agent.ViaBranch}, CodeDown: []agent.CodeWay{agent.ViaBranch},
			Needs: []agent.Need{agent.NeedGitHub, agent.NeedPushedBranch},
			Watch: agent.Watch{Surface: "the fake's own cloud CLI (tests only)", Docs: []string{"https://cloud.example.com/docs.md"}, Grep: "remote"},
		}},
	}
}

func (s stub) Detect(_ context.Context, h agent.Host) (agent.Install, error) {
	return agent.DefaultInstall(s.Spec(), h), nil
}

// run runs `fakecloud remote …` and maps its exit codes onto the SDK's errors.
func (stub) run(ctx context.Context, h agent.Host, dir string, args ...string) ([]byte, error) {
	r, err := h.Exec().Run(ctx, append([]string{"fakecloud", "remote"}, args...), agent.RunOptions{Dir: dir, Timeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	msg := strings.TrimSpace(string(r.Stderr))
	switch r.Code {
	case 0:
		return r.Stdout, nil
	case ExitSignedOut:
		return nil, fmt.Errorf("%w: %s", agent.ErrSignedOut, msg)
	case ExitNotEligible:
		return nil, fmt.Errorf("%w: %s", agent.ErrNotEligible, msg)
	case ExitRepoMismatch:
		return nil, fmt.Errorf("%w: %s", agent.ErrRepoUnsupported, msg)
	case ExitNotFound:
		return nil, fmt.Errorf("%w: %s", agent.ErrNotFound, msg)
	}
	return nil, fmt.Errorf("fakecloud remote %s: exit %d: %s", args[0], r.Code, msg)
}

func (stub) session(x Session) agent.CloudSession {
	st := agent.CloudState(x.State)
	switch st {
	case agent.CloudRunning, agent.CloudIdle, agent.CloudDone, agent.CloudFailed, agent.CloudArchived:
	default:
		st = agent.CloudUnknown
	}
	return agent.CloudSession{Key: agent.SessionKey{Agent: "fakecloud", Session: agent.SessionID(x.ID)}, Cloud: FakeCloud, URL: x.URL(),
		Title: x.Title, Repo: x.Repo, Branch: nonEmpty(x.Result, x.Branch), Base: x.Base, State: st, Updated: x.Updated, Attempts: x.Attempts}
}

func (s stub) ListCloud(ctx context.Context, h agent.Host, _ agent.Install, q agent.CloudQuery) (agent.CloudListing, error) {
	known := make([]string, len(q.Known))
	for i, k := range q.Known {
		known[i] = string(k)
	}
	out, err := s.run(ctx, h, "", "list", "--known", strings.Join(known, ","))
	if err != nil {
		return agent.CloudListing{}, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		return agent.CloudListing{}, fmt.Errorf("fakecloud remote list: %w", err)
	}
	var l agent.CloudListing
	for i, r := range raw {
		var x Session
		if err := json.Unmarshal(r, &x); err != nil || x.ID == "" {
			if err == nil {
				err = errors.New("a session without an id")
			}
			l.Errors = append(l.Errors, agent.SessionError{Path: fmt.Sprintf("list[%d]", i), Err: err})
			continue
		}
		if q.Repo != "" && x.Repo != q.Repo {
			continue
		}
		l.Sessions = append(l.Sessions, s.session(x))
	}
	return l, nil
}

func (s stub) SendCloud(ctx context.Context, h agent.Host, _ agent.Install, r agent.SendRequest) (agent.CloudSession, error) {
	out, err := s.run(ctx, h, r.Dir, "new", "--repo", r.Repo, "--branch", r.Branch, "--title", r.Title, r.Brief)
	if err != nil {
		return agent.CloudSession{}, err
	}
	var x Session
	if err := json.Unmarshal(out, &x); err != nil {
		return agent.CloudSession{}, err
	}
	return s.session(x), nil
}

func (s stub) FetchCloud(ctx context.Context, h agent.Host, _ agent.Install, id agent.SessionID, t agent.FetchTarget) (agent.Fetched, error) {
	out, err := s.run(ctx, h, t.Dir, "pull", string(id))
	if err != nil {
		return agent.Fetched{}, err
	}
	var x Session
	if err := json.Unmarshal(out, &x); err != nil {
		return agent.Fetched{}, err
	}
	seg := &ir.Segment{Header: ir.Header{Agent: "fakecloud", SessionID: x.ID, Title: x.Title, GitBranch: x.Result}}
	for _, m := range x.Messages {
		actor := ir.Agent
		if m.Role == "user" {
			actor = ir.User
		}
		seg.Nodes = append(seg.Nodes, ir.Node{Kind: ir.KindMessage, Actor: actor, Time: m.Time, Text: m.Text})
	}
	ir.Chain(seg.Nodes, "")
	return agent.Fetched{Code: agent.CodeResult{Way: agent.ViaBranch, Branch: x.Result}, Segment: seg, Expected: len(x.Messages),
		Loss: []string{"tool calls stay in the cloud"}}, nil
}

func (s stub) FollowUp(ctx context.Context, h agent.Host, _ agent.Install, id agent.SessionID, text string) (agent.CloudSession, error) {
	out, err := s.run(ctx, h, "", "message", string(id), text)
	if err != nil {
		return agent.CloudSession{}, err
	}
	var x Session
	if err := json.Unmarshal(out, &x); err != nil {
		return agent.CloudSession{}, err
	}
	return s.session(x), nil
}

func (s stub) Archive(ctx context.Context, h agent.Host, _ agent.Install, id agent.SessionID) error {
	_, err := s.run(ctx, h, "", "archive", string(id))
	return err
}
