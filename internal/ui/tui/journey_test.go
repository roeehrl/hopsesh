package tui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestDestinationSelectionCannotApplyAnUnrefreshedPlan(t *testing.T) {
	m := newModel(t)
	m.Update(m.Init()())
	for _, r := range m.rows {
		if r.item != nil {
			m.sel = r
			break
		}
	}
	choices := []agent.Summary{{Key: agent.SessionKey{Agent: "claude", Session: "original"}, Title: "Original"}, {Key: agent.SessionKey{Agent: "claude", Session: "second"}, Title: "Second copy"}}
	m.Update(planDone{plan: &move.Plan{Kind: move.KindMove, Destinations: choices, Blockers: []string{"select a destination"}}})
	if _, cmd := m.key("y"); cmd != nil {
		t.Fatal("ambiguous destination applied")
	}
	if _, cmd := m.key("d"); cmd == nil || m.opts.TargetSession != "claude/original" || !m.planning {
		t.Fatal("selection did not replan", m.opts.TargetSession)
	}
	if _, cmd := m.key("y"); cmd != nil {
		t.Fatal("stale plan applied during selection")
	}
	// The newly selected plan must retain the picker so another replica can be chosen.
	m.Update(planDone{plan: &move.Plan{Kind: move.KindMove}})
	if _, cmd := m.key("d"); cmd == nil || m.opts.TargetSession != "claude/second" {
		t.Fatal("second replica unreachable", m.opts.TargetSession)
	}
	m.Update(planDone{plan: &move.Plan{Kind: move.KindMove}})
	if view := m.View().Content; !strings.Contains(view, "choose destination session") || !strings.Contains(view, "claude/second") {
		t.Fatal(view)
	}
	m.inv.Close()
}

func TestJourneyShowsRealTransferAndCanReturnToTheList(t *testing.T) {
	m := newModel(t)
	m.Update(m.Init()())
	defer func() {
		if m.inv != nil {
			m.inv.Close()
		}
	}()
	ctx := context.Background()
	e, err := m.inv.Find(app.ParseRef("Find the codeword"))
	if err != nil {
		t.Fatal(err)
	}
	p, in, err := m.deps.App.Plan(ctx, m.inv, e, "codex", m.opts)
	if err != nil || len(p.Blockers) > 0 {
		t.Fatal(err, p.Blockers)
	}
	if _, err = m.deps.App.Apply(ctx, p, in, nil); err != nil {
		t.Fatal(err)
	}
	m.inv.Close()
	m.Update(scanDone{inv: m.deps.App.Scan(ctx, app.ScanOptions{Hosts: []string{app.LocalName()}})})
	for i, r := range m.rows {
		if r.item != nil && r.item.Entry.Agent == "codex" && r.item.Entry.Lineage != nil {
			m.cursor = i
			break
		}
	}
	m.width, m.height = 120, 10
	m.key("h")
	if m.mode != modeJourney {
		t.Fatal("journey inaccessible")
	}
	lines := strings.Join(m.journeyLines(), "\n")
	for _, want := range []string{"Branch:", "Family:", "1 transfers", "0 round trips to origin", "claude →", "/codex", "Operation:", "reasoning omitted"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("missing %q: %s", want, lines)
		}
	}
	m.key("pgdown")
	if m.journeyOffset == 0 {
		t.Fatal("journey cannot scroll on a short screen")
	}
	m.key("esc")
	if m.mode != modeBrowse {
		t.Fatal("journey cannot return to sessions")
	}
}

func TestWorklessPlanAndResultExplainTheExistingSession(t *testing.T) {
	m := newModel(t)
	m.mode = modePlan
	m.plan = &move.Plan{Kind: move.KindMove, Title: "Original", NoWork: true, Placement: agent.Placement{Key: agent.SessionKey{Agent: "claude", Session: "original"}}}
	if v := m.View().Content; !strings.Contains(v, "Sync lineage receipts") || !strings.Contains(v, "claude/original") || !strings.Contains(v, "0 new messages, 0 transfers") {
		t.Fatal(v)
	}
	m.mode, m.result = modeDone, &move.Result{}
	if v := m.View().Content; !strings.Contains(v, "already synchronized") || !strings.Contains(v, "0 new messages, 0 transfers") {
		t.Fatal(v)
	}
}

// A session whose opening would restart its agent's background service (OpenCode's,
// decision D2) is not opened by enter on the done screen: the screen asks first, and
// anything but y leaves it.
func TestDoneAsksBeforeRestartingTheService(t *testing.T) {
	m := newModel(t)
	m.mode = modeDone
	m.plan = &move.Plan{Kind: move.KindMove, Title: "Fix it", Agent: "OpenCode", Placement: agent.Placement{Key: agent.SessionKey{Agent: "opencode", Session: "ses_1"}}}
	m.result = &move.Result{Command: "opencode -s ses_1", RestartsService: true, Journal: "j1",
		Warnings: []string{"before you resume it: OpenCode's background service runs 2.2.5, and the opencode program (2.3.0) is another version"}}
	if v := m.View().Content; !strings.Contains(v, "runs 2.2.5") || !strings.Contains(v, "asks before restarting OpenCode's background service") {
		t.Fatal(v)
	}
	if _, cmd := m.key("enter"); cmd != nil || m.exit != nil || !m.restartAsk {
		t.Fatalf("enter opened it: %+v", m.exit)
	}
	if v := m.View().Content; !strings.Contains(v, "Restart OpenCode's background service and open it?") || !strings.Contains(v, "y: restart it and open") {
		t.Fatal(v)
	}
	if _, cmd := m.key("n"); cmd != nil || m.exit != nil || m.restartAsk {
		t.Fatalf("no opened it: %+v", m.exit)
	}
}

// y on the done screen opens the session with the person's yes to restart OpenCode's
// background service, and with the plan's own start (its first prompt), not a bare resume.
func TestDoneOpensWithTheYesToRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in opencode is linked in, which needs a POSIX system here")
	}
	const service = "2.2.5" // the stand-in is 2.3.0
	if b, err := os.ReadFile("/Applications/OpenCode.app/Contents/Resources/opencode-cli.version"); err == nil && strings.TrimSpace(string(b)) == service {
		t.Skipf("OpenCode desktop here bundles opencode %s, which the module would pick", service)
	}
	m := newModel(t)
	bin := t.TempDir()
	self, _ := os.Executable()
	if err := os.Symlink(self, filepath.Join(bin, "opencode")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_OPENCODE_SERVICE", "running")
	t.Setenv("FAKE_OPENCODE_SERVICE_VERSION", service)
	log := filepath.Join(t.TempDir(), "agents.log")
	t.Setenv("FAKE_AGENT_LOG", log)
	m.Update(m.Init()())
	defer func() {
		if m.inv != nil {
			m.inv.Close()
		}
	}()
	ctx := context.Background()
	e, err := m.inv.Find(app.ParseRef("Find the codeword"))
	if err != nil {
		t.Fatal(err)
	}
	opts := m.opts
	opts.Go = true
	p, in, err := m.deps.App.Plan(ctx, m.inv, e, "opencode", opts)
	if err != nil || len(p.Blockers) > 0 {
		t.Fatal(err, p.Blockers)
	}
	res, err := m.deps.App.Apply(ctx, p, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.RestartsService {
		t.Fatalf("not held for the restart: %+v", res)
	}
	m.mode, m.plan, m.result = modeDone, p, res
	if _, cmd := m.key("enter"); cmd != nil || m.exit != nil || !m.restartAsk {
		t.Fatalf("enter opened it: %+v", m.exit)
	}
	if _, cmd := m.key("y"); cmd == nil || m.exit == nil || m.mode == modeError {
		t.Fatalf("y did not open it: %v %+v", m.err, m.exit)
	}
	if a0 := m.exit.RunArgv[0]; a0 != "opencode" && a0 != filepath.Join(bin, "opencode") || m.exit.Key != p.Placement.Key {
		t.Fatalf("opened %+v", m.exit)
	}
	if calls, _ := os.ReadFile(log); !strings.Contains(string(calls), "service status") {
		t.Fatalf("the stand-in was not asked:\n%s", calls)
	}
	if i := slices.Index(m.exit.RunArgv, "--prompt"); i < 0 || i+1 >= len(m.exit.RunArgv) || m.exit.RunArgv[i+1] != "Continue." {
		t.Fatalf("the open lost the plan's prompt: %q", m.exit.RunArgv)
	}
}
