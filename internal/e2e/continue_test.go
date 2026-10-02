package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func codexInstall(l location) agent.Install {
	return agent.Install{Agent: "codex", Version: "0.153.2", Roots: map[string]string{"home": filepath.Join(l.m.Facts.Home, ".codex")}, Present: true}
}

func listAgent(t *testing.T, l location, m agent.Module, in agent.Install) []agent.Summary {
	t.Helper()
	h, err := l.m.For(context.Background(), m.Spec(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	ls, err := m.List(context.Background(), h, in)
	if err != nil {
		t.Fatal(err)
	}
	return ls.Sessions
}

func readAll(t *testing.T, l location, m agent.Module, in agent.Install, s agent.Summary) ir.Segment {
	t.Helper()
	h, _ := l.m.For(context.Background(), m.Spec(), in, nil)
	seg, err := m.(agent.Reader).Read(context.Background(), h, in, s, ir.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	return seg
}

func mentions(seg ir.Segment, text string) bool {
	for _, n := range seg.Nodes {
		if strings.Contains(n.Text, text) || n.Tool != nil && n.Tool.Shell != nil && strings.Contains(n.Tool.Shell.Command, text) {
			return true
		}
	}
	return false
}

func TestContinueInCodexAndBack(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seed(t, box)
	env := move.Env{StateDir: t.TempDir()}
	ctx := context.Background()
	cl, cx := claude.New(), codex.New()
	hereCodex := codexInstall(here)
	os.MkdirAll(hereCodex.Root("home"), 0o700)

	// Claude Code on box → Codex here.
	src := list(t, box)[sid]
	in := move.Input{Source: move.Side{Machine: box.m, Module: cl, Install: box.in}, Session: src,
		Target: move.Side{Machine: here.m, Module: cx, Install: hereCodex}}
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo, Mark: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != move.KindContinue || p.Continue.Relation != move.RelationNew || len(p.Blockers) > 0 {
		t.Fatalf("plan: %s %s %v", p.Kind, p.Continue.Relation, p.Blockers)
	}
	if p.Continue.Report.Reasoning != 1 || p.Continue.Report.ToolCalls != 1 {
		t.Fatalf("report: %+v", p.Continue.Report)
	}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	threads := listAgent(t, here, cx, hereCodex)
	if len(threads) != 1 || threads[0].CWD != here.repo {
		t.Fatalf("codex threads: %+v", threads)
	}
	th := threads[0]
	seg := readAll(t, here, cx, hereCodex, th)
	if !mentions(seg, "PLUM-7") || !mentions(seg, "[prior agent · Claude Code · execute]") || !mentions(seg, "moved from Claude Code") {
		t.Fatalf("the Codex thread lacks the history or the briefing: %+v", seg.Nodes)
	}
	if mentions(seg, box.repo) {
		t.Fatal("paths must point here")
	}
	if !strings.Contains(res.Command, "codex resume "+string(th.Key.Session)) {
		t.Fatalf("command: %s", res.Command)
	}
	left := list(t, box)[sid]
	if left.Mark == nil || left.Mark.Kind != agent.MarkContinued || left.Mark.AgentName != "Codex" {
		t.Fatalf("the Claude session must say it continues in Codex: %+v", left.Mark)
	}
	original, _ := os.ReadFile(left.Path)

	// Codex works on.
	appendCodexTurn(t, th.Path, "Now also check the second file.", "The second codeword is FIG-3.")

	// Codex here → back to Claude Code on box: only the new work is added to the original.
	threads = listAgent(t, here, cx, hereCodex)
	lin, err := lineage.Read(host.LocalFS(), threads[0].Path)
	if err != nil || lin == nil {
		t.Fatalf("lineage beside the Codex thread: %v", err)
	}
	backIn := move.Input{Source: move.Side{Machine: here.m, Module: cx, Install: hereCodex}, Session: threads[0], Lineage: lin,
		Target: move.Side{Machine: box.m, Module: cl, Install: box.in}, Copies: []move.Copy{{Summary: list(t, box)[sid]}}}
	back, err := move.Build(ctx, backIn, move.Options{TargetDir: box.repo, Mark: true})
	if err != nil {
		t.Fatal(err)
	}
	if back.Continue.Relation != move.RelationAppend || len(back.Blockers) > 0 {
		t.Fatalf("return leg: %s %v %s", back.Continue.Relation, back.Blockers, back.Conflict)
	}
	if _, err := move.Apply(ctx, back, backIn, env); err != nil {
		t.Fatal(err)
	}
	home := list(t, box)[sid]
	now, _ := os.ReadFile(home.Path)
	if !strings.HasPrefix(string(now), string(original)) {
		t.Fatal("the original Claude transcript must stay byte for byte; only new records are added")
	}
	homeSeg := readAll(t, box, cl, box.in, home)
	if !mentions(homeSeg, "FIG-3") || !mentions(homeSeg, "Now also check the second file.") {
		t.Fatal("the Codex work must come back")
	}
	if home.Mark != nil || home.Title != "Find the codeword" {
		t.Fatalf("back home the session has its own title again: %q %+v", home.Title, home.Mark)
	}
	if mentions(homeSeg, "PLUM-7 (from") == false {
		t.Fatal("the original turns are still there")
	}

	// Nothing new on either side: refused.
	lin, _ = lineage.Read(host.LocalFS(), home.Path)
	again := move.Input{Source: move.Side{Machine: box.m, Module: cl, Install: box.in}, Session: home, Lineage: lin,
		Target: move.Side{Machine: here.m, Module: cx, Install: hereCodex}, Copies: []move.Copy{{Summary: listAgent(t, here, cx, hereCodex)[0]}}}
	p, _ = move.Build(ctx, again, move.Options{TargetDir: here.repo})
	if p.Continue.Relation != move.RelationSame || len(p.Blockers) == 0 {
		t.Fatalf("right after the round trip both copies are in step: %s %v", p.Continue.Relation, p.Blockers)
	}
}

// Continuing on another machine keeps the source agent's own copy there too, byte for
// byte and marked; coming back to that agent there adds only the new work to it.
func TestContinueKeepsNativeCopy(t *testing.T) {
	root := t.TempDir()
	box, here := newLocation(t, "box", root), newLocation(t, "here", root)
	seed(t, box)
	os.MkdirAll(here.in.Root("home"), 0o700)
	env := move.Env{StateDir: t.TempDir()}
	ctx := context.Background()
	cl, cx := claude.New(), codex.New()
	hereCodex := codexInstall(here)
	os.MkdirAll(hereCodex.Root("home"), 0o700)

	in := move.Input{Source: move.Side{Machine: box.m, Module: cl, Install: box.in}, Session: list(t, box)[sid],
		Target: move.Side{Machine: here.m, Module: cx, Install: hereCodex},
		Native: &move.NativeSide{Target: move.Side{Machine: here.m, Module: cl, Install: here.in}}}
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo, Mark: true})
	if err != nil || len(p.Blockers) > 0 {
		t.Fatalf("%v %v", err, p.Blockers)
	}
	if p.NativeCopy == nil || p.NativeCopy.Agent != "Claude Code" || p.NativeCopy.Key.Session != sid {
		t.Fatalf("the plan keeps the Claude session here too: %+v", p.NativeCopy)
	}
	if _, err := move.Apply(ctx, p, in, env); err != nil {
		t.Fatal(err)
	}
	native, ok := list(t, here)[sid]
	if !ok || native.CWD != here.repo || native.Mark == nil || native.Mark.Kind != agent.MarkContinued || native.Mark.AgentName != "Codex" {
		t.Fatalf("the native copy here: %+v %+v", native, native.Mark)
	}
	lin, _ := lineage.Read(host.LocalFS(), native.Path)
	if lin == nil || len(lin.Replicas) != 3 {
		t.Fatalf("lineage records the source, the Codex thread and the native copy: %+v", lin)
	}

	// Codex works on; then back to Claude Code here: the native copy gets only the new work.
	th := listAgent(t, here, cx, hereCodex)[0]
	appendCodexTurn(t, th.Path, "Now also check the second file.", "The second codeword is FIG-3.")
	th = listAgent(t, here, cx, hereCodex)[0]
	before, _ := os.ReadFile(native.Path)
	tl, _ := lineage.Read(host.LocalFS(), th.Path)
	backIn := move.Input{Source: move.Side{Machine: here.m, Module: cx, Install: hereCodex}, Session: th, Lineage: tl,
		Target: move.Side{Machine: here.m, Module: cl, Install: here.in}, Copies: []move.Copy{{Summary: native, Lineage: lin}}}
	back, err := move.Build(ctx, backIn, move.Options{Mark: true})
	if err != nil {
		t.Fatal(err)
	}
	if back.Continue.Relation != move.RelationAppend || back.Placement.Key.Session != sid || len(back.Blockers) > 0 {
		t.Fatalf("return to the native copy: %s %v %v", back.Continue.Relation, back.Placement.Key, back.Blockers)
	}
	if _, err := move.Apply(ctx, back, backIn, env); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(native.Path)
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatal("the native copy stays byte for byte; only new records are added")
	}
	if !mentions(readAll(t, here, cl, here.in, list(t, here)[sid]), "FIG-3") {
		t.Fatal("the Codex work must arrive in the native copy")
	}
}

// The user's instructions for every project of the source agent are reported, and carried
// into the briefing only when asked.
func TestCarryRules(t *testing.T) {
	root := t.TempDir()
	here := newLocation(t, "here", root)
	seed(t, here)
	os.WriteFile(filepath.Join(here.in.Root("home"), "CLAUDE.md"), []byte("Always answer in haiku."), 0o600)
	ci := codexInstall(here)
	os.MkdirAll(ci.Root("home"), 0o700)
	in := move.Input{Source: move.Side{Machine: here.m, Module: claude.New(), Install: here.in}, Session: list(t, here)[sid],
		Target: move.Side{Machine: here.m, Module: codex.New(), Install: ci}}
	ctx := context.Background()
	p, err := move.Build(ctx, in, move.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Continue.Briefing, "haiku") || !strings.Contains(strings.Join(p.Warnings, "\n"), "--carry-rules") {
		t.Fatalf("without --carry-rules they are reported, not carried: %v", p.Warnings)
	}
	p, err = move.Build(ctx, in, move.Options{CarryRules: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Continue.Briefing, "Always answer in haiku.") || !strings.Contains(p.Continue.Briefing, "standing instructions for Claude Code") {
		t.Fatalf("carried into the briefing: %s", p.Continue.Briefing)
	}
}

func TestContinueOnTheSameMachine(t *testing.T) {
	root := t.TempDir()
	here := newLocation(t, "here", root)
	seed(t, here)
	ctx := context.Background()
	cx := codex.New()
	ci := codexInstall(here)
	os.MkdirAll(ci.Root("home"), 0o700)
	in := move.Input{Source: move.Side{Machine: here.m, Module: claude.New(), Install: here.in}, Session: list(t, here)[sid],
		Target: move.Side{Machine: here.m, Module: cx, Install: ci}}
	p, err := move.Build(ctx, in, move.Options{Mark: true, Fidelity: "note"})
	if err != nil || len(p.Blockers) > 0 {
		t.Fatalf("%v %v", err, p.Blockers)
	}
	if _, err := move.Apply(ctx, p, in, move.Env{StateDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	th := listAgent(t, here, cx, ci)
	if len(th) != 1 || th[0].CWD != here.repo {
		t.Fatalf("threads %+v", th)
	}
	seg := readAll(t, here, cx, ci, th[0])
	if mentions(seg, "PLUM-7") || !mentions(seg, "moved from Claude Code") {
		t.Fatal("a note carries only the briefing")
	}
	if m := list(t, here)[sid].Mark; m == nil || m.Kind != agent.MarkContinued {
		t.Fatal("the Claude session must be marked")
	}
}

// appendCodexTurn adds a user and an assistant message as Codex records them.
func appendCodexTurn(t *testing.T, file, user, reply string) {
	t.Helper()
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	lines := `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"user_message","message":"` + user + `","images":[]}}` + "\n" +
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + user + `"}]}}` + "\n" +
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"reasoning","summary":[],"encrypted_content":"gAAA"}}` + "\n" +
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + reply + `"}]}}` + "\n"
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(lines)
	f.Close()
}
