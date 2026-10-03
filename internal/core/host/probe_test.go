package host

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestPosixProbe(t *testing.T) {
	specs := []agent.Spec{{
		Roots:    []agent.Root{{Name: "home", Env: []string{"FAKE_AGENT_HOME"}}},
		LoginEnv: []string{"FAKE_AGENT_HOME", "bad name; rm -rf /"},
		Binaries: []agent.Binary{{Name: "sh", Candidates: map[string][]string{"*": {"~/not here/sh"}}, VersionArgs: []string{"-c", "echo 'v1.2.3 (fake)'"}}},
	}}
	script := posixProbe(wants(specs))
	if strings.Contains(script, "rm -rf") {
		t.Fatal("invalid variable names must not reach the script")
	}
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = []string{"HOME=/home/x", "PATH=/bin:/usr/bin", "FAKE_AGENT_HOME=/data/agent dir"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	f := parseProbe(out)
	if f.Home != "/home/x" || f.Env["FAKE_AGENT_HOME"] != "/data/agent dir" {
		t.Fatalf("facts: %+v", f)
	}
	sh := f.Binaries["sh"]
	if sh.Path == "" || sh.Version != "v1.2.3 (fake)" {
		t.Fatalf("binary: %+v", sh)
	}
}

// Git for Windows, MSYS2 and Cygwin answer uname on Windows; those machines are Windows.
func TestWindowsUnameIsWindows(t *testing.T) {
	for _, u := range []string{"MINGW64_NT-10.0-26100", "MSYS_NT-10.0", "CYGWIN_NT-10.0-26100\r\n"} {
		if !windowsUname(u) {
			t.Errorf("%q is Windows", u)
		}
	}
	for _, u := range []string{"Linux", "Darwin", "FreeBSD"} {
		if windowsUname(u) {
			t.Errorf("%q is not Windows", u)
		}
	}
}
