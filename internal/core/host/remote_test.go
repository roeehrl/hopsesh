package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// fakeSSH puts an ssh on PATH that runs the remote command line here with sh.
func fakeSSH(t *testing.T) *transport.Conn {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell shim")
	}
	dir := t.TempDir()
	shim := `#!/bin/sh
if [ "$1" = "-G" ]; then printf 'hostname 127.0.0.1\nport 22\nuser me\n'; exit 0; fi
while [ $# -gt 0 ] && [ "$1" != "--" ]; do shift; done
shift
exec sh -c "$*"
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	c, err := transport.NewConn("box", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A program on another machine gets its input and keeps it open until it has answered
// (it would stop at the end of input first), as codex app-server needs.
func TestRemoteRunWithInput(t *testing.T) {
	m := &Machine{Name: "box", Conn: fakeSSH(t), Facts: Facts{OS: "linux"}}
	ctx := context.Background()
	// Answers only after a delay, and stops at the end of its input.
	prog := []string{"sh", "-c", `read a; sleep 1; echo "answer:$a"; cat >/dev/null`}
	start := time.Now()
	r, err := m.Exec().Run(ctx, prog, agent.RunOptions{Stdin: []byte("hello\n"), HoldStdin: 20 * time.Second, StdinUntil: []byte("answer:")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Stdout), "answer:hello") || r.Code != 0 {
		t.Fatalf("result: %+v %q", r, r.Stdout)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("the input must close as soon as the answer arrives, not after the whole hold")
	}
	r, err = m.Exec().Run(ctx, []string{"sh", "-c", "cat; exit 3"}, agent.RunOptions{Stdin: []byte("x"), Env: []string{"A=b c"}, Dir: "/"})
	if err != nil || string(r.Stdout) != "x" || r.Code != 3 {
		t.Fatalf("exit code and output: %+v %v", r, err)
	}
}

func TestWindowsCommandLine(t *testing.T) {
	e := remoteExec{m: &Machine{Facts: Facts{OS: "windows"}}}
	line := e.commandLine([]string{`C:\Program Files\Codex\codex.exe`, "app-server"}, agent.RunOptions{Dir: `C:\Users\me`, Env: []string{"CODEX_HOME=C:\\Users\\me\\.codex"}})
	enc, ok := strings.CutPrefix(line, "powershell -NoProfile -NonInteractive -EncodedCommand ")
	if !ok {
		t.Fatalf("line: %s", line)
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	script := string(utf16.Decode(u))
	want := `Set-Location 'C:\Users\me'; $env:CODEX_HOME='C:\Users\me\.codex'; & 'C:\Program Files\Codex\codex.exe' 'app-server'; exit $LASTEXITCODE`
	if !strings.HasSuffix(script, want) {
		t.Fatalf("script: %s", script)
	}
}

// The interactive exchange must work locally and over SSH, including chunked output.
func TestProtocolExec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX transport shim; framing is covered on every OS")
	}
	for _, remote := range []bool{false, true} {
		var runner agent.Exec = localExec{}
		if remote {
			m := &Machine{Name: "box", Conn: fakeSSH(t), Facts: Facts{OS: "linux"}}
			defer m.Close()
			runner = m.Exec()
		}
		start := time.Now()
		result, err := runner.Run(context.Background(), []string{"sh", "-c", `read a; printf '{ "result": {}, '; printf '"id": 1 }\n'; read b; printf '{ "id":2, "result":{"value":"%s"} }\n' "$b"; cat >/dev/null`}, agent.RunOptions{Stdin: []byte("initialize\n"), HoldStdin: 10 * time.Second, Timeout: 15 * time.Second, StdinReply: func(line []byte) ([]byte, bool) {
			if strings.Contains(string(line), `"id": 1`) {
				return []byte("request\n"), false
			}
			return nil, strings.Contains(string(line), `"id":2`)
		}})
		if err != nil || result.Code != 0 || !strings.Contains(string(result.Stdout), `"value":"request"`) {
			t.Fatal(remote, result, err)
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("exchange waited for timeout instead of response")
		}
	}
}

func TestProtocolFramingHandlesChunks(t *testing.T) {
	var buf bytes.Buffer
	done := make(chan struct{})
	next := make(chan []byte, 1)
	writer := &watchWriter{buf: &buf, hit: done, next: next, reply: func(line []byte) ([]byte, bool) {
		if string(line) == "ready" {
			return []byte("request"), false
		}
		return nil, string(line) == "answer"
	}}
	for _, chunk := range []string{"rea", "dy\na", "nswer\n"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if string(<-next) != "request" {
		t.Fatal("missing next request")
	}
	select {
	case <-done:
	default:
		t.Fatal("missing completion")
	}
}
