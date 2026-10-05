//go:build windows

package term_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/term"
)

// The test binary also plays the program behind an npm shim: with TERM_TEST_CHILD set, it
// prints its arguments, one per line, between markers.
func TestMain(m *testing.M) {
	if os.Getenv("TERM_TEST_CHILD") != "" {
		fmt.Print("args-begin\r\n")
		for _, a := range os.Args[1:] {
			fmt.Printf("arg=%s\r\n", a)
		}
		fmt.Print("args-end\r\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A terminal step's driver installed by npm (claude.cmd) runs, under the relay, as the
// program behind the shim, with its arguments as they are: cmd.exe never reads them.
func TestRelayRunsNpmShimProgram(t *testing.T) {
	dir := t.TempDir()
	prog := filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(prog), 0o700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyFile(t, self, prog)
	shim := filepath.Join(dir, "claude.cmd")
	body := "@ECHO off\r\nGOTO start\r\n:find_dp0\r\nSET dp0=%~dp0\r\nEXIT /b\r\n:start\r\nSETLOCAL\r\nCALL :find_dp0\r\n" +
		`"%dp0%\node_modules\@anthropic-ai\claude-code\bin\claude.exe"   %*` + "\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--cloud", `fix "it" & say 100% ^done`}
	r := term.New(append([]string{shim}, args...), dir, append(os.Environ(), "TERM_TEST_CHILD=1"))
	r.Capture = &term.Capture{}
	r.SetStdin(strings.NewReader(""))
	var out bytes.Buffer
	r.SetStdout(&out)
	if err := r.Run(); errors.Is(err, term.ErrNoPseudoTerminal) {
		t.Skip("no ConPTY here")
	} else if err != nil {
		t.Fatal(err)
	}
	if r.Code != 0 {
		t.Fatalf("exit code %d; printed %q", r.Code, r.Capture.Text())
	}
	text := r.Capture.Text()
	for _, a := range args {
		if !strings.Contains(text, "arg="+a) {
			t.Errorf("the program did not get %q as it is: %q", a, text)
		}
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
}
