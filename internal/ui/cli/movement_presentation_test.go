package cli

import (
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"strings"
	"testing"
)

func TestNotifyFlagUsesConfigUnlessExplicit(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, flag := range []string{"", "true", "false"} {
			cmd := pullCmd()
			if flag != "" {
				if err := cmd.Flags().Set("notify", flag); err != nil {
					t.Fatal(err)
				}
			}
			r := &run{app: &app.App{Cfg: config.Config{MovementNotices: &configured}}}
			o, err := r.pullOptions(cmd)
			if err != nil {
				t.Fatal(err)
			}
			want := configured
			if flag != "" {
				want = flag == "true"
			}
			if o.Notify != want {
				t.Fatalf("config=%t flag=%q got %t", configured, flag, o.Notify)
			}
		}
	}
}

func TestReturnReviewCommandsAreExactAndReadOnly(t *testing.T) {
	e := app.Entry{Machine: "laptop", Session: agent.Summary{Key: agent.SessionKey{Agent: "codex", Profile: "work", Session: "child"}}}
	c := app.ReturnCandidate{Machine: "studio", Agent: "claude", Profile: "personal", Key: "claude@personal/original", Status: "diverged"}
	cmd := returnReviewCommand(e, c)
	for _, want := range []string{"On studio:", "hopsesh plan laptop:codex@work/child", "--target-profile personal", "--target-session claude@personal/original", "--keep-both"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("missing %q: %s", want, cmd)
		}
	}
	c.Status = "behind"
	if got := returnReviewCommand(e, c); got != "hopsesh show studio:claude@personal/original" {
		t.Fatal(got)
	}
	c.Status = "missing"
	if got := returnReviewCommand(e, c); !strings.Contains(got, "--new-session") || strings.Contains(got, "--target-session") {
		t.Fatal(got)
	}
}

func TestPushPreservesExplicitReturnAccountAndNewReplica(t *testing.T) {
	cmd := pushCmd()
	for key, value := range map[string]string{"target-profile": "alice-second-account", "new-session": "true"} {
		if err := cmd.Flags().Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	r := &run{app: &app.App{Cfg: config.Defaults()}}
	o, err := r.pullOptions(cmd)
	if err != nil || o.TargetProfile != "alice-second-account" || !o.NewReplica {
		t.Fatalf("push lost the chosen return account or new-session intent: %+v %v", o, err)
	}
}
