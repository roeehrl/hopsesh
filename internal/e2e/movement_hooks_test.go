package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// TestMain dispatches the copied executable here. This observes what the real
// shell delivered, rather than merely asserting that a command contains quotes.
func movementHookProbe() int {
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 1
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Args  []string `json:"args"`
		Input string   `json:"input"`
	}{os.Args[1:], string(b)}); err != nil {
		return 1
	}
	return 0
}

func TestMovementHookCommandInvocation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "hook path's 日本語 $dollar & semi;")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "hopsesh-notice-probe")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := os.WriteFile(bin, movementBytes(t, self), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"claude", "codex"} {
		t.Run(id, func(t *testing.T) {
			_, _, in := movementInput(t, "claude", id)
			install := in.Target.Install
			if id == "codex" {
				install.Version = "0.160.0"
			}
			h, err := in.Target.Machine.For(context.Background(), in.Target.Module.Spec(), install, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, bad := range []string{"bad\x00value", "bad\nvalue", "bad\rvalue"} {
				install.Profile = &agent.RuntimeProfile{ID: bad}
				hi := in.Target.Module.(agent.MovementHookIntegrator).MovementHooks(h, install, bin)
				if hi.Reason == "" {
					t.Errorf("accepted malformed profile %q", bad)
				}
				install.Profile = nil
				hi = in.Target.Module.(agent.MovementHookIntegrator).MovementHooks(h, install, bin+bad)
				if hi.Reason == "" {
					t.Errorf("accepted malformed executable path %q", bad)
				}
			}
			for _, profile := range []string{"", "profile with 'quotes' $HOME `literal` & semi; %PATH%"} {
				install.Profile = &agent.RuntimeProfile{ID: profile}
				hi := in.Target.Module.(agent.MovementHookIntegrator).MovementHooks(h, install, bin)
				if hi.Reason != "" || hi.File == nil {
					t.Fatalf("verified %s/%s must provide an invocable hook: %+v", runtime.GOOS, id, hi)
				}
				b, err := hi.File.Merge(nil)
				if err != nil {
					t.Fatal(err)
				}
				var doc struct {
					Hooks map[string][]struct {
						Hooks []struct {
							Command        string `json:"command"`
							CommandWindows string `json:"commandWindows"`
							Shell          string `json:"shell"`
						} `json:"hooks"`
					} `json:"hooks"`
				}
				if err := json.Unmarshal(b, &doc); err != nil {
					t.Fatal(err)
				}
				for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
					groups := doc.Hooks[event]
					if len(groups) != 1 || len(groups[0].Hooks) != 1 {
						t.Fatalf("%s: missing hook: %s", event, b)
					}
					handler := groups[0].Hooks[0]
					command := handler.Command
					if runtime.GOOS == "windows" {
						if id == "codex" {
							if handler.CommandWindows == "" {
								t.Fatal("missing Codex Windows command override")
							}
							command = handler.CommandWindows
						} else if handler.Shell != "powershell" {
							t.Fatalf("Claude Windows hook must select its documented shell: %+v", handler)
						}
					}
					shells := [][]string{{"sh", "-c", command}}
					if runtime.GOOS == "windows" {
						shells = [][]string{{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command}}
						if id == "codex" {
							shells = append(shells, []string{"cmd.exe", "/d", "/s", "/c", command})
						}
					}
					for _, shell := range shells {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						cmd := exec.CommandContext(ctx, shell[0], shell[1:]...)

						payload := fmt.Sprintf(`{"session_id":"session-'quote'","hook_event_name":%q,"transcript_path":"C:\\fixture 'quotes'\\$(literal);.jsonl"}`, event)
						cmd.Stdin = strings.NewReader(payload)
						out, err := cmd.CombinedOutput()
						if err != nil {
							t.Fatalf("%s/%s hook invocation: %v\n%s", id, event, err, out)
						}
						var got struct {
							Args  []string `json:"args"`
							Input string   `json:"input"`
						}
						if err := json.Unmarshal(out, &got); err != nil {
							t.Fatalf("hook emitted non-JSON output: %s: %v", out, err)
						}
						want := []string{"notice-hook", "--agent", id, "--profile", profile}
						if runtime.GOOS == "windows" {
							want = []string{"notice-hook", "--agent", id, "--profile=" + profile}
						}
						if !reflect.DeepEqual(got.Args, want) || strings.TrimRight(got.Input, "\r\n") != payload {
							t.Fatalf("shell changed arguments or interpolated payload: %+v; want %q and %q", got, want, payload)
						}
					}
				}
			}
		})
	}
}
