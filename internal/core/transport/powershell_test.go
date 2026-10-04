package transport

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// A long script is uploaded and run from a file in the home folder, by a short command
// that stays inside cmd.exe's limit and removes the file afterwards.
func TestPowerShellFromFile(t *testing.T) {
	if len(PowerShellCommand(longScript(400))) <= maxCommandLine {
		t.Fatal("the test script fits on a command line; it tests nothing")
	}
	name := psScriptName()
	if !strings.HasPrefix(name, ".hopsesh-") || !strings.HasSuffix(name, ".ps1") || name == psScriptName() {
		t.Fatalf("script name %q", name)
	}
	run := psFromFile(name)
	if len(PowerShellCommand(run)) > maxCommandLine || !strings.Contains(run, "'"+name+"'") || !strings.Contains(run, "Remove-Item") {
		t.Fatalf("the runner: %s", run)
	}
}

// Against a real Windows OpenSSH server (cmd.exe as its shell), set by CI's Windows job:
// a script far over the command-line limit runs whole, non-ASCII output intact, and its
// uploaded file is gone afterwards.
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
	left, err := c.RunPowerShell(t.Context(), "@(Get-ChildItem -Force -LiteralPath $HOME -Filter '.hopsesh-*.ps1').Count")
	if err != nil || strings.TrimSpace(string(left)) != "0" {
		t.Fatalf("uploaded scripts left in the home folder: %q, %v", left, err)
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
