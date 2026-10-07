package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func movementHookFixture(t *testing.T) (*App, *host.Machine, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "claude")
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.New(claude.New())
	if err != nil {
		t.Fatal(err)
	}
	a := New(config.Config{}, reg, filepath.Join(dir, "state"), nil)
	m := &host.Machine{Local: true, Name: "test-machine", Facts: host.Facts{Home: dir, OS: runtime.GOOS, Endpoint: strings.Repeat("a", 64), Env: map[string]string{"CLAUDE_CONFIG_DIR": root}, Binaries: map[string]agent.BinaryFact{"claude": {Path: "/fake/claude", Version: "2.1.284 (Claude Code)"}}}}
	return a, m, root
}

func TestMovementNoticeHookLifecyclePreservesSettings(t *testing.T) {
	a, m, root := movementHookFixture(t)
	defer m.Close()
	path := filepath.Join(root, "settings.json")
	original := []byte(`{"permissions":{"ask":["Bash(rm *)"]},"disableAllHooks":true,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"my-hook"}]}]}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run := func(action string) MovementHookStatus {
		t.Helper()
		ss, err := a.movementNoticeHooks(ctx, m, action, "claude", "", "/opt/bin/hopsesh")
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: %+v %v", action, ss, err)
		}
		return ss[0]
	}
	if run("status").Installed {
		t.Fatal("initially installed")
	}
	if !run("install").Installed {
		t.Fatal("install failed")
	}
	first, _ := os.ReadFile(path)
	fi, _ := os.Stat(path)
	if !run("install").Installed {
		t.Fatal("second install failed")
	}
	second, _ := os.ReadFile(path)
	si, _ := os.Stat(path)
	if !bytes.Equal(first, second) || !fi.ModTime().Equal(si.ModTime()) {
		t.Fatal("idempotent install rewrote file")
	}
	if !bytes.Contains(second, []byte(`"disableAllHooks": true`)) {
		t.Fatal("changed vendor policy")
	}
	if run("remove").Installed {
		t.Fatal("still installed")
	}
	removed, _ := os.ReadFile(path)
	if !strings.Contains(string(removed), "my-hook") || !strings.Contains(string(removed), "Bash(rm *)") {
		t.Fatalf("lost config: %s", removed)
	}
	run("remove")
	files, _ := os.ReadDir(root)
	if len(files) != 2 || files[0].Name() != "settings.json" || files[1].Name() != "settings.json.hopsesh-notice-lock" {
		t.Fatalf("left unexpected files: %v", files)
	}
}

func TestMovementNoticeHookExactLocalProfile(t *testing.T) {
	a, m, root := movementHookFixture(t)
	defer m.Close()
	other := filepath.Join(filepath.Dir(root), "other")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := a.accountStore().Register(agent.RuntimeProfile{ID: strings.Repeat("b", 32), Agent: "claude", Endpoint: m.Facts.Endpoint, Machine: m.Name, Root: other, Name: "work"})
	if err != nil {
		t.Fatal(err)
	}
	ss, err := a.movementNoticeHooks(context.Background(), m, "install", "claude", p.ID, "/opt/hopsesh")
	if err != nil || len(ss) != 1 || ss[0].Profile != p.ID || ss[0].Path != filepath.Join(other, "settings.json") {
		t.Fatalf("%+v %v", ss, err)
	}
	if _, err = os.Stat(filepath.Join(root, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("modified default root")
	}
	for _, bad := range []string{p.ID[:8], p.Name, "../other"} {
		if _, err = a.movementNoticeHooks(context.Background(), m, "install", "claude", bad, "/opt/hopsesh"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	m.Facts.Endpoint = strings.Repeat("c", 64)
	if _, err = a.movementNoticeHooks(context.Background(), m, "status", "claude", p.ID, "/opt/hopsesh"); err == nil {
		t.Fatal("accepted remote profile")
	}
}

func TestMovementNoticeHookGateAndSafeFiles(t *testing.T) {
	a, m, root := movementHookFixture(t)
	defer m.Close()
	m.Facts.Binaries["claude"] = agent.BinaryFact{Version: "2.0.0"}
	ss, err := a.movementNoticeHooks(context.Background(), m, "install", "claude", "", "/opt/hopsesh")
	if err == nil || ss[0].Supported {
		t.Fatalf("unsupported installed: %+v %v", ss, err)
	}
	path := filepath.Join(root, "settings.json")
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unsupported created settings")
	}
	if _, err = a.movementNoticeHooks(context.Background(), m, "remove", "claude", "", "/opt/hopsesh"); err != nil {
		t.Fatal("removal must work after downgrade", err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside")
	if err = os.WriteFile(outside, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, path); err == nil {
		if _, err = readMovementHookFile(path); err == nil {
			t.Fatal("accepted settings symlink")
		}
	}
	off := false
	a.Cfg.MovementNotices = &off
	if _, err = a.MovementNoticeHooks(context.Background(), "install", "claude", "", "/opt/hopsesh"); err == nil {
		t.Fatal("installed while disabled")
	}
}

func TestMovementNoticeDeliveryResumePromptAndChangedStatus(t *testing.T) {
	a, _, root := movementHookFixture(t)
	claim := func(event, text, profile string, want bool) {
		t.Helper()
		yes, err := a.ClaimMovementNotice(context.Background(), "claude", profile, "session", event, text)
		if err != nil || (yes != nil) != want {
			t.Fatalf("%s %q profile=%q: %v %v", event, text, profile, yes, err)
		}
		if yes != nil {
			if err := yes.Written(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	}
	claim("SessionStart", "Moved to laptop", "profile-a", true)
	claim("UserPromptSubmit", "Moved to laptop", "profile-a", false)
	claim("UserPromptSubmit", "Moved to laptop", "profile-a", false)
	claim("UserPromptSubmit", "Returned here", "profile-a", true)
	claim("UserPromptSubmit", "Returned here", "profile-a", false)
	claim("SessionStart", "Returned here", "profile-a", true)
	claim("UserPromptSubmit", "Returned here", "profile-b", true)
	claim("UserPromptSubmit", "", "profile-a", false)
	claim("UserPromptSubmit", "Returned here", "profile-a", true)
	// Receipts contain a digest/generation only, and never write agent data.
	files, _ := os.ReadDir(root)
	if len(files) != 0 {
		t.Fatalf("wrote agent data: %v", files)
	}
	receipts, _ := os.ReadDir(filepath.Join(a.StateDir, "movement-hook-delivery"))
	if len(receipts) != 4 {
		t.Fatalf("receipts %v", receipts)
	}
	for _, r := range receipts {
		b, _ := os.ReadFile(filepath.Join(a.StateDir, "movement-hook-delivery", r.Name()))
		if bytes.Contains(b, []byte("Returned")) || bytes.Contains(b, []byte("session")) {
			t.Fatalf("receipt contains content: %s", b)
		}
	}
}

func TestMovementNoticeHooksAllLocalProfilesAndDefaultDedup(t *testing.T) {
	a, m, root := movementHookFixture(t)
	defer m.Close()
	other := filepath.Join(filepath.Dir(root), "other")
	remote := filepath.Join(filepath.Dir(root), "remote")
	for _, r := range []string{other, remote} {
		if err := os.MkdirAll(r, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []agent.RuntimeProfile{
		{ID: strings.Repeat("1", 32), Agent: "claude", Endpoint: m.Facts.Endpoint, Machine: m.Name, Root: root, Name: "default"},
		{ID: strings.Repeat("2", 32), Agent: "claude", Endpoint: m.Facts.Endpoint, Machine: m.Name, Root: other, Name: "work"},
		{ID: strings.Repeat("3", 32), Agent: "claude", Endpoint: strings.Repeat("c", 64), Machine: m.Name, Root: remote, Name: "same label remote"},
	} {
		if _, err := a.accountStore().Register(p); err != nil {
			t.Fatal(err)
		}
	}
	ss, err := a.movementNoticeHooks(context.Background(), m, "install", "", "", "/opt/hopsesh")
	if err != nil || len(ss) != 2 {
		t.Fatalf("all roots: %+v %v", ss, err)
	}
	if ss[0].Profile != strings.Repeat("1", 32) || ss[1].Profile != strings.Repeat("2", 32) {
		t.Fatalf("profile not pinned: %+v", ss)
	}
	for _, r := range []string{root, other} {
		b, err := os.ReadFile(filepath.Join(r, "settings.json"))
		if err != nil || bytes.Count(b, []byte("notice-hook")) != 2 {
			t.Fatalf("expected exactly two event hooks: %s %v", b, err)
		}
	}
	if _, err = os.Stat(filepath.Join(remote, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("touched remote registration")
	}
	// A default absent from the environment must not hide registered roots.
	m.Facts.Env["CLAUDE_CONFIG_DIR"] = filepath.Join(filepath.Dir(root), "absent")
	ss, err = a.movementNoticeHooks(context.Background(), m, "status", "", "", "/opt/hopsesh")
	if err != nil || len(ss) != 2 {
		t.Fatalf("absent default hid registered roots: %+v %v", ss, err)
	}
}

func TestMovementNoticeDeliveryEvidenceIsReadOnlyAndExact(t *testing.T) {
	a, _, _ := movementHookFixture(t)
	const notice = "Hopsesh status: prepared elsewhere"
	if a.MovementNoticeDelivery("claude", "p", "s", notice) {
		t.Fatal("unclaimed delivery")
	}
	if _, err := os.Stat(a.StateDir); !os.IsNotExist(err) {
		t.Fatal("read created state")
	}
	yes, err := a.ClaimMovementNotice(context.Background(), "claude", "p", "s", "SessionStart", notice)
	if err != nil || yes == nil {
		t.Fatal(yes, err)
	}
	if a.MovementNoticeDelivery("claude", "p", "s", notice) {
		t.Fatal("attempt counted as written")
	}
	if err := yes.Written(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !a.MovementNoticeDelivery("claude", "p", "s", notice) {
		t.Fatal("missing claim")
	}
	for _, args := range [][4]string{{"codex", "p", "s", notice}, {"claude", "q", "s", notice}, {"claude", "p", "t", notice}, {"claude", "p", "s", notice + " changed"}, {"claude", "p", "s", ""}} {
		if a.MovementNoticeDelivery(agent.ID(args[0]), args[1], args[2], args[3]) {
			t.Fatalf("wrong identity: %v", args)
		}
	}
}

func TestMovementNoticeDeliveryRepeatedHopWithIdenticalText(t *testing.T) {
	a, _, _ := movementHookFixture(t)
	const text = "Hopsesh status: prepared in Codex on bob-laptop"
	identity := MovementNoticeIdentity{Operation: "outward-one", Status: "prepared"}
	claim := func(want bool) {
		t.Helper()
		got, err := a.ClaimMovementNotice(context.Background(), "claude", "profile-a", "session", "UserPromptSubmit", text, identity)
		if err != nil || (got != nil) != want {
			t.Fatalf("claim=%v want=%v: %v", got, want, err)
		}
		if got != nil {
			if err := got.Written(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	}
	delivered := func() bool { return a.MovementNoticeDelivery("claude", "profile-a", "session", text, identity) }
	claim(true)
	claim(false)
	if !delivered() {
		t.Fatal("first operation missing delivery")
	}
	identity.Operation = "outward-two"
	if delivered() {
		t.Fatal("old receipt delivered new operation")
	}
	claim(true)
	claim(false)
	identity.Status = "continued"
	if delivered() {
		t.Fatal("old receipt delivered new status")
	}
	claim(true)
	claim(false)
	if _, err := os.Stat(filepath.Join(a.StateDir, "movement")); !os.IsNotExist(err) {
		t.Fatal("delivery used scan cache")
	}
}

func TestMovementNoticeDeliveryDigestStableIdentity(t *testing.T) {
	id := MovementNoticeIdentity{Operation: "hop-one", Status: "prepared"}
	baseline := movementNoticeDeliveryDigest("notice", id)
	if baseline == movementNoticeDeliveryDigest("notice") || baseline == movementNoticeDeliveryDigest("notice", MovementNoticeIdentity{Operation: "hop-two", Status: id.Status}) || baseline == movementNoticeDeliveryDigest("notice", MovementNoticeIdentity{Operation: id.Operation, Status: "continued"}) {
		t.Fatal("identity collapsed")
	}
	if baseline != movementNoticeDeliveryDigest("notice", id) {
		t.Fatal("unstable identity")
	}
}

// Setup before and after a scan registers the default root must leave only the
// canonical exact-profile handlers, including migration of pre-directory hooks.
func TestMovementHookDefaultRegistrationMigratesOwnedHandlers(t *testing.T) {
	a, m, root := movementHookFixture(t)
	defer m.Close()
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(filepath.Dir(root), "installer config"))
	ctx := context.Background()
	bin := "/opt/hopsesh"
	run := func(action string) MovementHookStatus {
		t.Helper()
		ss, err := a.movementNoticeHooks(ctx, m, action, "claude", "", bin)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: %v %+v", action, err, ss)
		}
		return ss[0]
	}
	if st := run("install"); st.Profile != "" || !st.Installed {
		t.Fatal(st)
	}
	mod, _ := a.Module("claude")
	h, err := m.For(ctx, mod.Spec(), agent.Install{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	in, err := mod.Detect(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	old := mod.(agent.MovementHookIntegrator).MovementHooks(h, in, bin).File
	path := filepath.Join(root, "settings.json")
	b, _ := os.ReadFile(path)
	b, err = old.Merge(b)
	if err != nil {
		t.Fatal(err)
	}
	// Another user's handler containing the same words must survive migration.
	unrelated := agent.JSONMovementHookFile(path, "echo notice-hook unrelated")
	b, err = unrelated.Merge(b)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := a.accountStore().Register(agent.RuntimeProfile{ID: strings.Repeat("d", 32), Agent: "claude", Endpoint: m.Facts.Endpoint, Machine: m.Name, Root: root, Binding: "binding", Name: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if run("status").Installed {
		t.Fatal("obsolete default handler reported current")
	}
	if st := run("install"); st.Profile != p.ID || st.ProfileLabel != "default" || !st.Installed {
		t.Fatal(st)
	}
	b, _ = os.ReadFile(path)
	if old.Has(b) || !unrelated.Has(b) || bytes.Count(b, []byte("notice-hook")) != 4 {
		t.Fatalf("migration lost or duplicated hooks: %s", b)
	}
	if !bytes.Contains(b, []byte(p.ID)) || !bytes.Contains(b, []byte("--config-dir")) || !bytes.Contains(b, []byte("--state-dir")) {
		t.Fatalf("identity not pinned: %s", b)
	}
	if !bytes.Contains(b, []byte("installer config")) {
		t.Fatalf("wrong config directory: %s", b)
	}
	if !run("install").Installed {
		t.Fatal("reinstall failed")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(b, after) {
		t.Fatal("migration not idempotent")
	}
	run("remove")
	b, _ = os.ReadFile(path)
	if bytes.Count(b, []byte("notice-hook")) != 2 || !unrelated.Has(b) {
		t.Fatalf("remove: %s", b)
	}
}

func TestMovementNoticeAttemptRetryAndGenerationFence(t *testing.T) {
	a, _, _ := movementHookFixture(t)
	ctx := context.Background()
	claim := func(event string) *MovementNoticeClaim {
		t.Helper()
		c, err := a.ClaimMovementNotice(ctx, "claude", "p", "s", event, "notice")
		if err != nil || c == nil {
			t.Fatalf("claim: %v %v", c, err)
		}
		return c
	}
	first := claim("SessionStart")
	retry := claim("UserPromptSubmit")
	if a.MovementNoticeDelivery("claude", "p", "s", "notice") {
		t.Fatal("attempt was delivery")
	}
	if err := first.Written(ctx); err != nil {
		t.Fatal(err)
	}
	if a.MovementNoticeDelivery("claude", "p", "s", "notice") {
		t.Fatal("superseded attempt acknowledged")
	}
	if err := retry.Written(ctx); err != nil {
		t.Fatal(err)
	}
	if !a.MovementNoticeDelivery("claude", "p", "s", "notice") {
		t.Fatal("written missing")
	}
	resume := claim("SessionStart")
	if a.MovementNoticeDelivery("claude", "p", "s", "notice") {
		t.Fatal("new resume inherited old delivery")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := resume.Written(cancelled); err == nil {
		t.Fatal("cancelled write acknowledged")
	}
	claim("UserPromptSubmit")
}

func TestMovementHookWrittenReceiptAppearsInScanJSON(t *testing.T) {
	a, g, from, _, _, _ := movementFixture(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	path := filepath.Join(root, "alice-session.jsonl")
	if err := os.WriteFile(lineage.PathFor(path), g.Encode(), 0600); err != nil {
		t.Fatal(err)
	}
	replica := g.Replica(from)
	local := &host.Machine{Local: true, Name: LocalName(), Facts: host.Facts{Endpoint: replica.Endpoint}}
	defer local.Close()
	inv := &Inventory{Machines: []*Machine{{Name: LocalName(), Local: true, Status: StatusOK, host: local}}, Entries: []Entry{{Agent: "claude", Machine: LocalName(), Session: agent.Summary{Key: replica.Key, Path: path}, Lineage: g}}}
	ctx := context.Background()
	check := func(want string) {
		t.Helper()
		a.EnrichMovement(ctx, inv)
		if n := inv.Entries[0].Movement; n == nil || n.Delivery != want {
			t.Fatalf("scan notice: %+v want %s", n, want)
		}
		b, err := json.Marshal(inv.Entries[0])
		if err != nil || !bytes.Contains(b, []byte(`"delivery":"`+want+`"`)) {
			t.Fatalf("scan JSON: %s %v", b, err)
		}
	}
	check("pending")
	details, err := a.MovementNoticeDetailsForPath(ctx, "claude", "", "alice-session", path)
	if err != nil || details.Text == "" {
		t.Fatalf("details: %+v %v", details, err)
	}
	identity := MovementNoticeIdentity{Operation: details.Operation, Status: details.Status}
	claim, err := a.ClaimMovementNotice(ctx, details.Key.Agent, details.Key.Profile, string(details.Key.Session), "SessionStart", details.Text, identity)
	if err != nil || claim == nil {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	check("pending")
	if err := claim.Written(ctx); err != nil {
		t.Fatal(err)
	}
	check("supplied-to-hook")
	// A new resume generation must not inherit the previous write's evidence.
	if _, err := a.ClaimMovementNotice(ctx, details.Key.Agent, details.Key.Profile, string(details.Key.Session), "SessionStart", details.Text, identity); err != nil {
		t.Fatal(err)
	}
	check("pending")
}
