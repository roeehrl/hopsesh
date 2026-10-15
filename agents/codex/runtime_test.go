package codex

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

func TestDetectBundledAppRuntime(t *testing.T) {
	const bundle = "/Applications/ChatGPT.app"
	const bin = bundle + "/Contents/Resources/codex-cli/bin/codex"
	setup := func(t *testing.T, bundleID, pkg string, cli bool) agent.Install {
		base := agenttest.NewFakeHost("/home/alice")
		base.Put("/home/alice/.codex/sessions/.keep", nil, time.Time{})
		base.Put(bundle+"/Contents/Info.plist", []byte(`<plist><dict><key>CFBundleIdentifier</key><string>`+bundleID+`</string><key>CFBundleShortVersionString</key><string>26.1007.30912</string></dict></plist>`), time.Time{})
		base.Put(bundle+"/Contents/Resources/codex-cli/codex-package.json", []byte(pkg), time.Time{})
		base.AddBinaryAt("codex", bin, "codex-cli 0.162.0-alpha.17.2")
		if cli {
			base.AddBinary("codex", "codex-cli 0.150.0")
		}
		facts := base.Facts()
		facts.OS = "darwin"
		in, err := New().Detect(context.Background(), desktopFactsHost{Host: base, facts: facts})
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	pkg := `{"layoutVersion":1,"version":"0.162.0-alpha.17.2","target":"aarch64-apple-darwin","variant":"codex","entrypoint":"bin/codex"}`
	in := setup(t, "com.openai.codex", pkg, false)
	if in.Binary != bin || in.Runtime != agent.RuntimeApp || in.Version != "0.162.0-alpha.17.2" || in.Desktop != bundle || in.DesktopVersion != "26.1007.30912" {
		t.Fatalf("bundled runtime: %+v", in)
	}
	if hi := New().MovementHooks(agenttest.NewFakeHost("/home/alice"), in, "/opt/hopsesh"); hi.Reason != "" {
		t.Fatalf("bundled runtime refused hooks: %s", hi.Reason)
	}
	key := agent.SessionKey{Agent: id, Session: t1}
	if c := New().Resume(in, key, agent.Placement{CWD: "/w"}, agent.ResumeOptions{}); c.Argv[0] != bin {
		t.Fatalf("terminal resume must run the bundled runtime: %v", c.Argv)
	}
	// A CLI on PATH or a CLI candidate wins over the app's runtime.
	if in := setup(t, "com.openai.codex", pkg, true); in.Binary != "/bin/codex" || in.Runtime != "" || in.Version != "0.150.0" || in.Desktop != bundle {
		t.Fatalf("CLI not preferred: %+v", in)
	}
	for name, bad := range map[string][2]string{
		"unrelated ChatGPT build": {"com.openai.chat", pkg},
		"entrypoint elsewhere":    {"com.openai.codex", strings.Replace(pkg, `"bin/codex"`, `"bin/other"`, 1)},
		"entrypoint escapes":      {"com.openai.codex", strings.Replace(pkg, `"bin/codex"`, `"../../../../../bin/codex"`, 1)},
		"absolute entrypoint":     {"com.openai.codex", strings.Replace(pkg, `"bin/codex"`, `"`+bin+`"`, 1)},
		"no package":              {"com.openai.codex", `not json`},
	} {
		if in := setup(t, bad[0], bad[1], false); in.Binary != "" || in.Runtime != "" || in.Version != "" {
			t.Errorf("%s: accepted %+v", name, in)
		}
	}
	// The package's version wins; an unparseable one falls back to the probed line.
	if in := setup(t, "com.openai.codex", strings.Replace(pkg, `"0.162.0-alpha.17.2"`, `"next"`, 1), false); in.Version != "0.162.0-alpha.17.2" {
		t.Fatalf("version fallback: %+v", in)
	}
}
