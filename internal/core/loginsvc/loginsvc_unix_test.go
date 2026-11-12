//go:build darwin || linux

package loginsvc

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestInstallWritesTheServiceAndReportsWhatRuns(t *testing.T) {
	home := t.TempDir()
	var calls []string
	running := false
	s := Service{Home: home, Program: "/Users/me/my tools/hopsesh", Args: []string{"serve"}, Log: home + "/.hopsesh/run/serve.log",
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			line := name + " " + strings.Join(args, " ")
			calls = append(calls, line)
			switch {
			case strings.HasPrefix(line, "launchctl print gui/") && !strings.HasSuffix(line, Label):
				return nil, nil // a login session there
			case strings.Contains(line, "bootstrap"), strings.Contains(line, "enable --now"):
				running = true
			case strings.Contains(line, "print gui/") && running:
				return []byte("state = running\n"), nil
			case strings.Contains(line, "is-active") && running:
				return []byte("active\n"), nil
			case strings.Contains(line, "print"), strings.Contains(line, "is-active"):
				return []byte("inactive"), errors.New("exit 3")
			}
			return nil, nil
		}}
	if st := s.Check(context.Background()); st.Registered || st.Running {
		t.Fatalf("registered before install: %+v", st)
	}
	st, err := s.Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Registered || !st.Running || st.Program != "/Users/me/my tools/hopsesh" {
		t.Fatalf("%+v (calls %v)", st, calls)
	}
	if err := s.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	running = false
	if st := s.Check(context.Background()); st.Registered {
		t.Fatalf("still registered: %+v", st)
	}
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		if e.Name() != ".hopsesh" && e.Name() != "Library" && e.Name() != ".config" {
			t.Fatalf("left %s behind", e.Name())
		}
	}
}
