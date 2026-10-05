//go:build windows

package winshim

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The test binary also plays the program behind an npm shim: with WINSHIM_TEST_CHILD set,
// it prints its arguments as JSON.
func TestMain(m *testing.M) {
	if os.Getenv("WINSHIM_TEST_CHILD") != "" {
		b, _ := json.Marshal(os.Args[1:])
		fmt.Println(string(b))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// NpmShim writes an npm command shim, claude.cmd, in a new folder put first on PATH, for a
// program that is this test binary; it returns the shim and the program.
func npmShim(t *testing.T) (shim, prog string) {
	t.Helper()
	dir := t.TempDir()
	prog = filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(prog), 0o700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(prog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	dst.Close()
	shim = filepath.Join(dir, "claude.cmd")
	body := "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n" +
		`"%dp0%\node_modules\@anthropic-ai\claude-code\bin\claude.exe"   %*` + "\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return shim, prog
}

// On this Windows, claude found on PATH as npm's claude.cmd runs as the program behind it,
// which gets its arguments exactly, cmd.exe's special characters included.
func TestArgvRunsTheProgramBehindTheShim(t *testing.T) {
	_, prog := npmShim(t)
	args := []string{"--cloud", `[hopsesh] fix "the bug" & say 100% done ^ | <x>`}
	argv, err := Argv(append([]string{"claude"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(argv[0], prog) || !slices.Equal(argv[1:], args) {
		t.Fatalf("got %q, want %s and %q", argv, prog, args)
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Env = append(os.Environ(), "WINSHIM_TEST_CHILD=1")
	out, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil || !slices.Equal(got, args) {
		t.Fatalf("the program got %s (%v), want %q", out, err, args)
	}
}
