package host

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestObserveLocalDoesNotRunVersionCommands(t *testing.T) {
	facts := ObserveLocal(context.Background(), []agent.Spec{{Binaries: []agent.Binary{{Name: "go", VersionArgs: []string{"version"}}}}})
	if facts.Binaries["go"].Path == "" {
		t.Fatal("installed binary not resolved")
	}
	for name, binary := range facts.Binaries {
		if binary.Version != "" {
			t.Fatalf("passive probe ran %s: %s", name, binary.Version)
		}
	}
}

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

// A cloud driver runs without the variables its cloud must not inherit.
func TestUnsetting(t *testing.T) {
	t.Setenv("HOPSESH_TEST_MARKER", "set")
	m := &Machine{Name: "here", Local: true}
	h := Unsetting(&moduleHost{m: m}, []string{"HOPSESH_TEST_MARKER"})
	argv := []string{"sh", "-c", "echo marker=${HOPSESH_TEST_MARKER-unset}"}
	if runtime.GOOS == "windows" {
		argv = []string{"cmd", "/c", "set HOPSESH_TEST_MARKER"}
	}
	r, err := h.Exec().Run(context.Background(), argv, agent.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out := string(r.Stdout)
	if strings.Contains(out, "set") && !strings.Contains(out, "unset") || runtime.GOOS != "windows" && !strings.Contains(out, "marker=unset") {
		t.Fatalf("the variable reached the program: %q", out)
	}
}
