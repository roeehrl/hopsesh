// Package e2e tests the core with real agent modules, on this machine's filesystem.
package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

const sid = "0b6c6a8e-1d2f-4c3b-9a7e-5f4d3c2b1a01"

// resumes reports whether a start command (POSIX shell or PowerShell) resumes a session.
func resumes(command, id string) bool {
	return strings.Contains(command, "resume") && strings.Contains(command, id)
}

// jsonText is s as it appears inside a JSON string (a Windows path's backslashes doubled).
func jsonText(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// location is one place sessions live, simulated by its own folders on this machine.
type location struct {
	m    *host.Machine
	in   agent.Install
	repo string
}

func newLocation(t *testing.T, name, root string) location {
	t.Helper()
	home := filepath.Join(root, name)
	repo := filepath.Join(home, "git", "demo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	repo, _ = filepath.EvalSymlinks(repo)
	m := &host.Machine{Name: name, Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: home, Env: map[string]string{}, Binaries: map[string]agent.BinaryFact{}}}
	in := agent.Install{Agent: "claude", Version: "2.1.284", Roots: map[string]string{"home": filepath.Join(home, ".claude")}, Present: true}
	return location{m: m, in: in, repo: repo}
}

// seed copies the Claude fixtures into a location, pointing their paths at its repo.
func seed(t *testing.T, l location) {
	t.Helper()
	fix := "../../agents/claude/testdata/2.1.284"
	err := filepath.Walk(fix, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(fix, p)
		b, _ := os.ReadFile(p)
		b = []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", jsonText(l.repo)))
		rel = strings.ReplaceAll(rel, "-home-u-git-demo", claude.Slug(l.repo))
		dst := filepath.Join(l.in.Root("home"), rel)
		os.MkdirAll(filepath.Dir(dst), 0o700)
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func list(t *testing.T, l location) map[string]agent.Summary {
	t.Helper()
	m := claude.New()
	h, err := l.m.For(context.Background(), m.Spec(), l.in, nil)
	if err != nil {
		t.Fatal(err)
	}
	ls, err := m.List(context.Background(), h, l.in)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]agent.Summary{}
	for _, s := range ls.Sessions {
		out[string(s.Key.Session)] = s
	}
	return out
}

func input(t *testing.T, from, to location) move.Input {
	t.Helper()
	m := claude.New()
	s := list(t, from)[sid]
	lin, _ := lineage.Read(host.LocalFS(), s.Path)
	in := move.Input{Source: move.Side{Machine: from.m, Module: m, Install: from.in}, Session: s, Lineage: lin,
		Target: move.Side{Machine: to.m, Module: m, Install: to.in}}
	if c, ok := list(t, to)[sid]; ok {
		cl, _ := lineage.Read(host.LocalFS(), c.Path)
		in.Copies = []move.Copy{{Summary: c, Live: agent.LiveInfo{State: agent.Ended}, Lineage: cl}}
	}
	return in
}

func TestMoveRoundTripAndUndo(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seed(t, box)
	state := t.TempDir()
	env := move.Env{StateDir: state, Audit: nil}
	ctx := context.Background()

	// box → here
	in := input(t, box, here)
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 {
		t.Fatalf("blockers: %v", p.Blockers)
	}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	moved := list(t, here)[sid]
	if moved.CWD != here.repo || moved.Path == "" {
		t.Fatalf("moved copy: %+v", moved)
	}
	b, _ := os.ReadFile(moved.Path)
	// Signed thinking keeps its text (it may still name the old path); everything else moves.
	if strings.Count(string(b), jsonText(box.repo)) != 1 || !strings.Contains(string(b), `"thinking":"Read the file at `+jsonText(box.repo)) ||
		!strings.Contains(string(b), `"relocatedCwd":"`+jsonText(here.repo)+`"`) {
		t.Fatalf("paths not rewritten:\n%s", b)
	}
	if !strings.Contains(string(b), `"signature":"c2lnbmF0dXJlL2hvbWUvdS9naXQvZGVtbw=="`) {
		t.Fatal("a signature must stay byte-identical")
	}
	if strings.Contains(string(b), "bridge-session") {
		t.Fatal("the Remote Control bridge record must be dropped")
	}
	left := list(t, box)[sid]
	if left.LegacyLabel != nil || left.Title != moved.Title {
		t.Fatalf("the copy left behind keeps its title: %q %+v", left.Title, left.LegacyLabel)
	}
	for _, s := range []agent.Summary{moved, left} {
		m, err := lineage.Read(host.LocalFS(), s.Path)
		if err != nil || m == nil || len(m.Replicas) != 2 || len(m.Hops) != 1 || m.Replica(m.Replicas[0].ID).Head == "" || m.Replica(m.Replicas[1].ID).Head == "" {
			t.Fatalf("lineage beside %s: %+v %v", s.Path, m, err)
		}
		if s.Path == left.Path {
			hop, ok := m.Departed(m.Hops[0].From)
			if !ok || m.Replica(hop.To).Location != "here" {
				t.Fatalf("the lineage beside the copy left behind says where it went: %+v %v", hop, ok)
			}
		}
	}
	if !resumes(res.Command, sid) {
		t.Fatalf("command: %s", res.Command)
	}

	// Undo puts everything back.
	j, err := journal.Load(state, res.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Undo(ctx, journal.Files(func(string) (host.FS, error) { return host.LocalFS(), nil }), false); err != nil {
		t.Fatal(err)
	}
	if _, ok := list(t, here)[sid]; ok {
		t.Fatal("undo must remove the moved copy")
	}
	undone, e := lineage.Read(host.LocalFS(), left.Path)
	if e != nil || undone == nil || len(undone.Compensations) != 1 || undone.Journey().Transfers != 0 {
		t.Fatalf("undo must preserve the compensated journey: %+v %v", undone, e)
	}
	if _, moved := undone.Departed(undone.Hops[0].From); moved {
		t.Fatal("after undo the copy at box is no longer moved on")
	}

	// Again, then home: the copy at box was not touched, so it is simply replaced.
	in = input(t, box, here)
	p, _ = move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if _, err := move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	moved = list(t, here)[sid]
	appendTurn(t, moved.Path, "a turn added on here")
	back := input(t, here, box)
	p, err = move.Build(ctx, back, move.Options{TargetDir: box.repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 || p.Conflict != "" {
		t.Fatalf("an untouched copy at home must be replaced: %v %s", p.Blockers, p.Conflict)
	}
	if _, err := move.Apply(ctx, p, back, env); err != nil {
		t.Fatal(err)
	}
	home := list(t, box)[sid]
	b, _ = os.ReadFile(home.Path)
	if home.LegacyLabel != nil || !strings.Contains(string(b), "a turn added on here") || !strings.Contains(string(b), `"cwd":"`+jsonText(box.repo)) {
		t.Fatalf("home copy: label %+v\n%s", home.LegacyLabel, b)
	}

	// Both changed: refused unless the user chooses.
	appendTurn(t, home.Path, "work at home")
	appendTurn(t, list(t, here)[sid].Path, "work on here")
	conflict := input(t, here, box)
	p, _ = move.Build(ctx, conflict, move.Options{TargetDir: box.repo})
	if p.Conflict == "" || len(p.Blockers) == 0 {
		t.Fatalf("both copies changed: want a conflict, got %+v", p.Blockers)
	}
	p, _ = move.Build(ctx, conflict, move.Options{TargetDir: box.repo, Conflict: move.ConflictKeepBoth})
	if len(p.Blockers) > 0 || p.Placement.Key.Session == sid {
		t.Fatalf("keep-both: %v %s", p.Blockers, p.Placement.Key)
	}
	if _, err := move.Apply(ctx, p, conflict, env); err != nil {
		t.Fatal(err)
	}
	if n := len(list(t, box)); n != 3 {
		t.Fatalf("keep-both must leave both copies (and the fixture's other session): %d sessions", n)
	}
}

// appendTurn adds a user message chained to the current leaf, as Claude Code would.
func appendTurn(t *testing.T, file, text string) {
	t.Helper()
	b, _ := os.ReadFile(file)
	leaf := ""
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, `"uuid":"`); i >= 0 && (strings.Contains(line, `"type":"user"`) || strings.Contains(line, `"type":"assistant"`)) {
			leaf = strings.SplitN(line[i+8:], `"`, 2)[0]
		}
	}
	u := fmt.Sprintf("u-%x", sha256.Sum256([]byte(text)))
	first, _ := json.Marshal(map[string]any{"parentUuid": leaf, "isSidechain": false, "type": "user", "message": map[string]any{"role": "user", "content": text}, "uuid": u, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "sessionId": sid, "cwd": "x"})
	last, _ := json.Marshal(map[string]any{"type": "last-prompt", "lastPrompt": text, "leafUuid": u, "sessionId": sid})
	rec := string(first) + "\n" + string(last) + "\n"
	f, _ := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(rec)
	f.Close()
}

// A source checkout that could not be read is not "no repository": the move stops instead
// of placing the session where the source's path happens to exist here.
func TestUnreadCheckoutBlocks(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seed(t, box)
	ctx := context.Background()

	in := input(t, box, here)
	in.GitErr = "connection lost"
	p, err := move.Build(ctx, in, move.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) != 1 || !strings.Contains(p.Blockers[0], "could not read the session's git checkout on box (connection lost)") {
		t.Fatalf("blockers: %v", p.Blockers)
	}

	// Choosing the folder needs no checkout.
	p, err = move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) > 0 {
		t.Fatalf("blockers with --to: %v", p.Blockers)
	}
}
