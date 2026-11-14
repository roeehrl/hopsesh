//go:build !windows

package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/core/serve"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// hopsesh serve answers a peer from the login session and runs a new session in a
// terminal it owns, which another connection shows, types in and sees end.
func TestServeRunsTerminalsForAnotherMachine(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "hs")
	if err != nil {
		t.Fatal(err)
	}
	home, _ = filepath.EvalSymlinks(home)
	t.Cleanup(func() { os.RemoveAll(home) })
	bin := filepath.Join(home, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo '2.1.299 (Claude Code)'; exit 0; fi\necho \"started in $PWD\"\nIFS= read -r x\necho \"typed:$x\"\n"), 0o755)
	work := filepath.Join(home, "work")
	os.MkdirAll(work, 0o755)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(home, "state"))
	t.Setenv("HOPSESH_MACHINE", "studio")
	t.Setenv(host.LoginSessionEnv, "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, all.Registry(), config.StateDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- a.RunServe(ctx) }()
	t.Cleanup(func() { cancel(); <-served })
	for i := 0; serve.Running(ctx, home) == "" && i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
	}

	c, r, _, err := serve.Dial(ctx, home, serve.ModePeer)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	pc := peer.NewClient(r, c)
	var hr peer.HelloReply
	if err := pc.Call(ctx, peer.MethodHello, peer.Hello{Protocol: peer.Protocol, Min: peer.MinProtocol, From: "here"}, &hr); err != nil {
		t.Fatal(err)
	}
	if !hr.Login || hr.Protocol != peer.Protocol {
		t.Fatalf("not answered from the login session: %+v", hr)
	}
	var tab peer.Terminal
	if err := pc.Call(ctx, peer.MethodTerminalOpen, peer.TerminalRequest{Kind: peer.TerminalNew, Agent: "claude", Dir: work}, &tab); err != nil {
		t.Fatal(err)
	}
	tc, tr, _, err := serve.Dial(ctx, home, serve.ModeTerminal)
	if err != nil {
		t.Fatal(err)
	}
	out := &lockedBuf{}
	keys := &delayed{data: []byte("abc\n")}
	code, err := pty.Attach(serve.NewFrameConn(ctx, tr, tc, tc.Close), tab.ID, keys, out, nil, nil)
	if err != nil || code != 0 || !strings.Contains(out.String(), "typed:abc") || !strings.Contains(out.String(), "started in "+work) {
		t.Fatalf("code %d err %v output %q", code, err, out.String())
	}
	var tabs []peer.Terminal
	if err := pc.Call(ctx, peer.MethodTerminalList, struct{}{}, &tabs); err != nil || len(tabs) != 1 || tabs[0].State != string(pty.Exited) {
		t.Fatalf("list: %+v %v", tabs, err)
	}
}

// delayed gives its bytes once, after the tab is attached, then blocks.
type delayed struct {
	once sync.Once
	data []byte
}

func (d *delayed) Read(p []byte) (int, error) {
	n := 0
	d.once.Do(func() {
		time.Sleep(500 * time.Millisecond)
		n = copy(p, d.data)
	})
	if n > 0 {
		return n, nil
	}
	select {}
}

// A peer's request to open an OpenCode session, or start one, that would restart the
// background service here is refused with the code and the reason alone (the hint for an
// older hopsesh stays in the message); asked again with the person's yes, it opens
// (decision D2). The stand-in plays OpenCode, its service of another version. It calls
// OpenTerminal itself: hopsesh serve would put the login shell's PATH, where a real
// opencode may be, before the stand-in's.
func TestOpenTerminalAsksBeforeRestartingTheService(t *testing.T) {
	const service = "2.2.5" // the stand-in is 2.3.0
	if b, err := os.ReadFile("/Applications/OpenCode.app/Contents/Resources/opencode-cli.version"); err == nil && strings.TrimSpace(string(b)) == service {
		t.Skipf("OpenCode desktop here bundles opencode %s, which the module would pick", service)
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	testkit.UnsetAgentEnv(t)
	testkit.Setenv(t, testkit.Env(home))
	if err := testkit.DemoHome(home); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	os.MkdirAll(bin, 0o755)
	self, _ := os.Executable()
	if err := os.Symlink(self, filepath.Join(bin, "opencode")); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(home, "agents.log")
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("FAKE_AGENT_LOG", log)
	t.Setenv("FAKE_OPENCODE_SERVICE", "running")
	t.Setenv("FAKE_OPENCODE_SERVICE_VERSION", service)
	if p, err := exec.LookPath("opencode"); err != nil || filepath.Dir(p) != bin {
		t.Fatalf("opencode is %s, not the stand-in (%v)", p, err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, all.Registry(), config.StateDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	lg := &Login{tabs: map[string]peer.Terminal{}}
	lg.Terms = pty.NewManager(pty.Options{MaxTabs: 4, IdleAfter: -1, OnChange: lg.changed})
	t.Cleanup(lg.Terms.CloseAll)
	a.Login = lg
	ctx := context.Background()
	for _, req := range []peer.TerminalRequest{
		{Kind: peer.TerminalResume, Agent: "opencode", Session: testkit.DemoOpenCode[0]},
		{Kind: peer.TerminalNew, Agent: "opencode", Dir: home},
	} {
		_, err := a.OpenTerminal(ctx, req)
		var pe *peer.Error
		if !errors.As(err, &pe) || pe.Code != peer.CodeRestartsService || !errors.Is(err, agent.ErrRestartsService) ||
			strings.Contains(err.Error(), "too old") || !strings.Contains(pe.Message, "too old") || !strings.Contains(err.Error(), "runs "+service) {
			t.Fatalf("%s, not agreed: %v (%+v)", req.Kind, err, pe)
		}
		req.RestartService = true
		tab, err := a.OpenTerminal(ctx, req)
		if err != nil || tab.ID == "" {
			t.Fatalf("%s, agreed: %+v %v", req.Kind, tab, err)
		}
	}
	// The stand-in answered the probes and runs the agreed resume.
	var calls []byte
	for i := 0; i < 100 && !strings.Contains(string(calls), "-s "+testkit.DemoOpenCode[0]); i++ {
		time.Sleep(50 * time.Millisecond)
		calls, _ = os.ReadFile(log)
	}
	if !strings.Contains(string(calls), "service status") || !strings.Contains(string(calls), "-s "+testkit.DemoOpenCode[0]) {
		t.Fatalf("the stand-in did not run it:\n%s", calls)
	}
}
