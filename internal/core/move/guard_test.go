package move

import (
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The source machine's guard decides whether the original can be blocked or warned: the
// reasons are always reported (the app shows the option off with them), and a plan that
// asks for it says it won't be protected.
func TestGuardDecidesProtection(t *testing.T) {
	src := &host.Machine{Name: "macbook", Facts: host.Facts{Binaries: map[string]agent.BinaryFact{}}}
	src.Facts.Binaries[host.Hopsesh.Name] = agent.BinaryFact{Path: "/x/hopsesh"}
	in := Input{Source: Side{Machine: src}}
	plan := func(g *Guard, notify, fork bool) *Plan {
		in.Guard = g
		p := &Plan{Source: Endpoint{Location: "macbook"}}
		markUnprotected(p, in, Options{Notify: notify, Fork: fork})
		return p
	}
	if p := plan(&Guard{Ready: true}, true, false); p.Unprotected || len(p.CannotProtect) > 0 || len(p.Warnings) > 0 {
		t.Fatalf("ready: %+v", p)
	}
	why := []string{"hopsesh there is 0.5.0; blocking needs 0.6.0 or newer"}
	if p := plan(&Guard{Why: why}, true, false); !p.Unprotected || len(p.CannotProtect) != 1 || !strings.Contains(p.Warnings[0], "0.5.0") {
		t.Fatalf("not ready, asked: %+v", p)
	}
	if p := plan(&Guard{Why: why}, false, false); p.Unprotected || len(p.CannotProtect) != 1 || len(p.Warnings) > 0 {
		t.Fatalf("not ready, not asked: the reasons are there, no warning: %+v", p)
	}
	if p := plan(&Guard{Why: why}, true, true); p.Unprotected || len(p.CannotProtect) > 0 {
		t.Fatalf("a fork leaves the original as it is: %+v", p)
	}
	// Without a guard, only whether hopsesh is there counts.
	if p := plan(nil, true, false); p.Unprotected {
		t.Fatalf("hopsesh there: %+v", p)
	}
	src.Facts.Binaries = nil
	if p := plan(nil, true, false); !p.Unprotected || p.CannotProtect[0] != "hopsesh isn't installed there" {
		t.Fatalf("no hopsesh: %+v", p)
	}
}
