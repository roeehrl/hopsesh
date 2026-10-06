package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/peer"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// machineHome is one simulated machine: a home folder with Claude Code (and an empty
// Codex), its own hopsesh configuration and state.
type machineHome struct {
	name, home, repo string
}

func newMachineHome(t *testing.T, root, name string, withSession bool) machineHome {
	t.Helper()
	m := machineHome{name: name, home: filepath.Join(root, name)}
	m.repo = filepath.Join(m.home, "git", "demo")
	os.MkdirAll(m.repo, 0o700)
	m.repo, _ = filepath.EvalSymlinks(m.repo)
	m.home, _ = filepath.EvalSymlinks(m.home)
	os.MkdirAll(filepath.Join(m.home, ".claude", "projects"), 0o700)
	os.MkdirAll(filepath.Join(m.home, ".codex", "sessions"), 0o700)
	if withSession {
		fix := "../../agents/claude/testdata/2.1.284"
		_ = filepath.Walk(fix, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(fix, p)
			// Fixture PIDs are not processes owned by this isolated test machine.
			if strings.HasPrefix(filepath.ToSlash(rel), "sessions/") {
				return nil
			}
			b, _ := os.ReadFile(p)
			b = []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", jsonText(m.repo)))
			rel = strings.ReplaceAll(rel, "-home-u-git-demo", claude.Slug(m.repo))
			dst := filepath.Join(m.home, ".claude", rel)
			os.MkdirAll(filepath.Dir(dst), 0o700)
			return os.WriteFile(dst, b, 0o600)
		})
	}
	return m
}

func (m machineHome) env() []string {
	return []string{"HOME=" + m.home, "USERPROFILE=" + m.home, "PATH=" + testPath(), "HOPSESH_MACHINE=" + m.name,
		"HOPSESH_CONFIG_DIR=" + filepath.Join(m.home, "config"), "HOPSESH_STATE_DIR=" + filepath.Join(m.home, "state"),
		"CLAUDE_CONFIG_DIR=", "CODEX_HOME="}
}

func (m machineHome) writeConfig(t *testing.T, c config.Config) {
	t.Helper()
	for _, kv := range m.env() {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	if err := config.Save(c); err != nil {
		t.Fatal(err)
	}
}

// buildHopsesh builds the command once per test run.
func buildHopsesh(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "hopsesh"+exeSuffix())
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/hopsesh")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// dialProcess runs `hopsesh peer --stdio` as the other machine.
func dialProcess(bin string, other machineHome) func(context.Context, config.Host) (*app.PeerConn, error) {
	return func(ctx context.Context, _ config.Host) (*app.PeerConn, error) {
		cmd := exec.Command(bin, "peer", "--stdio")
		cmd.Env = other.env()
		in, _ := cmd.StdinPipe()
		out, _ := cmd.StdoutPipe()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &app.PeerConn{Out: out, In: in, Stderr: stderr.String, Close: func() { in.Close(); _ = cmd.Wait() }}, nil
	}
}

// A push sends a Claude Code session from this machine to hopsesh on another, which
// installs it with its own module; this machine marks its copy, records the lineage in its
// own journal, and one undo here undoes both sides. A machine that does not receive
// refuses.
func TestPushToPeer(t *testing.T) {
	if testing.Short() {
		t.Skip("builds hopsesh")
	}
	bin := buildHopsesh(t)
	root := t.TempDir()
	box := newMachineHome(t, root, "box", false)
	here := newMachineHome(t, root, "here", true)

	recv := config.Defaults()
	recv.Peer.Receive = true
	recv.ReposDir = filepath.Join(box.home, "git")
	box.writeConfig(t, recv)

	cfg := config.Defaults()
	cfg.Hosts = []config.Host{{Name: "box", Destination: "box", Allowed: true}}
	here.writeConfig(t, cfg) // also points this process at "here"

	a := app.New(cfg, all.Registry(), filepath.Join(here.home, "state"), nil)
	a.PeerDial = dialProcess(bin, box)
	ctx := context.Background()
	inv := a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
	e, err := inv.Find(app.ParseRef(sid))
	if err != nil {
		t.Fatal(err)
	}

	p, err := a.StartPush(ctx, inv, e, cfg.Hosts[0], "", move.Options{TargetDir: box.repo, Mark: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Plan.Kind != move.KindContinue || len(p.Plan.Blockers) > 0 || p.Plan.Target.Location != "box" || p.Plan.Target.CWD != box.repo {
		t.Fatalf("plan: %+v", p.Plan)
	}
	res, err := p.Commit(ctx)
	p.Close()
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(box.home, ".claude", "projects", claude.Slug(box.repo), string(p.Plan.Placement.Key.Session)+".jsonl")
	b, err := os.ReadFile(moved)
	// (signed reasoning keeps its text, paths included: it is never rewritten)
	if err != nil || !strings.Contains(string(b), `"cwd":"`+jsonText(box.repo)) || strings.Contains(string(b), `"cwd":"`+jsonText(here.repo)) {
		t.Fatalf("the session is on box with box's paths: %v\n%s", err, b)
	}
	if res.Result.Mark != "done" {
		t.Fatalf("mark: %+v", res.Result)
	}
	inv.Close()
	inv = a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
	e, _ = inv.Find(app.ParseRef(sid))
	if e.Session.Mark == nil || e.Session.Mark.Location != "box" || e.Lineage == nil || len(e.Lineage.Hops) != 1 {
		t.Fatalf("the copy here is marked and carries the lineage: %+v %+v", e.Session.Mark, e.Lineage)
	}

	// One undo here undoes both machines.
	if _, err := a.Undo(ctx, res.Journal, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Fatal("undo must remove the copy on box")
	}
	inv.Close()
	inv = a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
	if e, _ = inv.Find(app.ParseRef(sid)); e.Session.Mark != nil {
		t.Fatalf("undo must remove the mark here: %+v", e.Session.Mark)
	}

	// Continue in Codex on box.
	p, err = a.StartPush(ctx, inv, e, cfg.Hosts[0], "codex", move.Options{TargetDir: box.repo, Mark: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Plan.Kind != move.KindContinue || len(p.Plan.Blockers) > 0 {
		t.Fatalf("continue plan: %+v", p.Plan)
	}
	if _, err := p.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	p.Close()
	rollouts, _ := filepath.Glob(filepath.Join(box.home, ".codex", "sessions", "*", "*", "*", "rollout-*.jsonl"))
	if len(rollouts) != 1 {
		t.Fatalf("a Codex thread on box: %v", rollouts)
	}
	inv.Close()

	// A machine that does not receive refuses.
	recv.Peer.Receive = false
	box.writeConfig(t, recv)
	here.writeConfig(t, cfg)
	inv = a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
	defer inv.Close()
	e, _ = inv.Find(app.ParseRef(sid))
	if _, err := a.StartPush(ctx, inv, e, cfg.Hosts[0], "", move.Options{TargetDir: box.repo}); !errors.Is(err, peer.ErrRefused) {
		t.Fatalf("refused: %v", err)
	}
}

// testPath keeps git but no agent binaries: the system folders only (on Windows, as is).
func testPath() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("PATH")
	}
	return "/usr/bin:/bin"
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// The receiving process resolves the explicit profile ID in its own registry. A
// sender cannot substitute a path or cause writes to the receiver's default root.
func TestAccountProfilePeerDestinations(t *testing.T) {
	if testing.Short() {
		t.Skip("builds hopsesh")
	}
	bin := buildHopsesh(t)
	root := t.TempDir()
	box := newMachineHome(t, root, "box", false)
	here := newMachineHome(t, root, "here", true)
	recv := config.Defaults()
	recv.Peer.Receive = true
	recv.ReposDir = filepath.Join(box.home, "git")
	box.writeConfig(t, recv)
	profiles := map[string]agent.RuntimeProfile{}
	for _, id := range []string{"claude", "codex"} {
		c := exec.Command(bin, "accounts", "add", id, "Second personal", "--tag", "Personal", "--json")
		c.Env = box.env()
		b, err := c.Output()
		if err != nil {
			t.Fatal(err)
		}
		var p agent.RuntimeProfile
		if err = json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		profiles[id] = p
	}
	cfg := config.Defaults()
	cfg.Hosts = []config.Host{{Name: "box", Destination: "box", Allowed: true}}
	here.writeConfig(t, cfg)
	a := app.New(cfg, all.Registry(), filepath.Join(here.home, "state"), nil)
	a.PeerDial = dialProcess(bin, box)
	ctx := context.Background()
	for _, id := range []string{"claude", "codex"} {
		t.Run(id, func(t *testing.T) {
			inv := a.Scan(ctx, app.ScanOptions{Hosts: []string{"here"}})
			defer inv.Close()
			e, err := inv.Find(app.ParseRef(sid))
			if err != nil {
				t.Fatal(err)
			}
			target := profiles[id]
			push, err := a.StartPush(ctx, inv, e, cfg.Hosts[0], agent.ID(id), move.Options{TargetDir: box.repo, TargetProfile: target.ID, Mark: true})
			if err != nil {
				t.Fatal(err)
			}
			defer push.Close()
			if push.Plan.Placement.Key.Profile != target.ID || len(push.Plan.Blockers) > 0 {
				t.Fatalf("wrong destination: %+v", push.Plan)
			}
			res, err := push.Commit(ctx)
			if err != nil {
				t.Fatal(err)
			}
			c := exec.Command(bin, "ls", "--json")
			c.Env = box.env()
			b, err := c.Output()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), target.ID) || !strings.Contains(string(b), string(push.Plan.Placement.Key.Session)) {
				t.Fatalf("receiver did not list profile copy: %s", b)
			}
			var native []string
			err = filepath.Walk(target.Root, func(path string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() && strings.HasSuffix(path, ".jsonl") {
					native = append(native, path)
				}
				return err
			})
			if err != nil || len(native) == 0 {
				t.Fatal("receiver wrote no native conversation in selected root", err)
			}
			if _, err = a.Undo(ctx, res.Journal, false); err != nil {
				t.Fatal(err)
			}
			for _, path := range native {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("undo retained transferred native file", path)
				}
			}
		})
	}
}
