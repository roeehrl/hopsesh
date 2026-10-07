package e2e

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/profiles"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Exact matrix regression: a remote default profile was discovered before its
// first public account observation. The return receiver probes it locally, which
// rotates its binding. Unchanged native history is still comparable for a fresh
// portable return; this never grants permission to append under the new binding.
func TestMovementQuietReturnAfterAccountObservation(t *testing.T) {
	for _, from := range []string{"claude", "codex"} {
		for _, to := range []string{"claude", "codex"} {
			for _, originalWork := range []string{"unchanged", "appended", "rewritten"} {
				t.Run(from+"-"+to+"/"+originalWork, func(t *testing.T) {
					a, b, in := movementInput(t, from, to)
					ctx := context.Background()
					store := profiles.Store{Dir: t.TempDir()}
					for _, side := range []*move.Side{&in.Source, &in.Target} {
						root, err := filepath.EvalSymlinks(side.Install.Root("home"))
						if err != nil {
							t.Fatal(err)
						}
						endpoint, err := side.Machine.PrepareIdentity(ctx)
						if err != nil {
							t.Fatal(err)
						}
						profile, err := store.Register(agent.RuntimeProfile{Endpoint: endpoint, Agent: side.Install.Agent,
							Root: root, Name: side.Machine.Name + " default", Default: true})
						if err != nil {
							t.Fatal(err)
						}
						side.Install.Roots["home"] = root
						side.Install.Profile = &profile
					}
					in.Session = listAgent(t, a, in.Source.Module, in.Source.Install)[0]
					// Claude fixtures contain more than one session; pin the intended one.
					for _, s := range listAgent(t, a, in.Source.Module, in.Source.Install) {
						if string(s.Key.Session) == sid {
							in.Session = s
						}
					}
					in.Session.Key.Profile = in.Source.Install.ProfileID()
					env := move.Env{StateDir: t.TempDir()}
					p, _ := applyMovement(t, in, move.Options{TargetDir: b.repo, Mark: true, Notify: false}, env)
					dst := movementProfileSession(t, b, in.Target, p.Placement.Key)
					if to == "claude" {
						appendTurn(t, dst.Path, "BINDING-RETURN-WORK")
					} else {
						appendCodexTurn(t, dst.Path, "BINDING-RETURN-WORK", "BINDING-RETURN-REPLY")
					}
					dst = movementProfileSession(t, b, in.Target, dst.Key)
					left := movementProfileSession(t, a, in.Source, in.Session.Key)
					observed, err := store.Observe(in.Source.Install.ProfileID(), &agent.Account{
						Provider: "fixture", Observation: "first-public-observation", LoggedIn: true, Confidence: "limited"}, "")
					if err != nil {
						t.Fatal(err)
					}
					if observed.Binding == in.Source.Install.BindingID() {
						t.Fatal("fixture did not rotate the unobserved binding")
					}
					target := in.Source
					target.Install.Profile = &observed
					switch originalWork {
					case "appended":
						if from == "claude" {
							appendTurn(t, left.Path, "INDEPENDENT-ORIGINAL-WORK")
						} else {
							appendCodexTurn(t, left.Path, "INDEPENDENT-ORIGINAL-WORK", "must stay")
						}
					case "rewritten":
						needle := []byte("PLUM-7")
						if from == "codex" {
							needle = []byte("MOVEMENT-SEED")
						}
						before := movementBytes(t, left.Path)
						changed := bytes.ReplaceAll(before, needle, []byte("REWRITTEN-ORIGINAL-WORK"))
						if bytes.Equal(before, changed) {
							t.Fatal("fixture did not rewrite native conversation")
						}
						if err := os.WriteFile(left.Path, changed, 0o600); err != nil {
							t.Fatal(err)
						}
					}
					left = movementProfileSession(t, a, target, left.Key)
					original := movementBytes(t, left.Path)
					back := move.Input{Source: in.Target, Session: dst, Lineage: movementGraph(t, dst), Target: target,
						Copies: []move.Copy{{Summary: left, Lineage: movementGraph(t, left)}}}
					opt := move.Options{TargetDir: a.repo, Mark: true, Notify: false}
					planned, err := move.Build(ctx, back, opt)
					if err != nil {
						t.Fatal(err)
					}
					if originalWork != "unchanged" {
						if len(planned.Blockers) == 0 || planned.Continue.Relation != move.RelationDiverged {
							t.Fatalf("independent/rewritten original must still block: %+v", planned)
						}
						return
					}
					if len(planned.Blockers) != 0 {
						t.Fatalf("account observation alone cannot make native history diverge: %v", planned.Blockers)
					}
					if !planned.Options.OtherAccount || planned.Placement.Key.Session == left.Key.Session || planned.Continue.Relation != move.RelationNew {
						t.Fatal("return under unverified binding must create a separate portable native session")
					}
					// Explicit native append is still forbidden, even though comparison succeeds.
					selected := opt
					selected.TargetSession = left.Key.String()
					blocked, err := move.Build(ctx, back, selected)
					if err != nil || !strings.Contains(strings.Join(blocked.Blockers, " "), "unverified account binding") {
						t.Fatalf("binding change must not grant native append permission: %v %v", blocked, err)
					}
					if _, err := move.Apply(ctx, planned, back, env); err != nil {
						t.Fatal(err)
					}
					home := movementProfileSession(t, a, target, planned.Placement.Key)
					if !mentions(readAll(t, a, target.Module, target.Install, home), "BINDING-RETURN-WORK") {
						t.Fatal("portable return lost new work")
					}
					if !bytes.Equal(original, movementBytes(t, left.Path)) {
						t.Fatal("portable return changed original native bytes")
					}
					g := movementGraph(t, home)
					if len(g.ActiveHops()) != 2 {
						t.Fatalf("return lost movement receipts: %+v", g.Hops)
					}
					for _, h := range g.ActiveHops() {
						if h.Notify {
							t.Fatal("quiet return enabled notices")
						}
					}
				})
			}
		}
	}
}

func movementProfileSession(t *testing.T, l location, side move.Side, key agent.SessionKey) agent.Summary {
	t.Helper()
	for _, s := range listAgent(t, l, side.Module, side.Install) {
		s.Key.Profile = side.Install.ProfileID()
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("missing profile session %s", key)
	return agent.Summary{}
}
