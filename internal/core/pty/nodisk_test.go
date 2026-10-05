package pty

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// What a tab prints stays in memory: no file in this package writes, logs or journals
// anything (it imports no logger, audit log or journal, and calls nothing that creates or
// writes a file), and closing a tab zeroes its scrollback and capture.
func TestOutputNeverLeavesMemory(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	banned := map[string]bool{"log": true, "log/slog": true, "io/ioutil": true,
		"github.com/roeehrl/hopsesh/internal/core/audit": true, "github.com/roeehrl/hopsesh/internal/core/journal": true}
	bannedCalls := map[string]bool{"os.Create": true, "os.CreateTemp": true, "os.WriteFile": true, "os.OpenFile": true, "os.Mkdir": true, "os.MkdirAll": true}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); banned[p] {
				t.Errorf("%s imports %s", f, p)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && bannedCalls[id.Name+"."+sel.Sel.Name] {
					t.Errorf("%s: %s.%s", fset.Position(sel.Pos()), id.Name, sel.Sel.Name)
				}
			}
			return true
		})
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(Options{})
	defer m.CloseAll()
	marker := "hopsesh-marker-7f3c"
	s, err := m.Start(Spec{Argv: []string{self, "-test.run=^$"}, Dir: t.TempDir(), Capture: CaptureStep,
		Env: Env{Set: []string{"PTY_TEST_ROLE=print", "PTY_TEST_TEXT=" + marker + "\r\n"}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("the program did not end")
	}
	s.mu.Lock()
	kept := bytes.Contains(s.scroll.bytes(), []byte(marker)) && strings.Contains(s.capture.Text(), marker)
	scroll, captured := s.scroll.buf[:cap(s.scroll.buf)], s.capture
	s.mu.Unlock()
	if !kept {
		t.Fatal("the tab did not keep its output while open")
	}
	if err := m.Close(s.ID()); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(scroll, []byte(marker)) || bytes.ContainsFunc(scroll, func(r rune) bool { return r != 0 }) {
		t.Error("the scrollback was not zeroed")
	}
	if captured.Text() != "" {
		t.Error("the capture was not reset")
	}
}

func TestRing(t *testing.T) {
	r := newRing(8)
	r.write([]byte("abc"))
	if string(r.bytes()) != "abc" {
		t.Fatal(string(r.bytes()))
	}
	r.write([]byte("defgh"))
	if string(r.bytes()) != "abcdefgh" {
		t.Fatal(string(r.bytes()))
	}
	r.write([]byte("ij"))
	if string(r.bytes()) != "cdefghij" {
		t.Fatal(string(r.bytes()))
	}
	r.write([]byte("0123456789"))
	if string(r.bytes()) != "23456789" {
		t.Fatal(string(r.bytes()))
	}
	r.write([]byte("x"))
	if string(r.bytes()) != "3456789x" {
		t.Fatal(string(r.bytes()))
	}
	r.wipe()
	if len(r.bytes()) != 0 || bytes.ContainsFunc(r.buf[:cap(r.buf)], func(r rune) bool { return r != 0 }) {
		t.Fatal("not wiped")
	}
}

func TestNotifier(t *testing.T) {
	for in, want := range map[string]Signal{
		"plain text":                             SignalNone,
		"ring\a":                                 SignalBell,
		"\x1b]0;title\a":                         SignalNone, // BEL ends an OSC, it is no bell
		"\x1b]9;Build finished\x1b\\":            SignalNotification,
		"\x1b]9;Done\a":                          SignalNotification,
		"\x1b]9;4;1;50\x1b\\":                    SignalNone, // ConEmu progress
		"\x1b]9;12\a":                            SignalNone,
		"\x1b]777;notify;Claude;needs you\a":     SignalNotification,
		"\x1b]777;preexec\a":                     SignalNone,
		"\x1b]99;i=1;hello\x1b\\":                SignalNotification,
		"\x1bP+q544e\a\x1b\\":                    SignalNone, // a BEL inside DCS
		"\x1b[31mred\x1b[0m":                     SignalNone,
		"\x1b]8;;https://example.com\x1b\\x\a":   SignalBell,
		"\x1b]9;Build \x1b[31m":                  SignalNone, // an OSC cut by another ESC
		"\x1b_Gdata\a\x1b\\":                     SignalNone, // APC
		"\x1b]9;":                                SignalNone,
		"\x1b]133;A\a":                           SignalNone,
		"\x1b]1337;SetUserVar=x\a\x1b]9;hey\x07": SignalNotification,
	} {
		var d notifier
		if got := d.feed([]byte(in)); got != want {
			t.Errorf("feed(%q) = %v, want %v", in, got, want)
		}
	}
	// Across reads.
	var d notifier
	sig := SignalNone
	for _, c := range []byte("\x1b]777;notify;t;b\x1b\\") {
		if s := d.feed([]byte{c}); s > sig {
			sig = s
		}
	}
	if sig != SignalNotification {
		t.Error("a notification split across reads was missed")
	}
}
