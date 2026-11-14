package e2e

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/agents/opencode"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/internal/testkit/fakeopencode"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Sessions continue between Claude Code or Codex and OpenCode: hopsesh writes OpenCode's
// own transfer and has OpenCode's program (here the stand-in 2.3.0) import it, and reads
// OpenCode's sessions back for the other agents. OpenCode cannot add to a session, so a
// return to one continued there before starts a new session that continues it.

// codexThread is the Codex fixture's thread.
const codexThread agent.SessionID = "01a0fe1c-0000-7000-8000-000000000001"

// ocTurn continues an OpenCode session as a prompt and its reply in OpenCode do: two
// messages, and the session's update time.
func ocTurn(t *testing.T, l location, sid agent.SessionID, prompt, reply string) {
	t.Helper()
	useOpenCode(t, l, sid, prompt)
	db, err := fakeopencode.Open(ocDB(l))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UnixMilli()
	id := "msg_ffffffffffff" + strings.Repeat("a", 10) + time.Now().Format("150405.000")[7:]
	if _, err := db.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data)
		VALUES (?, ?, 'assistant', (SELECT max(seq) + 1 FROM session_message WHERE session_id = ?), ?, ?,
		json_object('time', json_object('created', ?, 'completed', ?), 'agent', 'build', 'model', json_object('id', 'qwen', 'providerID', 'omlx'),
			'content', json_array(json_object('type', 'text', 'text', ?)), 'finish', 'stop'))`,
		id, string(sid), string(sid), now, now, now, now, reply); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE session_v2 SET time_updated = max(time_updated + 1, ?) WHERE id = ?`, now, string(sid)); err != nil {
		t.Fatal(err)
	}
}

// ocOnly is the one OpenCode session at a location.
func ocOnly(t *testing.T, l location, m *opencode.Module) agent.Summary {
	t.Helper()
	all := ocSessions(t, l, m)
	if len(all) != 1 {
		t.Fatalf("OpenCode sessions at %s: %+v", l.m.Name, all)
	}
	for _, s := range all {
		return s
	}
	return agent.Summary{}
}

// resumesOC reports whether a result resumes an OpenCode session with a runnable command.
func resumesOC(res *move.Result, id agent.SessionID) bool {
	return len(res.Run.Argv) == 3 && filepath.Base(res.Run.Argv[0]) == "opencode" && slices.Equal(res.Run.Argv[1:], []string{"-s", string(id)}) &&
		strings.Contains(res.Command, string(id))
}

// A Claude Code session continues in OpenCode and comes back: OpenCode imports the written
// session under the id hopsesh minted (once, on a private server with the service
// stopped), it reads as the conversation with hopsesh's briefing and paths of this
// machine, the resume is checked here, and its new work comes back to the original Claude
// session as an addition.
func TestContinueClaudeInOpenCodeAndBack(t *testing.T) {
	log, box, here := ocWorld(t)
	seed(t, box)
	hereOC := ocInstall(here)
	os.MkdirAll(hereOC.Root("data"), 0o700)
	oc, cl := opencode.New(), claude.New()
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}

	src := list(t, box)[sid]
	in := move.Input{Source: move.Side{Machine: box.m, Module: cl, Install: box.in}, Session: src,
		Target: move.Side{Machine: here.m, Module: oc, Install: hereOC}}
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != move.KindContinue || p.Continue.Relation != move.RelationNew || len(p.Blockers) > 0 || !strings.HasPrefix(string(p.Placement.Key.Session), "ses_") {
		t.Fatalf("plan: %s %s %v %s", p.Kind, p.Continue.Relation, p.Blockers, p.Placement.Key.Session)
	}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	s := ocOnly(t, here, oc)
	if s.Key.Session != p.Placement.Key.Session || s.CWD != here.repo || s.Title != src.Title || s.TitleSource != "custom" {
		t.Fatalf("the OpenCode session: %+v", s)
	}
	imports := calls(t, log, "session import")
	if len(imports) != 1 || !strings.Contains(imports[0], "--directory "+here.repo+" --standalone") {
		t.Fatalf("imports: %q", imports)
	}
	if !resumesOC(res, s.Key.Session) {
		t.Fatalf("resume: %+v %s", res.Run, res.Command)
	}
	seg := readAll(t, here, oc, hereOC, s)
	if !mentions(seg, "PLUM-7") || !mentions(seg, "moved from Claude Code") || mentions(seg, box.repo) {
		t.Fatalf("the OpenCode session lacks the history or the briefing, or names the other machine's folder: %+v", seg.Nodes)
	}
	if exp, err := fakeopencode.Export(ocDB(here), string(s.Key.Session)); err != nil || !strings.Contains(string(exp), `"providerID": "hopsesh"`) {
		t.Fatalf("OpenCode's export of it: %v", err)
	}
	lin, err := lineage.Read(host.LocalFS(), s.Path)
	if err != nil || lin == nil || len(lin.ActiveHops()) != 1 {
		t.Fatalf("lineage beside the anchor: %v %v", lin, err)
	}

	// OpenCode works on; back to Claude Code on box: only the new work is added.
	ocTurn(t, here, s.Key.Session, "Now also check the second file.", "The second codeword is FIG-3.")
	s = ocOnly(t, here, oc)
	lin, _ = lineage.Read(host.LocalFS(), s.Path)
	before, _ := os.ReadFile(src.Path)
	backIn := move.Input{Source: move.Side{Machine: here.m, Module: oc, Install: hereOC}, Session: s, Lineage: lin,
		Target: move.Side{Machine: box.m, Module: cl, Install: box.in}, Copies: []move.Copy{{Summary: list(t, box)[sid]}}}
	back, err := move.Build(ctx, backIn, move.Options{TargetDir: box.repo})
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
	if !strings.HasPrefix(string(now), string(before)) || len(now) == len(before) {
		t.Fatal("the original Claude transcript must stay byte for byte; only new records are added")
	}
	homeSeg := readAll(t, box, cl, box.in, home)
	// hopsesh's own notices in OpenCode (the briefing and "Import ready" it wrote there)
	// do not come back as conversation.
	if !mentions(homeSeg, "FIG-3") || !mentions(homeSeg, "Now also check the second file.") || mentions(homeSeg, "not by OpenCode") || mentions(homeSeg, "moved from Claude Code") {
		t.Fatal("the OpenCode work must come back, without hopsesh's notices from the first leg")
	}
	if n := len(calls(t, log, "session import")); n != 1 {
		t.Fatalf("%d imports", n)
	}
}

// A Codex session continues in OpenCode and comes back the same way; worked on again in
// Codex, it continues in OpenCode once more as a new session that continues the first
// (OpenCode cannot add to a session), which is left as it was.
func TestContinueCodexInOpenCodeAndBack(t *testing.T) {
	log, box, here := ocWorld(t)
	boxCX := seedCodex(t, box)
	hereOC := ocInstall(here)
	os.MkdirAll(hereOC.Root("data"), 0o700)
	oc, cx := opencode.New(), codex.New()
	ctx := context.Background()
	env := move.Env{StateDir: t.TempDir()}
	thread := func() agent.Summary {
		for _, x := range listAgent(t, box, cx, boxCX) {
			if x.Key.Session == codexThread {
				return x
			}
		}
		t.Fatal("the Codex thread is not listed")
		return agent.Summary{}
	}

	src := thread()
	in := move.Input{Source: move.Side{Machine: box.m, Module: cx, Install: boxCX}, Session: src,
		Target: move.Side{Machine: here.m, Module: oc, Install: hereOC}}
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
	if err != nil || len(p.Blockers) > 0 {
		t.Fatalf("plan: %v %v", err, p.Blockers)
	}
	res, err := move.Apply(ctx, p, in, env)
	if err != nil {
		t.Fatal(err)
	}
	first := ocOnly(t, here, oc)
	if !resumesOC(res, first.Key.Session) || !mentions(readAll(t, here, oc, hereOC, first), "moved from Codex") {
		t.Fatalf("continued in OpenCode: %s", res.Command)
	}

	// Back to Codex: added to the thread.
	ocTurn(t, here, first.Key.Session, "Run the tests too.", "All tests pass.")
	first = ocOnly(t, here, oc)
	lin, _ := lineage.Read(host.LocalFS(), first.Path)
	backIn := move.Input{Source: move.Side{Machine: here.m, Module: oc, Install: hereOC}, Session: first, Lineage: lin,
		Target: move.Side{Machine: box.m, Module: cx, Install: boxCX}, Copies: []move.Copy{{Summary: thread()}}}
	back, err := move.Build(ctx, backIn, move.Options{TargetDir: box.repo})
	if err != nil || back.Continue.Relation != move.RelationAppend || len(back.Blockers) > 0 {
		t.Fatalf("return leg: %v %s %v", err, back.Continue.Relation, back.Blockers)
	}
	if _, err := move.Apply(ctx, back, backIn, env); err != nil {
		t.Fatal(err)
	}
	if seg := readAll(t, box, cx, boxCX, thread()); !mentions(seg, "All tests pass.") || !mentions(seg, "Run the tests too.") {
		t.Fatal("the OpenCode work must come back to Codex")
	}

	// Codex works on; continued in OpenCode again: a new session continues the first.
	appendCodexTurn(t, thread().Path, "Now release it.", "Released v1.2.")
	src = thread()
	lin, _ = lineage.Read(host.LocalFS(), src.Path)
	stamp, err := oc.SessionStamp(ctx, mustHost(t, here, oc, hereOC), hereOC, first.Key.Session)
	if err != nil {
		t.Fatal(err)
	}
	again := move.Input{Source: move.Side{Machine: box.m, Module: cx, Install: boxCX}, Session: src, Lineage: lin,
		Target: move.Side{Machine: here.m, Module: oc, Install: hereOC}, Copies: []move.Copy{{Summary: first, Lineage: readLineage(first.Path)}}}
	p, err = move.Build(ctx, again, move.Options{TargetDir: here.repo})
	if err != nil {
		t.Fatal(err)
	}
	if p.Continue.Relation != move.RelationNew || p.Continue.Rollover == nil || p.Continue.Rollover.Key.Session != first.Key.Session || len(p.Blockers) > 0 ||
		!slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "OpenCode cannot add to an existing session") }) {
		t.Fatalf("plan: %s %+v %v %q", p.Continue.Relation, p.Continue.Rollover, p.Blockers, p.Warnings)
	}
	res, err = move.Apply(ctx, p, again, env)
	if err != nil {
		t.Fatal(err)
	}
	all := ocSessions(t, here, oc)
	second, ok := all[p.Placement.Key.Session]
	if len(all) != 2 || !ok || !resumesOC(res, second.Key.Session) {
		t.Fatalf("OpenCode sessions: %+v", all)
	}
	seg := readAll(t, here, oc, hereOC, second)
	if !mentions(seg, "Released v1.2.") || !mentions(seg, "All tests pass.") {
		t.Fatal("the new session must carry the whole conversation")
	}
	if after, err := oc.SessionStamp(ctx, mustHost(t, here, oc, hereOC), hereOC, first.Key.Session); err != nil || after != stamp {
		t.Fatalf("the first OpenCode session changed: %v", err)
	}
	g, err := lineage.Read(host.LocalFS(), second.Path)
	if err != nil || g == nil {
		t.Fatal(err)
	}
	rolled := false
	for _, h := range g.ActiveHops() {
		rolled = rolled || h.Rollover != nil && g.Replica(h.Rollover.Replica).Key.Session == first.Key.Session
	}
	if !rolled {
		t.Fatal("the new session's lineage does not say it continues the first")
	}
	if n := len(calls(t, log, "session import")); n != 2 {
		t.Fatalf("%d imports", n)
	}
	if len(calls(t, log, "session delete")) != 0 {
		t.Fatal("a session was deleted")
	}
}

func mustHost(t *testing.T, l location, m agent.Module, in agent.Install) agent.Host {
	t.Helper()
	h, err := l.m.For(context.Background(), m.Spec(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func readLineage(path string) *lineage.Manifest {
	g, _ := lineage.Read(host.LocalFS(), path)
	return g
}

// Undo of a continuation into OpenCode deletes the session OpenCode imported, at the
// stamp the write left it, and everything hopsesh placed beside it; once it was used, an
// unforced undo refuses and leaves it.
func TestUndoContinueIntoOpenCode(t *testing.T) {
	log, box, here := ocWorld(t)
	seed(t, box)
	hereOC := ocInstall(here)
	os.MkdirAll(hereOC.Root("data"), 0o700)
	oc := opencode.New()
	ctx := context.Background()
	state := t.TempDir()
	in := move.Input{Source: move.Side{Machine: box.m, Module: claude.New(), Install: box.in}, Session: list(t, box)[sid],
		Target: move.Side{Machine: here.m, Module: oc, Install: hereOC}}
	reach := ocReach(oc, here)

	for _, used := range []bool{false, true} {
		p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
		if err != nil || len(p.Blockers) > 0 {
			t.Fatalf("plan: %v %v", err, p.Blockers)
		}
		res, err := move.Apply(ctx, p, in, move.Env{StateDir: state})
		if err != nil {
			t.Fatal(err)
		}
		made := p.Placement.Key.Session
		j, err := journal.Load(state, res.Journal)
		if err != nil {
			t.Fatal(err)
		}
		if v := vendorEntries(j); len(v) != 1 || !v[0].Created || v[0].Key.Session != made || v[0].Stamp == "" {
			t.Fatalf("the written session in the journal: %+v", v)
		}
		if used {
			ocTurn(t, here, made, "Keep going.", "Done.")
			if err := j.Undo(ctx, reach, false); !errors.Is(err, journal.ErrChanged) {
				t.Fatalf("undo over later work: %v", err)
			}
			if !sessionThere(t, here, made) {
				t.Fatal("an unforced undo deleted a used session")
			}
			if err := j.Undo(ctx, reach, true); err != nil {
				t.Fatal(err)
			}
		} else if err := j.Undo(ctx, reach, false); err != nil {
			t.Fatal(err)
		}
		if sessionThere(t, here, made) {
			t.Fatal("undo left the session")
		}
		if _, err := os.Stat(filepath.Join(hereOC.Root("data"), agent.AnchorRel("opencode", made))); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("undo left the anchor")
		}
		// Nor its lineage, its title, its archive or the transfer file in tmp/.
		var left []string
		_ = filepath.WalkDir(filepath.Join(hereOC.Root("data"), "hopsesh"), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				left = append(left, p)
			}
			return nil
		})
		if len(left) > 0 {
			t.Fatalf("undo left %q", left)
		}
	}
	if n := len(calls(t, log, "session delete")); n != 2 {
		t.Fatalf("%d deletes", n)
	}
}

// Interrupted after OpenCode imported the written session, a continuation recovers by that
// session: no second import, the same command, the session journaled at the write's
// stamp and lineage beside it. Interrupted before the import, the recovery imports it.
func TestContinueIntoOpenCodeRecovery(t *testing.T) {
	for _, stage := range []string{"native-written", "before-import", "unjournaled"} {
		t.Run(stage, func(t *testing.T) {
			log, box, here := ocWorld(t)
			seed(t, box)
			hereOC := ocInstall(here)
			os.MkdirAll(hereOC.Root("data"), 0o700)
			oc := opencode.New()
			ctx := context.Background()
			in := move.Input{Source: move.Side{Machine: box.m, Module: claude.New(), Install: box.in}, Session: list(t, box)[sid],
				Target: move.Side{Machine: here.m, Module: oc, Install: hereOC}}
			opt := move.Options{TargetDir: here.repo, OperationID: "continue-oc-recovery"}
			p, err := move.Build(ctx, in, opt)
			if err != nil {
				t.Fatal(err)
			}
			made := p.Placement.Key.Session
			env := move.Env{StateDir: t.TempDir(), Failpoint: func(s string) error {
				if s == "native-written" {
					return errors.New("simulated process exit")
				}
				return nil
			}}
			if stage != "native-written" {
				// The program dies at the import: the session is placed, not imported.
				t.Setenv("FAKE_OPENCODE_FAIL", "session import")
			}
			failed, err := move.Apply(ctx, p, in, env)
			if err == nil {
				t.Fatal("the fault did not interrupt the continuation")
			}
			if stage == "unjournaled" {
				// As if the run had stopped after saving its request and before journaling
				// the session: the recovery journals it before it imports it.
				j, err := journal.Load(env.StateDir, failed.Journal)
				if err != nil {
					t.Fatal(err)
				}
				v := vendorEntries(j)
				if len(v) != 1 {
					t.Fatalf("the session in the journal: %+v", v)
				}
				if err := j.ForgetSession(v[0].Machine, *v[0].Key, true); err != nil || len(vendorEntries(j)) != 0 {
					t.Fatalf("forgetting it: %v", err)
				}
			}
			if _, err := os.Stat(filepath.Join(hereOC.Root("data"), agent.AnchorRel("opencode", made))); err != nil {
				t.Fatalf("the written session is not placed: %v", err)
			}
			if there := sessionThere(t, here, made); there != (stage == "native-written") {
				t.Fatalf("imported before the fault: %t", there)
			}
			t.Setenv("FAKE_OPENCODE_FAIL", "")
			env.Failpoint = nil
			if p, err = move.Build(ctx, in, opt); err != nil {
				t.Fatal(err)
			}
			res, err := move.Apply(ctx, p, in, env)
			if err != nil {
				t.Fatal(err)
			}
			wantImports := 1
			if stage != "native-written" {
				wantImports = 2 // the failed one, and the recovery's
			}
			if n := len(calls(t, log, "session import")); n != wantImports || !sessionThere(t, here, made) || !resumesOC(res, made) {
				t.Fatalf("recovered: %d imports, resumes with %s (made %s)", n, res.Command, made)
			}
			j, err := journal.Load(env.StateDir, res.Journal)
			if err != nil {
				t.Fatal(err)
			}
			if v := vendorEntries(j); len(v) != 1 || v[0].Key.Session != made || v[0].Stamp == "" {
				t.Fatalf("the session in the journal: %+v", v)
			}
			s := ocOnly(t, here, oc)
			if g, err := lineage.Read(host.LocalFS(), s.Path); err != nil || g == nil || len(g.ActiveHops()) != 1 {
				t.Fatalf("recovered lineage: %v %v", g, err)
			}
			if err := j.Undo(ctx, ocReach(oc, here), false); err != nil {
				t.Fatal(err)
			}
			if sessionThere(t, here, made) {
				t.Fatal("undo left the session")
			}
		})
	}
}

// The resume command of every arrival in OpenCode is checked on the target: with a service
// of another version running (D2), a continuation, a second continuation with no new
// work, and a recovered one each leave the command shown but not runnable; a recovered
// continuation asked to go on keeps its prompt. The service's version is one no desktop
// app ships, so no resume program of that version is found (desktopCLIs also looks in
// /Applications).
func TestContinueIntoOpenCodeChecksResume(t *testing.T) {
	refused := func(res *move.Result) bool {
		return len(res.Run.Argv) == 0 && res.Command != "" && slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "before you resume it") })
	}
	setup := func(t *testing.T) (location, location, agent.Install, move.Input) {
		_, box, here := ocWorld(t)
		seed(t, box)
		hereOC := ocInstall(here)
		os.MkdirAll(hereOC.Root("data"), 0o700)
		in := move.Input{Source: move.Side{Machine: box.m, Module: claude.New(), Install: box.in}, Session: list(t, box)[sid],
			Target: move.Side{Machine: here.m, Module: opencode.New(), Install: hereOC}}
		return box, here, hereOC, in
	}
	ctx := context.Background()
	t.Run("continue and no work", func(t *testing.T) {
		box, here, _, in := setup(t)
		t.Setenv("FAKE_OPENCODE_SERVICE", "running")
		t.Setenv("FAKE_OPENCODE_SERVICE_VERSION", "2.1.5")
		p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo})
		if err != nil || len(p.Blockers) > 0 {
			t.Fatalf("plan: %v %v", err, p.Blockers)
		}
		res, err := move.Apply(ctx, p, in, move.Env{StateDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if !refused(res) {
			t.Fatalf("the continuation's resume: %v %q", res.Run.Argv, res.Warnings)
		}
		oc := in.Target.Module.(*opencode.Module)
		s := ocOnly(t, here, oc)
		lin, _ := lineage.Read(host.LocalFS(), list(t, box)[sid].Path)
		again := move.Input{Source: in.Source, Session: list(t, box)[sid], Lineage: lin, Target: in.Target,
			Copies: []move.Copy{{Summary: s, Lineage: readLineage(s.Path)}}}
		p, err = move.Build(ctx, again, move.Options{TargetDir: here.repo})
		if err != nil || !p.NoWork {
			t.Fatalf("again: %v, no work %t", err, p.NoWork)
		}
		if res, err = move.Apply(ctx, p, again, move.Env{StateDir: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
		if !refused(res) || len(ocSessions(t, here, oc)) != 1 {
			t.Fatalf("the resume with no new work: %v %q", res.Run.Argv, res.Warnings)
		}
	})
	for _, tc := range []struct {
		name    string
		service bool // a service of another version runs at the recovery
	}{{"recovery keeps the prompt", false}, {"recovery checks the service", true}} {
		t.Run(tc.name, func(t *testing.T) {
			_, here, _, in := setup(t)
			opt := move.Options{TargetDir: here.repo, OperationID: "continue-oc-resume", Go: true}
			p, err := move.Build(ctx, in, opt)
			if err != nil {
				t.Fatal(err)
			}
			env := move.Env{StateDir: t.TempDir(), Failpoint: func(s string) error {
				if s == "native-written" {
					return errors.New("simulated process exit")
				}
				return nil
			}}
			if _, err := move.Apply(ctx, p, in, env); err == nil {
				t.Fatal("the fault did not interrupt the continuation")
			}
			env.Failpoint = nil
			if tc.service {
				t.Setenv("FAKE_OPENCODE_SERVICE", "running")
				t.Setenv("FAKE_OPENCODE_SERVICE_VERSION", "2.1.5")
			}
			if p, err = move.Build(ctx, in, opt); err != nil {
				t.Fatal(err)
			}
			res, err := move.Apply(ctx, p, in, env)
			if err != nil {
				t.Fatal(err)
			}
			if tc.service != refused(res) || !tc.service && !slices.Contains(res.Run.Argv, "--prompt") {
				t.Fatalf("the recovered resume: %v %q", res.Run.Argv, res.Warnings)
			}
		})
	}
}

// A bounded continuation of an OpenCode session (a new session that carries a bounded
// context of it) is written by OpenCode's import as a new session on the same branch; the
// original stays as it was.
func TestOpenCodeBoundedContinuation(t *testing.T) {
	log, _, here := ocWorld(t)
	seedOpenCode(t, here)
	oc := opencode.New()
	hereOC := ocInstall(here)
	ctx := context.Background()
	src := ocSessions(t, here, oc)[ocParent]
	stamp, err := oc.SessionStamp(ctx, mustHost(t, here, oc, hereOC), hereOC, ocParent)
	if err != nil {
		t.Fatal(err)
	}
	side := move.Side{Machine: here.m, Module: oc, Install: hereOC}
	in := move.Input{Source: side, Session: src, Target: side}
	p, err := move.Build(ctx, in, move.Options{TargetDir: here.repo, Bounded: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != move.KindContinue || p.Continue.Rollover == nil || len(p.Blockers) > 0 || p.Placement.Key.Session == ocParent {
		t.Fatalf("plan: %s %+v %v", p.Kind, p.Continue.Rollover, p.Blockers)
	}
	res, err := move.Apply(ctx, p, in, move.Env{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	made := p.Placement.Key.Session
	if !sessionThere(t, here, made) || !resumesOC(res, made) || p.Continue.Report.Capacity.Window != ir.FallbackWindow {
		t.Fatalf("continued in %s: %s (window %d)", made, res.Command, p.Continue.Report.Capacity.Window)
	}
	if seg := readAll(t, here, oc, hereOC, ocSessions(t, here, oc)[made]); !mentions(seg, "pong") {
		t.Fatal("the new session must carry the conversation, the subagent's answer too")
	}
	if after, err := oc.SessionStamp(ctx, mustHost(t, here, oc, hereOC), hereOC, ocParent); err != nil || after != stamp {
		t.Fatalf("the original changed: %v", err)
	}
	if n := len(calls(t, log, "session import")); n != 1 { // the seed is imported in the test's process, not logged
		t.Fatalf("imports: %q", calls(t, log, "session import"))
	}
}

// An OpenCode session handed off to Codex cloud comes back as a new OpenCode session:
// OpenCode cannot add to a session, so the original is not offered for the cloud's work
// (asking for it is refused), and the brought conversation is written under an id in
// OpenCode's form and imported by OpenCode's program.
func TestBringCloudWorkIntoOpenCode(t *testing.T) {
	w := newCodexWorld(t)
	self, _ := os.Executable()
	if err := os.Symlink(self, filepath.Join(filepath.Dir(w.home), "bin", "opencode")); err != nil {
		t.Fatal(err)
	}
	a := w.app()
	ctx := context.Background()
	inv := a.Scan(ctx, app.ScanOptions{})
	orig, err := inv.Find(app.ParseRef("opencode/ses_e29aa1ec8ffes7x6RCUekHyBbf"))
	if err != nil {
		t.Fatal(err)
	}
	opt := a.HandoffDefaults("codex-cloud")
	opt.Env = "env_api"
	hp, err := a.PlanHandoff(ctx, inv, orig, "codex-cloud", opt)
	if err != nil || len(hp.Blockers) > 0 {
		t.Fatalf("hand-off: %v %v", err, hp.Blockers)
	}
	hres, err := a.Apply(ctx, hp, move.Input{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, hres.Handoff.Session, false); err != nil {
		t.Fatal(err)
	}
	inv = a.Scan(ctx, app.ScanOptions{})
	defer inv.Close()
	e := cloudEntryFor(t, inv, "codex-cloud", hres.Handoff.Session)
	p, _, err := a.Plan(ctx, inv, e, "opencode", move.Options{})
	if err != nil || !p.Fetch.Write || p.Fetch.CanAppend || p.Fetch.Original != nil || len(p.Blockers) > 0 {
		t.Fatalf("the offer: %+v %v %v", p.Fetch, p.Blockers, err)
	}
	if p, _, err = a.Plan(ctx, inv, e, "opencode", move.Options{AppendOriginal: true}); err != nil || p.Fetch.Append ||
		!slices.ContainsFunc(p.Blockers, func(b string) bool { return strings.Contains(b, "OpenCode cannot add to an existing session") }) {
		t.Fatalf("asking to add the cloud's work to an OpenCode session: %v %v", p, err)
	}
	p, _, err = a.Plan(ctx, inv, e, "opencode", move.Options{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Apply(ctx, p, move.Input{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Fetch.Key, "opencode") || !strings.Contains(res.Fetch.Key, "/ses_") {
		t.Fatalf("brought as %q", res.Fetch.Key)
	}
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(w.home), "agents.log"))
	if !strings.Contains(string(b), "opencode session import ") {
		t.Fatal("OpenCode did not import the brought session")
	}
	inv2 := a.Scan(ctx, app.ScanOptions{})
	defer inv2.Close()
	sid := res.Fetch.Key[strings.LastIndex(res.Fetch.Key, "/")+1:]
	if _, err := inv2.Find(app.ParseRef("opencode/" + sid)); err != nil {
		t.Fatalf("the brought session is not listed: %v", err)
	}
	if _, err := a.Undo(ctx, res.Journal, false); err != nil {
		t.Fatal(err)
	}
	inv3 := a.Scan(ctx, app.ScanOptions{})
	defer inv3.Close()
	if _, err := inv3.Find(app.ParseRef("opencode/" + sid)); err == nil {
		t.Fatal("undo left the brought session")
	}
}
