package host

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestScopedDetectionUsesRemoteOriginalEnvironment(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		for _, explicit := range []bool{false, true} {
			t.Run(goos+map[bool]string{false: "/native", true: "/explicit"}[explicit], func(t *testing.T) {
				home := "/remote/alice"
				if goos == "windows" {
					home = `C:\Users\remote-alice`
				}
				pa := agent.PathFor(goos)
				root := pa.Join(home, ".claude")
				env := map[string]string{"CLAUDE_CONFIG_DIR": ""}
				if explicit {
					env["CLAUDE_CONFIG_DIR"] = root
				}
				m := NewSnapshot("remote", Facts{Home: home, OS: goos, Env: env}, nil)
				mod := claude.New()
				p := &agent.RuntimeProfile{ID: "default", Root: root, Default: true}
				// Refresh starts with no detection metadata, so this first host
				// injects the pinned root even for a native default profile.
				h, err := m.For(context.Background(), mod.Spec(), agent.Install{Agent: "claude", Profile: p, Roots: map[string]string{"home": root}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				if h.Facts().Env["CLAUDE_CONFIG_DIR"] != root || h.Facts().UserEnv == nil || h.Facts().UserEnv["CLAUDE_CONFIG_DIR"] != env["CLAUDE_CONFIG_DIR"] {
					t.Fatalf("lost original environment: %+v", h.Facts())
				}
				in, err := mod.Detect(context.Background(), h)
				if err != nil {
					t.Fatal(err)
				}
				in.Profile = p
				if (in.NativeDefaultRoot != "") == explicit || !explicit && in.NativeDefaultRoot != root {
					t.Fatalf("wrong remote metadata: %+v", in)
				}
				b, err := json.Marshal(in)
				if err != nil {
					t.Fatal(err)
				}
				in = agent.Install{}
				if err := json.Unmarshal(b, &in); err != nil {
					t.Fatal(err)
				}
				// ProfileSpec is module-owned, not peer launch policy.
				in.Accounts = mod.Spec().Accounts
				wantEnv := []string(nil)
				if explicit {
					wantEnv = []string{"CLAUDE_CONFIG_DIR=" + root}
				}
				if !slices.Equal(in.ProfileEnv(), wantEnv) {
					t.Fatalf("wire metadata lost scoping: %+v", in)
				}
				if env["CLAUDE_CONFIG_DIR"] != map[bool]string{false: "", true: root}[explicit] {
					t.Fatal("profile scoping mutated machine environment")
				}
			})
		}
	}
}
