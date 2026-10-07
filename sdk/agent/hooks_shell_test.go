package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// A real native child exercises shell argument/stdin/output transport in platform
// CI. This never starts an agent or reads its configuration, much less inference.
func init() {
	if os.Getenv("HOPSESH_HOOK_TEST_HELPER") == "1" {
		b, _ := io.ReadAll(os.Stdin)
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": os.Args[1:], "input": string(b)})
		os.Exit(0)
	}
}

func hookHelperExecutable(t *testing.T) string {
	t.Helper()
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	name := "hop ' $x %VALUE% & test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(t.TempDir(), name)
	if err = os.WriteFile(target, b, 0700); err != nil {
		t.Fatal(err)
	}
	return target
}

var hookShellDirArgs = []string{"--config-dir", filepath.FromSlash("C:/Users/alice/config ' $x %VALUE% & 日本語"), "--state-dir", filepath.FromSlash("C:/Users/alice/state ' $x %VALUE% & 日本語")}

func assertHookShellTransport(t *testing.T, cmd *exec.Cmd, profile string) {
	t.Helper()
	const input = `{"session_id":"s","transcript_path":"/home/alice/日本語-é-🙂.jsonl","hook_event_name":"SessionStart"}`
	cmd.Env = append(os.Environ(), "HOPSESH_HOOK_TEST_HELPER=1")
	cmd.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("shell: %v; stdout=%q; stderr=%q", err, b, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("quiet hook emitted diagnostics or progress: %q", stderr.String())
	}
	if !utf8.Valid(b) {
		t.Fatalf("hook stdout is not UTF-8: %q", b)
	}
	var got struct {
		Args  []string `json:"args"`
		Input string   `json:"input"`
	}
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatalf("shell output: %v: %q", err, b)
	}
	if strings.TrimSpace(got.Input) != input {
		t.Fatalf("stdin changed: %q", got.Input)
	}
	if len(got.Args) < 4 || got.Args[0] != "notice-hook" || got.Args[1] != "--agent" || got.Args[2] != "claude" {
		t.Fatalf("args changed: %q", got.Args)
	}
	n := 5
	if runtime.GOOS == "windows" {
		n = 4
	}
	if len(got.Args) != n+len(hookShellDirArgs) || !reflect.DeepEqual(got.Args[n:], hookShellDirArgs) {
		t.Fatalf("directory arguments changed: %q", got.Args)
	}
	got.Args = got.Args[:n]
	if runtime.GOOS == "windows" {
		if len(got.Args) != 4 || got.Args[3] != "--profile="+profile {
			t.Fatalf("profile changed: %q", got.Args)
		}
	} else {
		if len(got.Args) != 5 || got.Args[3] != "--profile" || got.Args[4] != profile {
			t.Fatalf("profile changed: %q", got.Args)
		}
	}
}

func TestNoticeHookPOSIXShellTransport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell contract")
	}
	bin := hookHelperExecutable(t)
	for _, profile := range []string{"", "profile ' $x ; echo bad"} {
		command, err := NoticeHookCommand(bin, "claude", profile, hookShellDirArgs...)
		if err != nil {
			t.Fatal(err)
		}
		assertHookShellTransport(t, exec.Command("/bin/sh", "-c", command), profile)
	}
}
