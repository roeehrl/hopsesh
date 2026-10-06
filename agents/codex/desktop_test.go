package codex

import (
	"context"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"reflect"
	"strings"
	"testing"
)

func TestDesktopResumeExactThread(t *testing.T) {
	key := agent.SessionKey{Agent: id, Session: t1}
	for _, tc := range []struct {
		os, app string
		want    []string
	}{
		{"darwin", "/Applications/Codex.app", []string{"/usr/bin/open", "-a", "/Applications/Codex.app", "codex://threads/" + t1}},
		{"windows", `C:\Users\alice\AppData\Local\Programs\Codex\Codex.exe`, []string{"rundll32.exe", "url.dll,FileProtocolHandler", "codex://threads/" + t1}},
		{"linux", "/usr/share/applications/codex.desktop", []string{"xdg-open", "codex://threads/" + t1}},
	} {
		t.Run(tc.os, func(t *testing.T) {
			in := agent.Install{OS: tc.os, Desktop: tc.app}
			m := New()
			if err := m.CheckApp(in, key, agent.ResumeOptions{App: true}); err != nil {
				t.Fatal(err)
			}
			c := m.Resume(in, key, agent.Placement{}, agent.ResumeOptions{App: true})
			if !reflect.DeepEqual(c.Argv, tc.want) || len(c.Env) > 0 || c.Dir != "" {
				t.Fatalf("desktop launch: %+v", c)
			}
		})
	}
}
func TestDesktopRejectsWrongProfileOrUnsupportedOptions(t *testing.T) {
	m := New()
	in := agent.Install{OS: "darwin", Desktop: "/Applications/Codex.app"}
	key := agent.SessionKey{Agent: id, Session: t1}
	for _, bad := range []string{"new", "../../settings", "x?prompt=send", "';&echo unsafe", ""} {
		k := key
		k.Session = agent.SessionID(bad)
		if err := m.CheckApp(in, k, agent.ResumeOptions{}); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	for _, o := range []agent.ResumeOptions{{App: true, Fork: true}, {App: true, Prompt: "Continue."}} {
		if err := m.CheckApp(in, key, o); err == nil {
			t.Fatal("ignored unsupported desktop options")
		}
	}
	in.Profile = &agent.RuntimeProfile{ID: "other", Root: "/home/alice/account-b"}
	if err := m.CheckApp(in, key, agent.ResumeOptions{}); err == nil {
		t.Fatal("opened sibling account in default app")
	}
	in.Profile = nil
	in.Desktop = ""
	if err := m.CheckApp(in, key, agent.ResumeOptions{}); err == nil {
		t.Fatal("missing app accepted")
	}
	if c := m.Resume(in, key, agent.Placement{}, agent.ResumeOptions{App: true}); len(c.Argv) > 0 {
		t.Fatalf("missing desktop fell back to terminal: %+v", c)
	}
}
func TestDesktopDetectHonorsRoot(t *testing.T) {
	h := agenttest.NewFakeHost("/home/alice")
	// A desktop file without a registered URL handler must not be advertised.
	if err := h.FS().WriteFile("/usr/share/applications/codex.desktop", []byte("[Desktop Entry]"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.FS().WriteFile("/home/alice/.codex/sessions/empty", nil, 0600); err != nil {
		t.Fatal(err)
	}
	in, err := New().Detect(context.Background(), h)
	if err != nil || in.Desktop != "" {
		t.Fatalf("default: %+v %v", in, err)
	}
	in.Roots[home] = "/home/alice/other-codex"
	in.Desktop = ""
	detectDesktop(h, &in)
	if in.Desktop != "" || !strings.Contains(in.DesktopWhy, "account root") {
		t.Fatalf("custom root incorrectly accepted: %+v", in)
	}
}

type desktopFactsHost struct {
	agent.Host
	facts agent.Facts
}

func (h desktopFactsHost) Facts() agent.Facts { return h.facts }
func TestDesktopDetectRegisteredHandler(t *testing.T) {
	base := agenttest.NewFakeHost("/home/alice")
	facts := base.Facts()
	facts.DesktopProtocols = map[string]bool{"codex": true}
	h := desktopFactsHost{Host: base, facts: facts}
	in := agent.Install{Roots: map[string]string{home: "/home/alice/.codex"}}
	detectDesktop(h, &in)
	if in.Desktop == "" || in.DesktopWhy != "" {
		t.Fatal("registered handler not detected", in)
	}
	in.Roots[home] = "/home/alice/other"
	detectDesktop(h, &in)
	if in.Desktop != "" {
		t.Fatal("handler ignored nondefault root")
	}
}

func TestDesktopDetectDoesNotMistakeChatGPTForCodex(t *testing.T) {
	base := agenttest.NewFakeHost("/home/alice")
	facts := base.Facts()
	facts.OS = "darwin"
	h := desktopFactsHost{Host: base, facts: facts}
	file := "/Applications/ChatGPT.app/Contents/Info.plist"
	if err := base.FS().WriteFile(file, []byte(`<plist><dict><key>CFBundleIdentifier</key><string>com.openai.chat</string></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	in := agent.Install{Roots: map[string]string{home: "/home/alice/.codex"}}
	detectDesktop(h, &in)
	if in.Desktop != "" {
		t.Fatal("unrelated ChatGPT app offered as Codex")
	}
	if err := base.FS().WriteFile(file, []byte(`<plist><dict><key>CFBundleIdentifier</key><string>com.openai.codex</string></dict></plist>`), 0600); err != nil {
		t.Fatal(err)
	}
	detectDesktop(h, &in)
	if in.Desktop != "/Applications/ChatGPT.app" {
		t.Fatal("Codex bundle not found", in)
	}
}
