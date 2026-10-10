package move

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// A synthetic agent proves this workflow dispatches capabilities, not agent IDs.
type endingModule struct {
	*claude.Module
	cursor      ir.Cursor
	account     agent.Account
	stopped     []agent.SessionKey
	err         error
	state       agent.Liveness
	missingLive bool
}

func (m *endingModule) Spec() agent.Spec { s := m.Module.Spec(); s.ID = "test-agent"; return s }
func (m *endingModule) Read(context.Context, agent.Host, agent.Install, agent.Summary, ir.Cursor) (ir.Segment, error) {
	return ir.Segment{Cursor: m.cursor}, nil
}
func (m *endingModule) Account(context.Context, agent.Host, agent.Install) (agent.Account, error) {
	return m.account, nil
}
func (m *endingModule) Stop(_ context.Context, _ agent.Host, _ agent.Install, s agent.Summary, _ time.Duration) error {
	m.stopped = append(m.stopped, s.Key)
	m.cursor.Offset++
	return m.err
}
func (m *endingModule) Live(_ context.Context, _ agent.Host, _ agent.Install, ids []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	if m.missingLive {
		return nil, nil
	}
	return map[agent.SessionID]agent.LiveInfo{ids[0]: {State: m.state}}, nil
}

func endingFixture(t *testing.T) (*Plan, Input, *endingModule) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &endingModule{Module: claude.New(), cursor: ir.Cursor{Offset: 42}, account: agent.Account{LoggedIn: true, Provider: "test", Observation: "login-one"}, state: agent.Ended}
	profile := &agent.RuntimeProfile{ID: "personal", Root: root, Name: "Personal", Binding: "binding-one", Account: &m.account}
	// Account observation may change independently of the pinned plan.
	account := m.account
	profile.Account = &account
	machine := &host.Machine{Name: "here", Local: true, Facts: host.Facts{OS: "linux", Home: root, Endpoint: "endpoint-one"}}
	s := agent.Summary{Key: agent.SessionKey{Agent: "test-agent", Profile: profile.ID, Session: "original"}, Path: filepath.Join(root, "original.jsonl")}
	in := Input{Target: Side{Machine: machine, Module: m, Install: agent.Install{Agent: "test-agent", Binary: "test", Roots: map[string]string{"home": root}, Profile: profile}}}
	p := &Plan{Placement: agent.Placement{Key: s.Key}, Target: Endpoint{Location: "here", ID: "endpoint-one", Profile: profile.ID, Binding: profile.Binding}, Blockers: []string{"the destination copy is open; quit it first"}, Continue: &ContinuePlan{Relation: RelationAppend, AppendTo: &s, expect: m.cursor}}
	return p, in, m
}

func TestEndReturnDestinationUsesModuleAndRequiresFreshPlan(t *testing.T) {
	p, in, m := endingFixture(t)
	if err := EndDestination(context.Background(), p, in); err != nil {
		t.Fatal(err)
	}
	if len(m.stopped) != 1 || m.stopped[0] != p.Placement.Key {
		t.Fatalf("ended wrong session: %v", m.stopped)
	}
	if err := EndDestination(context.Background(), p, in); err == nil || !strings.Contains(err.Error(), "changed") || len(m.stopped) != 1 {
		t.Fatalf("reused stale review: %v %v", err, m.stopped)
	}
}

func TestEndReturnDestinationRejectsChangedBoundaryBeforeModuleAction(t *testing.T) {
	for _, change := range []string{"key", "profile", "binding", "endpoint", "machine", "login", "history", "new session", "windows", "remote", "snapshot", "unsupported module"} {
		t.Run(change, func(t *testing.T) {
			p, in, m := endingFixture(t)
			switch change {
			case "key":
				p.Placement.Key.Session = "another"
			case "profile":
				p.Target.Profile = "work"
			case "binding":
				p.Target.Binding = "another"
			case "endpoint":
				p.Target.ID = "another"
			case "machine":
				p.Target.Location = "elsewhere"
			case "login":
				m.account.Observation = "login-two"
			case "history":
				m.cursor.Offset++
			case "new session":
				p.Continue.Relation = RelationNew
			case "windows":
				in.Target.Machine.Facts.OS = "windows"
			case "remote":
				in.Target.Machine.Local = false
			case "snapshot":
				in.Target.Machine = host.NewSnapshot("here", host.Facts{}, nil)
			case "unsupported module":
				in.Target.Module = codex.New()
			}
			if err := EndDestination(context.Background(), p, in); err == nil || len(m.stopped) != 0 {
				t.Fatalf("accepted changed %s: %v %v", change, err, m.stopped)
			}
		})
	}
}

func TestEndReturnDestinationDoesNotClaimSuccessOnStopFailureOrRestart(t *testing.T) {
	for _, failure := range []string{"stop error", "restarted", "unknown", "missing live result"} {
		t.Run(failure, func(t *testing.T) {
			p, in, m := endingFixture(t)
			switch failure {
			case "stop error":
				m.err = errors.New("normal exit timed out")
			case "restarted":
				m.state = agent.Live
			case "missing live result":
				m.missingLive = true
			default:
				m.state = agent.Unknown
			}
			if err := EndDestination(context.Background(), p, in); err == nil {
				t.Fatal("claimed that original ended")
			}
		})
	}
}
