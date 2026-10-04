package scenario

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
	"github.com/rogpeppe/go-internal/testscript"

	"github.com/roeehrl/hopsesh/internal/core/term"
)

// atTerminal runs a program in a pseudo-terminal, as in a user's terminal, and plays the
// user there: at-terminal [-answer yes|no|none] [-paste LINK] PROG ARGS... When Claude
// Code's question whether the folder is trusted shows, the user answers it (Enter or 2),
// or leaves it; when hopsesh asks for a session's link, the user pastes LINK (or presses
// Enter). The test types here, as the user would; hopsesh never does. What the terminal
// showed goes to stdout as plain text.
func atTerminal(ts *testscript.TestScript, neg bool, args []string) {
	answer, paste := "\r", ""
	for len(args) > 1 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-answer":
			answer = map[string]string{"yes": "\r", "no": "2", "none": ""}[args[1]]
		case "-paste":
			paste = args[1]
		default:
			ts.Fatalf("at-terminal: unknown flag %s", args[0])
		}
		args = args[2:]
	}
	if len(args) == 0 {
		ts.Fatalf("usage: at-terminal [-answer yes|no|none] [-paste LINK] PROG ARGS...")
	}
	prog := args[0]
	for _, d := range filepath.SplitList(ts.Getenv("PATH")) {
		if p := filepath.Join(d, args[0]); fileExists(p) {
			prog = p
			break
		}
	}
	pty, err := xpty.NewPty(100, 30)
	ts.Check(err)
	defer pty.Close()
	cmd := exec.Command(prog, args[1:]...)
	cmd.Dir = ts.Getenv("WORK")
	cmd.Env = os.Environ()
	for _, k := range []string{"HOME", "USERPROFILE", "PATH", "TMPDIR", "WORK", "HOPSESH_CONFIG_DIR", "HOPSESH_STATE_DIR", "HOPSESH_MACHINE", "HOPSESH_TAILSCALE",
		"FAKE_AGENT_LOG", "FAKE_CLOUD_DIR", "FAKE_CLOUD_FAIL", "FAKE_CLAUDE_TRUSTED", "FAKE_CLAUDE_WRAP", "GIT_CONFIG_GLOBAL", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "CLAUDE_CODE_CHILD_SESSION", "ANTHROPIC_API_KEY", "CCR_FORCE_BUNDLE"} {
		cmd.Env = append(cmd.Env, k+"="+ts.Getenv(k))
	}
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	setCtty(cmd)
	ts.Check(pty.Start(cmd))

	emu := vt.NewEmulator(100, 30)
	var mu sync.Mutex
	var shown bytes.Buffer
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				mu.Lock()
				_, _ = emu.Write(buf[:n])
				shown.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { // the terminal's answers to the program's queries
		buf := make([]byte, 1024)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				_, _ = pty.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	answered, pasted := false, false
	deadline := time.After(90 * time.Second)
	var werr error
wait:
	for {
		select {
		case werr = <-done:
			break wait
		case <-deadline:
			_ = cmd.Process.Kill()
			ts.Fatalf("at-terminal: %s did not end; the terminal showed:\n%s", args[0], term.Plain(shown.Bytes()))
		case <-time.After(50 * time.Millisecond):
		}
		mu.Lock()
		text := term.Plain(shown.Bytes())
		mu.Unlock()
		if !answered && strings.Contains(text, "Quick safety check") {
			answered = true
			if answer != "" {
				_, _ = pty.Write([]byte(answer))
			}
		}
		if !pasted && strings.Contains(text, "paste its link (or press Enter to stop)") {
			pasted = true
			_, _ = pty.Write([]byte(paste + "\r"))
		}
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	_, _ = ts.Stdout().Write([]byte(term.Plain(shown.Bytes())))
	mu.Unlock()
	if neg && werr == nil {
		ts.Fatalf("at-terminal: %s succeeded unexpectedly", args[0])
	}
	if !neg && werr != nil {
		ts.Fatalf("at-terminal: %s: %v", args[0], werr)
	}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
