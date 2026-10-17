package gui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
)

func TestHistorySaveRejectsLateReviewPublications(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	t.Cleanup(a.Shutdown)
	for _, kind := range []string{move.KindContinue, move.KindHop, move.KindHandoff} {
		t.Run(kind, func(t *testing.T) {
			core, generation, err := a.reviewSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			ready, release := make(chan struct{}), make(chan struct{})
			result := make(chan error, 1)
			go func() {
				close(ready)
				<-release
				result <- a.publishReview(generation, &move.Plan{Kind: kind}, move.Input{})
			}()
			<-ready
			if err := a.SetHistorySettings(config.History{LineageMB: 128}); err != nil {
				t.Fatal(err)
			}
			// Budget equality alone cannot reject this old planner (ABA).
			if err := a.SetHistorySettings(core.Cfg.History); err != nil {
				t.Fatal(err)
			}
			close(release)
			if err := <-result; err != errReviewChanged || a.plan != nil {
				t.Fatalf("old %s review restored after save: %v", kind, err)
			}
		})
	}
}

func TestHistorySaveDuringApplyPreservesActionAndResult(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	t.Cleanup(a.Shutdown)
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	key := findEntry(t, scan, "claude/"+sid).Key
	if _, err := a.Plan(app.LocalName(), key, "codex", OptsDTO{}); err != nil {
		t.Fatal(err)
	}
	p := a.plan
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.Emitter = func(name string, _ any) {
		if name == ProgressEvent {
			once.Do(func() { close(entered); <-release })
		}
	}
	result := make(chan error, 1)
	go func() { _, err := a.Apply(); result <- err }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("apply did not start")
	}
	if err := a.SetHistorySettings(config.History{LineageMB: 128}); err != nil {
		t.Fatal(err)
	}
	a.ClosePlan()
	a.mu.Lock()
	preserved := a.plan == p && a.actionRunning
	a.mu.Unlock()
	if !preserved {
		t.Fatal("save/close revoked an active apply's borrowed plan")
	}
	if _, err := a.Plan(app.LocalName(), key, "codex", OptsDTO{}); err != errActionRunning {
		t.Fatalf("another review replaced an active apply: %v", err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if a.plan != p || a.res == nil || a.actionRunning {
		t.Fatal("completed action lost the result needed by OpenResult")
	}
}

type reviewReplyGate struct {
	io.Reader
	armed   atomic.Bool
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (g *reviewReplyGate) Read(p []byte) (int, error) {
	if g.armed.Load() {
		g.once.Do(func() { close(g.entered); <-g.release })
	}
	return g.Reader.Read(p)
}

// A real peer protocol connection, with a controlled reply read. All source,
// destination, receive settings and native transcript writes live in temp homes.
func peerReviewFixture(t *testing.T) (*App, string, string, *reviewReplyGate, *atomic.Int32) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "hopsesh"+exeSuffix())
	build := exec.Command("go", "build", "-o", bin, "./cmd/hopsesh")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	box, _ := filepath.EvalSymlinks(t.TempDir())
	boxRepo := filepath.Join(box, "git", "demo")
	for _, path := range []string{boxRepo, filepath.Join(box, ".claude", "projects")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	boxEnv := []string{"HOME=" + box, "USERPROFILE=" + box, "PATH=" + testPath(), "HOPSESH_MACHINE=box", "HOPSESH_CONFIG_DIR=" + filepath.Join(box, "config"), "HOPSESH_STATE_DIR=" + filepath.Join(box, "state"), "CLAUDE_CONFIG_DIR=", "CODEX_HOME="}
	recv := exec.Command(bin, "receive", "on")
	recv.Env = boxEnv
	if out, err := recv.CombinedOutput(); err != nil {
		t.Fatalf("receive: %v %s", err, out)
	}
	home(t)
	t.Setenv("HOPSESH_MACHINE", "here")
	a := NewApp(all.Registry())
	t.Cleanup(a.Shutdown)
	if err := a.AddHost("box", "box", false, false); err != nil {
		t.Fatal(err)
	}
	gate := &reviewReplyGate{entered: make(chan struct{}), release: make(chan struct{})}
	closed := &atomic.Int32{}
	a.core.PeerDial = func(_ context.Context, _ config.Host) (*app.PeerConn, error) {
		cmd := exec.Command(bin, "peer", "--stdio")
		cmd.Env = boxEnv
		in, _ := cmd.StdinPipe()
		out, _ := cmd.StdoutPipe()
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		gate.Reader = out
		return &app.PeerConn{Out: gate, In: in, Close: func() { closed.Add(1); _ = in.Close(); _ = cmd.Wait() }}, nil
	}
	scan, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	return a, findEntry(t, scan, "claude/"+sid).Key, boxRepo, gate, closed
}

func TestHistorySaveDuringPushRPCPreservesTransport(t *testing.T) {
	a, key, repo, gate, closed := peerReviewFixture(t)
	if _, err := a.PushPlan(key, "box", "", OptsDTO{TargetDir: repo}); err != nil {
		t.Fatal(err)
	}
	gate.armed.Store(true)
	result := make(chan error, 1)
	go func() { _, err := a.PushApply(); result <- err }()
	<-gate.entered
	if err := a.SetHistorySettings(config.History{LineageMB: 128}); err != nil {
		t.Fatal(err)
	}
	a.ClosePlan()
	if closed.Load() != 0 {
		t.Fatal("save closed the active RPC transport")
	}
	close(gate.release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if closed.Load() != 1 || a.push != nil || a.actionRunning {
		t.Fatal("push transport not released exactly once after return")
	}
}

func TestHistorySaveRejectsLatePushPlanAndClosesOnlyItsConnection(t *testing.T) {
	a, key, repo, gate, closed := peerReviewFixture(t)
	gate.armed.Store(true)
	result := make(chan error, 1)
	go func() { _, err := a.PushPlan(key, "box", "", OptsDTO{TargetDir: repo}); result <- err }()
	<-gate.entered
	if err := a.SetHistorySettings(config.History{LineageMB: 128}); err != nil {
		t.Fatal(err)
	}
	close(gate.release)
	if err := <-result; err != errReviewChanged {
		t.Fatalf("late push review accepted: %v", err)
	}
	if closed.Load() != 1 || a.push != nil {
		t.Fatal("newly rejected peer connection retained or published")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(repo)), ".claude", "projects", "*", "*.jsonl"))
	if len(files) != 0 {
		t.Fatalf("review wrote native destination work: %s", strings.Join(files, ","))
	}
}
