package cli

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func TestUnverifiedReturnExplainsSameBranchReview(t *testing.T) {
	var out bytes.Buffer
	r := &run{out: &out}
	r.renderPlan(&move.Plan{ReviewNewSession: true, Options: move.Options{TargetSession: "claude@work/original", TargetProfile: "work"}})
	for _, text := range []string{"does not mean you changed accounts", "remove --target-session and add --new-session", "same lineage branch", "independent destination work still requires --keep-both"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("missing %q in %s", text, out.String())
		}
	}
}

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
	c := app.ReturnCandidate{Machine: "studio", Agent: "claude", Profile: "personal", Key: "claude@personal/original"}
	for _, tc := range []struct{ status, posix, powershell string }{
		{"diverged", "On studio: hopsesh plan laptop:codex@work/child --in claude --target-profile personal --target-session claude@personal/original --keep-both", "On studio: hopsesh plan 'laptop:codex@work/child' --in 'claude' --target-profile 'personal' --target-session 'claude@personal/original' --keep-both"},
		{"behind", "hopsesh show studio:claude@personal/original", "hopsesh show 'studio:claude@personal/original'"},
		{"missing", "On studio: New session (original missing): hopsesh plan laptop:codex@work/child --in claude --target-profile personal --new-session", "On studio: New session (original missing): hopsesh plan 'laptop:codex@work/child' --in 'claude' --target-profile 'personal' --new-session"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			c.Status = tc.status
			want := tc.posix
			if runtime.GOOS == "windows" {
				want = tc.powershell
			}
			if got := returnReviewCommand(e, c); got != want {
				t.Fatalf("got %q; want %q", got, want)
			}
		})
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
