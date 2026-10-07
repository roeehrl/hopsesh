package claude

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

type desktopHost struct {
	agent.Host
	facts agent.Facts
}

func (h desktopHost) Facts() agent.Facts { return h.facts }

func TestDesktopGatesAndExactSession(t *testing.T) {
	for _, tc := range []struct{ name, os, arch, version, root, why string }{
		{"mac", "darwin", "arm64", "2.1.289", "", ""},
		{"windows", "windows", "amd64", "2.1.285", "", ""},
		{"linux", "linux", "amd64", "2.1.289", "", "macOS or Windows"},
		{"windows-arm", "windows", "arm64", "2.1.289", "", "macOS or Windows"},
		{"old", "darwin", "arm64", "2.1.284", "", "2.1.285"},
		{"unknown", "darwin", "arm64", "", "", "2.1.285"},
		{"alternate-root", "darwin", "arm64", "2.1.289", "/home/alice/second", "account root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := agenttest.NewFakeHost("/home/alice")
			f := base.Facts()
			f.OS, f.Arch = tc.os, tc.arch
			f.Binaries = map[string]agent.BinaryFact{"claude": {Path: "/test/claude", Version: tc.version}}
			f.Env = map[string]string{"CLAUDE_CONFIG_DIR": tc.root}
			m := New()
			in, err := m.Detect(context.Background(), desktopHost{base, f})
			if err != nil {
				t.Fatal(err)
			}
			key := agent.SessionKey{Agent: id, Session: s1}
			o := agent.ResumeOptions{App: true}
			err = m.CheckApp(in, key, o)
			if tc.why != "" {
				if err == nil || !strings.Contains(err.Error(), tc.why) {
					t.Fatalf("gate: %v", err)
				}
				if c := m.Resume(in, key, agent.Placement{}, o); len(c.Argv) > 0 {
					t.Fatal("unsupported desktop fell back to a launch", c)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			c := m.Resume(in, key, agent.Placement{CWD: "/project"}, o)
			if !reflect.DeepEqual(c.Argv, []string{"/test/claude", "--desktop", "--resume", s1}) || !c.Wait || !c.TTY || c.Dir != "/project" {
				t.Fatalf("launch: %+v", c)
			}
			for _, bad := range []agent.ResumeOptions{{App: true, Fork: true}, {App: true, Prompt: "continue"}, {App: true, RemoteControl: true}} {
				if m.CheckApp(in, key, bad) == nil {
					t.Fatal("ignored conflicting options")
				}
			}
			for _, sid := range []agent.SessionID{"", "title", "../../x", "x?prompt=send"} {
				key.Session = sid
				if m.CheckApp(in, key, o) == nil {
					t.Fatal("accepted non-UUID", sid)
				}
			}
			key.Session = s1
			in.Profile = &agent.RuntimeProfile{ID: "other", Default: false}
			if m.CheckApp(in, key, o) == nil {
				t.Fatal("accepted nondefault account")
			}
		})
	}
}

func TestDesktopFocusRequiresCurrentAppOwnershipAndVerifiedBundle(t *testing.T) {
	base := agenttest.NewFakeHost("/home/alice")
	f := base.Facts()
	f.OS, f.Arch = "darwin", "arm64"
	f.Binaries = map[string]agent.BinaryFact{"claude": {Path: "/test/claude", Version: "2.1.289"}}
	h := desktopHost{base, f}
	key := agent.SessionKey{Agent: id, Session: s1}
	o := agent.ResumeOptions{App: true, AppRunning: true}
	m := New()
	for _, tc := range []struct {
		bundle, version string
		ok              bool
	}{
		{"com.example.other", "2.19675.1", false},
		{"com.anthropic.claudefordesktop", "2.110.0", false},
		{"com.anthropic.claudefordesktop", "", false},
		{"com.anthropic.claudefordesktop", "2.19675.1", true},
	} {
		if err := base.FS().WriteFile("/Applications/Claude.app/Contents/Info.plist", []byte(`<plist><dict><key>CFBundleIdentifier</key><string>`+tc.bundle+`</string><key>CFBundleShortVersionString</key><string>`+tc.version+`</string></dict></plist>`), 0600); err != nil {
			t.Fatal(err)
		}
		in, err := m.Detect(context.Background(), h)
		if err != nil {
			t.Fatal(err)
		}
		if err = m.CheckApp(in, key, o); (err == nil) != tc.ok {
			t.Fatalf("bundle %s version %s: %v", tc.bundle, tc.version, err)
		}
		if !tc.ok {
			continue
		}
		c := m.Resume(in, key, agent.Placement{}, o)
		want := []string{"/usr/bin/open", "-a", "/Applications/Claude.app", "claude://resume?session=" + s1}
		if !reflect.DeepEqual(c.Argv, want) || !c.Wait || c.TTY {
			t.Fatalf("focus: %+v", c)
		}
		// A saved/ended session must still use the supported CLI with ownership checks.
		o.AppRunning = false
		c = m.Resume(in, key, agent.Placement{}, o)
		o.AppRunning = true
		if !c.TTY || len(c.Argv) != 4 || c.Argv[1] != "--desktop" {
			t.Fatalf("bypassed CLI checks: %+v", c)
		}
	}
}
