package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A test-owned process stands in for a desktop child that survives closing its view.
func TestMovementEndProcessHelper(t *testing.T) {
	ready := os.Getenv("HOPSESH_END_TEST_READY")
	if ready == "" {
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	defer signal.Stop(ch)
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	<-ch
}

func registeredEndProcess(t *testing.T, root string, id agent.SessionID) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(exe, "-test.run=^TestMovementEndProcessHelper$")
	cmd.Env = append(os.Environ(), "HOPSESH_END_TEST_READY="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
			_ = cmd.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("test process did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, _ := json.Marshal(map[string]any{"pid": cmd.Process.Pid, "sessionId": id, "entrypoint": "claude-desktop", "status": "idle"})
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(cmd.Process.Pid)+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// Runs in the mandatory Linux/macOS/Windows movement matrix. Windows asserts the
// unsupported action is withheld; POSIX runs the real Claude module's graceful stop.
func TestMovementEndOriginalBeforeAgentReturn(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a, b := newLocation(t, "a", root), newLocation(t, "b", root)
	seed(t, a)
	cl, cx := claude.New(), codex.New()
	cxi := codexInstall(b)
	if err := os.MkdirAll(cxi.Root("home"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := list(t, a)[sid]
	env := move.Env{StateDir: t.TempDir()}
	in := move.Input{Source: move.Side{Machine: a.m, Module: cl, Install: a.in}, Session: s, Target: move.Side{Machine: b.m, Module: cx, Install: cxi}}
	p, err := move.Build(ctx, in, move.Options{TargetDir: b.repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	thread := listAgent(t, b, cx, cxi)[0]
	appendCodexTurn(t, thread.Path, "Return work", "END-RETURN-WORK")
	thread = listAgent(t, b, cx, cxi)[0]
	lin, err := lineage.Read(host.LocalFS(), thread.Path)
	if err != nil {
		t.Fatal(err)
	}
	original := list(t, a)[sid]
	before, err := os.ReadFile(original.Path)
	if err != nil {
		t.Fatal(err)
	}
	live := agent.LiveInfo{State: agent.Live}
	var unrelated int
	if runtime.GOOS != "windows" {
		registeredEndProcess(t, a.in.Root("home"), original.Key.Session)
		registeredEndProcess(t, a.in.Root("home"), original.Key.Session)
		unrelated = registeredEndProcess(t, a.in.Root("home"), "another-conversation")
		h, _ := a.m.For(ctx, cl.Spec(), a.in, nil)
		ls, e := cl.Live(ctx, h, a.in, []agent.SessionID{original.Key.Session})
		if e != nil {
			t.Fatal(e)
		}
		live = ls[original.Key.Session]
		if len(live.Procs) != 2 || !live.App {
			t.Fatalf("desktop idle original: %+v", live)
		}
	}
	backIn := move.Input{Source: move.Side{Machine: b.m, Module: cx, Install: cxi}, Session: thread, Lineage: lin, Target: move.Side{Machine: a.m, Module: cl, Install: a.in}, Copies: []move.Copy{{Summary: original, Live: live}}}
	opts := move.Options{TargetDir: a.repo, TargetSession: original.Key.String()}
	back, err := move.Build(ctx, backIn, opts)
	if err != nil {
		t.Fatal(err)
	}
	if back.Continue.Relation != move.RelationAppend || len(back.Blockers) == 0 {
		t.Fatalf("open original did not block: %+v", back.Continue)
	}
	if _, err = move.Apply(ctx, back, backIn, env); err == nil {
		t.Fatal("append ran while original was live")
	}
	if runtime.GOOS == "windows" {
		if move.CanEndDestination(back, backIn) || move.EndDestination(ctx, back, backIn) == nil {
			t.Fatal("unsupported Windows stop was offered")
		}
		return
	}
	if err = move.EndDestination(ctx, back, backIn); err != nil {
		t.Fatal(err)
	}
	alive, err := a.m.Procs().Alive(ctx, []int{unrelated})
	if err != nil || !alive[unrelated] {
		t.Fatalf("another conversation affected: %v %v", alive, err)
	}
	after, err := os.ReadFile(original.Path)
	if err != nil || string(after) != string(before) {
		t.Fatal("end action changed the saved original")
	}
	backIn.Copies[0].Live = agent.LiveInfo{State: agent.Ended}
	fresh, err := move.Build(ctx, backIn, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Blockers) != 0 || fresh.Continue.AppendTo.Key != original.Key {
		t.Fatalf("fresh review did not reuse original: %v", fresh.Blockers)
	}
	if _, err = move.Apply(ctx, fresh, backIn, env); err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(original.Path)
	if err != nil || !strings.HasPrefix(string(after), string(before)) || !mentions(readAll(t, a, cl, a.in, list(t, a)[sid]), "END-RETURN-WORK") {
		t.Fatal("new work did not append to the same original with history preserved")
	}
}
