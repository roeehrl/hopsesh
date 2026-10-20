package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/relay"
)

// The Claude cloud path the person drives: the installed SessionStart hook prepares
// the identity, the person sends "hopsesh status" and then the owner's connect code,
// and the prompt hook claims it and starts the connector. The agent runs nothing.
func TestCloudConnectHookSQLiteR2(t *testing.T) {
	if testing.Short() || os.Getenv("HOPSESH_RELAY_PLATFORM") != "1" {
		t.Skip("actual SQLite/R2 cloud connect hook")
	}
	ctx, _, origin, cert, client := startSQLiteRelayFixture(t, 2*time.Minute)
	bin := buildHopsesh(t)
	f := newRelayFleet(t, ctx, bin, origin, cert, client)
	desktop := f.homes['A']
	cloud := newMachineHome(t, t.TempDir(), "cloud-hook", true)
	cloud.writeConfig(t, config.Defaults())
	transcript := filepath.Join(cloud.home, ".claude", "projects", claude.Slug(cloud.repo), sid+".jsonl")
	env := append(cloud.env(), "CLAUDE_CODE_REMOTE=true", "CLAUDE_PROJECT_DIR="+cloud.repo)
	hook := func(input any, args ...string) []byte {
		t.Helper()
		body, _ := json.Marshal(input)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env, cmd.Stdin = env, bytes.NewReader(body)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s failed: %v %s", args[1], err, stderr.String())
		}
		return out
	}
	prompt := func(text string, extra ...string) []byte {
		t.Helper()
		in := map[string]string{"session_id": sid, "cwd": cloud.repo, "transcript_path": transcript, "hook_event_name": "UserPromptSubmit", "prompt": text}
		return hook(in, append([]string{"cloud-integration", "connect-hook"}, extra...)...)
	}
	decision := func(out []byte) string {
		t.Helper()
		var d struct{ Decision, Reason string }
		if err := json.Unmarshal(out, &d); err != nil || d.Decision != "block" || d.Reason == "" {
			t.Fatalf("hook did not block its own message: %s", out)
		}
		return d.Reason
	}

	// SessionStart, exactly as the installed hook runs it (owner opted into export).
	start := map[string]string{"session_id": sid, "cwd": cloud.repo, "transcript_path": transcript, "hook_event_name": "SessionStart", "source": "startup"}
	if out := hook(start, "cloud-integration", "prepare", "--claude-hook", "--quiet", "--allow-transcript-export"); len(out) != 0 {
		t.Fatalf("SessionStart hook wrote into the agent's context: %s", out)
	}
	if out := prompt("please continue the refactor"); len(out) != 0 {
		t.Fatalf("ordinary prompt was not passed through: %s", out)
	}
	var instance cloudintegration.Incarnation
	cmd := exec.CommandContext(ctx, bin, "cloud-integration", "current", "--provider", "claude-hosted", "--session", sid, "--workspace", cloud.repo)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil || json.Unmarshal(out, &instance) != nil || !instance.ExportTranscript {
		t.Fatalf("hook-prepared identity: %s %v", out, err)
	}
	status := decision(prompt(cloudintegration.StatusPrompt))
	for _, want := range []string{sid, instance.Public.ID, "not connected", "--session " + sid + " --connect-code"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status reply lacks %q: %s", want, status)
		}
	}

	// The owner issues the code; only its single line is sent into the session.
	issued := string(f.run(t, desktop, "cloud-integration", "ticket", "--provider", "claude-hosted", "--session", sid, "--lease", "10m", "--connect-code"))
	code := ""
	for _, line := range strings.Split(issued, "\n") {
		if strings.HasPrefix(line, "hopsesh-connect ") {
			code = line
		}
	}
	if code == "" {
		t.Fatalf("no connect code printed: %s", issued)
	}
	reply := decision(prompt(code, "--ca-file", cert))
	if !strings.Contains(reply, "connected this session") || !strings.Contains(reply, instance.Public.ID) || strings.Contains(reply, code) {
		t.Fatalf("connect reply: %s", reply)
	}
	pidBody, err := os.ReadFile(filepath.Join(instance.Directory, "serve.pid"))
	if err != nil {
		t.Fatal("connector was not started", err)
	}
	pid, _ := strconv.Atoi(string(pidBody))
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	if !strings.Contains(decision(prompt(cloudintegration.StatusPrompt)), "connected until") {
		t.Fatal("status does not report the connection")
	}

	// The desktop still approves the fresh identity before importing anything.
	store := relay.Store{Directory: filepath.Join(desktop.home, "state", "relay")}
	if err = store.Approve(ctx, relay.Grant{Peer: instance.Public, Endpoint: instance.Public.Endpoint, Kind: "cloud-session", SendMethods: []string{"observe", "export"}, Expires: instance.Expires.Unix()}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Plan   move.Plan   `json:"plan"`
		Result move.Result `json:"result"`
	}
	deadline := time.Now().Add(time.Minute)
	for {
		c := exec.CommandContext(ctx, bin, "cloud-integration", "import", instance.Public.ID, "--to", desktop.repo, "--in", "claude", "--operation-id", "connect-hook-import-1", "--yes")
		c.Env = desktop.env()
		var stderr bytes.Buffer
		c.Stderr = &stderr
		out, err := c.Output()
		if err == nil {
			if err = json.Unmarshal(out, &result); err != nil {
				t.Fatal(err)
			}
			break
		}
		// The detached connector may still be registering its listener.
		if time.Now().After(deadline) {
			t.Fatalf("import through the hook-started connector failed: %v %s", err, stderr.String())
		}
		time.Sleep(time.Second)
	}
	if result.Result.Journal == "" {
		t.Fatal("checkpoint import had no durable native outcome")
	}
	f.find(t, 'A', "claude", string(result.Plan.Placement.Key.Session))
}
