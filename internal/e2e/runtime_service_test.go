package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
)

// This changes only a unique disposable namespace's user login registration.
// Every module is disabled so the supervised process cannot scan the OS user's
// real agent data. The installed desktop app and its namespace are untouched.
func TestRuntimeNativeUserServiceLifecycle(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RUNTIME_SERVICE") != "1" {
		t.Skip("native OS user supervisor qualification requires an available login/user manager")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	originalEnv := os.Environ()
	// Capture before writeConfig redirects HOME/USERPROFILE. Native service
	// management must use the actual OS user's infrastructure, even though all
	// Hopsesh settings/state and agent roots remain in the disabled fixture.
	keys := []string{"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"}
	var supervisorEnv []string
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			supervisorEnv = append(supervisorEnv, key+"="+value)
		}
	}
	bin := buildHopsesh(t)
	box := newMachineHome(t, t.TempDir(), "native-user-service", false)
	cfg := config.Defaults()
	cfg.Agents = map[string]config.Agent{}
	for _, m := range all.Modules() {
		cfg.Agents[string(m.Spec().ID)] = config.Agent{Disabled: true}
	}
	box.writeConfig(t, cfg)
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = append(box.env(), supervisorEnv...)
		if runtime.GOOS == "windows" {
			// This exercises native OS integration, not a simulated machine's
			// stripped environment. COM activation repeatedly timed out under
			// that fixture, even with standard Windows variables restored, while
			// the same read-only query passed under the original environment.
			// Preserve the actual OS context just as an ordinary CLI launch does;
			// override only Hopsesh's namespace. Task XML contains explicit config
			// and state flags, never a copy of this environment.
			cmd.Env = append([]string{}, originalEnv...)
			for _, kv := range box.env() {
				if strings.HasPrefix(kv, "HOPSESH_") {
					cmd.Env = append(cmd.Env, kv)
				}
			}
		}
		// Agent roots stay explicit and disposable even if a module is later enabled.
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+filepath.Join(box.home, ".claude"), "CODEX_HOME="+filepath.Join(box.home, ".codex"))
		started := time.Now()
		out, err := cmd.CombinedOutput()
		t.Logf("native runtime service %v completed in %s", args, time.Since(started).Round(time.Millisecond))
		if err != nil {
			if runtime.GOOS == "windows" {
				diagnoseWindowsScheduler(t, cmd.Env, originalEnv)
			}
			t.Fatalf("native runtime service %v: %v\n%s", args, err, out)
		}
		return out
	}
	var plan localruntime.ServicePlan
	if err := json.Unmarshal(run("runtime", "enable", "--plan"), &plan); err != nil {
		t.Fatal(err)
	}
	n, err := localruntime.NewNamespace(filepath.Join(box.home, "config"), filepath.Join(box.home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Namespace != n.ID {
		t.Fatal("login registration was not scoped to the disposable namespace")
	}
	client := localruntime.Client{Namespace: n}
	// Always drain before unregistering; use a separate cleanup context when the
	// qualification itself times out. Never target an unrelated supervisor job.
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		_ = client.Call(stop, "stop", nil, nil)
		if err := plan.DisableAtLogin(stop); err != nil {
			t.Errorf("native service cleanup: %v", err)
		}
	})
	var manual localruntime.Status
	if err = json.Unmarshal(run("runtime", "start"), &manual); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(run("runtime", "enable"), &plan); err != nil {
		t.Fatal(err)
	}
	ready := func() localruntime.Status {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			var status localruntime.Status
			probe, done := context.WithTimeout(ctx, time.Second)
			err := client.Call(probe, "status", nil, &status)
			done()
			if err == nil && status.Namespace == n.ID && status.Mode == "headless" {
				return status
			}
			if time.Now().After(deadline) {
				t.Fatal("registered supervisor did not establish the private runtime", err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	owned := ready()
	if owned.PID == manual.PID {
		t.Fatal("manual owner did not hand over to the supervisor")
	}
	var state localruntime.ServiceStatus
	if err = json.Unmarshal(run("runtime", "login-status"), &state); err != nil || !state.Known || !state.Registered || !state.Enabled || !state.Running || !state.Definition {
		t.Fatal("native supervisor state not verified", err, state)
	}
	run("runtime", "enable")
	if repeated := ready(); repeated.PID != owned.PID {
		t.Fatal("idempotent login enable restarted its running owner")
	}
	run("runtime", "disable")
	if err = client.WaitReleased(ctx); err != nil {
		t.Fatal("supervisor disable did not drain and release ownership", err)
	}
	if err = json.Unmarshal(run("runtime", "login-status"), &state); err != nil || !state.Known || state.Registered || state.Enabled || state.Definition {
		t.Fatal("native registration remains after disable", err, state)
	}
	if _, err = os.Stat(plan.Path); !os.IsNotExist(err) {
		t.Fatal("private login definition remains", err)
	}
	run("runtime", "enable")
	if restarted := ready(); restarted.PID == owned.PID {
		t.Fatal("reenabling did not establish a new owner")
	}
	run("runtime", "disable")
	if err = client.WaitReleased(ctx); err != nil {
		t.Fatal(err)
	}
}

// Compare the exact CLI fixture environment with the runner's original OS
// environment only after a failure. Both probes are read-only, local COM calls;
// neither invokes Hopsesh, loads a user profile, reads agent files nor prints
// environment values. This distinguishes environment damage from a scheduler
// or process-context failure without widening the tested CLI's credentials.
func diagnoseWindowsScheduler(t *testing.T, isolated, original []string) {
	t.Helper()
	const script = `$ErrorActionPreference='Stop'
[Console]::WriteLine('phase=create')
$service=New-Object -ComObject Schedule.Service
[Console]::WriteLine('phase=connect')
$service.Connect()
[Console]::WriteLine('phase=folder')
$folder=$service.GetFolder('\')
[Console]::WriteLine('phase=ready')`
	for _, probe := range []struct {
		name string
		env  []string
	}{{"CLI fixture", isolated}, {"original OS", original}} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		cmd.Env = probe.env
		cmd.WaitDelay = time.Second
		started := time.Now()
		out, err := cmd.Output()
		phase := "startup"
		for _, line := range strings.Split(string(out), "\n") {
			switch strings.TrimSpace(line) {
			case "phase=create", "phase=connect", "phase=folder", "phase=ready":
				phase = strings.TrimSpace(line)
			}
		}
		t.Logf("read-only scheduler probe %s: %s elapsed=%s error=%v deadline=%v", probe.name, phase, time.Since(started).Round(time.Millisecond), err, ctx.Err())
		cancel()
	}
}
