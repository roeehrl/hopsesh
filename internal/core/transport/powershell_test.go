package transport

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A script that fits goes on the command line; a longer one through standard input, behind
// a short command that stays inside cmd.exe's limit.
func TestPowerShellInvocation(t *testing.T) {
	cmd, stdin := PowerShellInvocation("Get-Date")
	if stdin != nil || !strings.HasPrefix(cmd, "powershell ") {
		t.Fatalf("a short script: %q, stdin %q", cmd, stdin)
	}
	long := longScript(400)
	cmd, stdin = PowerShellInvocation(long)
	if len(cmd) > maxCommandLine {
		t.Fatalf("the command line is %d characters", len(cmd))
	}
	got, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(stdin)))
	if err != nil || string(got) != long {
		t.Fatalf("standard input does not carry the script (%v)", err)
	}
}

// What RunInput is given reaches the remote command's standard input.
func TestRunInputSendsStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in ssh is a shell script")
	}
	fake := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Conn{Dest: "box", sshBinary: fake}
	out, err := c.RunInput(t.Context(), "anything", []byte("hello\n"))
	if err != nil || string(out) != "hello\n" {
		t.Fatalf("got %q, %v", out, err)
	}
}

// Against a real Windows OpenSSH server (cmd.exe as its shell), set by CI's Windows job:
// a script far over the command-line limit runs whole, non-ASCII output intact.
func TestRunPowerShellOverSSH(t *testing.T) {
	dest := os.Getenv("HOPSESH_TEST_WINDOWS_SSH")
	if dest == "" {
		t.Skip("HOPSESH_TEST_WINDOWS_SSH is not set")
	}
	c, err := NewConn(dest, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	keys, _, err := c.ScanHostKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Trust(keys); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{3, 400} {
		out, err := c.RunPowerShell(t.Context(), longScript(n))
		if err != nil {
			t.Fatalf("%d folders: %v: %s", n, err, out)
		}
		lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r", "")), "\n")
		if len(lines) != n || lines[n-1] != fmt.Sprintf("フォルダ-%d:False", n-1) {
			t.Fatalf("%d folders: got %d lines, the last %q", n, len(lines), lines[len(lines)-1])
		}
	}
}

// longScript is like the git probe: a loop over n quoted folder paths, on several lines.
func longScript(n int) string {
	var paths []string
	for i := range n {
		paths = append(paths, PSQuote(fmt.Sprintf(`C:\Users\someone\git\フォルダ-%d`, i)))
	}
	return "[Console]::OutputEncoding = [Text.Encoding]::UTF8\n" +
		"foreach ($p in @(" + strings.Join(paths, ",") + ")) {\n" +
		"  (Split-Path -Leaf $p) + ':' + (Test-Path -LiteralPath $p)\n}"
}
