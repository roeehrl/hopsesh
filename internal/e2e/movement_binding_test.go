package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/profiles"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Exact matrix regression: a remote default profile was discovered before its
// first public account observation. The return receiver probes it locally, which
// rotates its binding. Portable delta appends preserve the exact original, native
// anchors and historical authorship; they never replay source-private state.
func TestMovementQuietReturnAfterAccountObservation(t *testing.T) {
	for _, from := range []string{"claude", "codex"} {
		for _, to := range []string{"claude", "codex"} {
			for _, originalWork := range []string{"unchanged", "appended", "rewritten"} {
				for _, alreadyObserved := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s-%s/%s/already-observed=%t", from, to, originalWork, alreadyObserved), func(t *testing.T) {
						a, b, in := movementInput(t, from, to)
						ctx := context.Background()
						store := profiles.Store{Dir: t.TempDir()}
						account := &agent.Account{Provider: "fixture", Email: "alice@example.com", Observation: "public-observation", LoggedIn: true, Confidence: "limited"}
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
							if alreadyObserved {
								profile, err = store.Observe(profile.ID, account, "")
								if err != nil {
									t.Fatal(err)
								}
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
						p, _ := applyMovement(t, in, move.Options{TargetDir: b.repo, Notify: false}, env)
						dst := movementProfileSession(t, b, in.Target, p.Placement.Key)
						if to == "claude" {
							appendTurn(t, dst.Path, "BINDING-RETURN-WORK")
						} else {
							appendCodexTurn(t, dst.Path, "BINDING-RETURN-WORK", "BINDING-RETURN-REPLY")
						}
						dst = movementProfileSession(t, b, in.Target, dst.Key)
						left := movementProfileSession(t, a, in.Source, in.Session.Key)
						observed, err := store.Observe(in.Source.Install.ProfileID(), account, "")
						if err != nil {
							t.Fatal(err)
						}
						if (observed.Binding == in.Source.Install.BindingID()) != alreadyObserved {
							t.Fatal("fixture binding must stay stable for an unchanged observation, and rotate for a first observation")
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
						opt := move.Options{TargetDir: a.repo, Notify: false, NewReplica: true}
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
						// Returning to the exact original appends only missing portable work.
						// Limited metadata and a first account observation are not native
						// replay permission, but neither prevents ordinary text appends.
						selected := opt
						selected.NewReplica = false
						selected.TargetSession = left.Key.String()
						planned, err = move.Build(ctx, back, selected)
						if err != nil || len(planned.Blockers) != 0 || planned.ReviewNewSession || planned.Options.Fork || planned.Continue.AppendTo == nil || planned.Placement.Key != left.Key {
							t.Fatalf("original return was not selected: %+v %v", planned, err)
						}
						unsupported := back
						unsupported.Target.Module = &noPortableAppend{back.Target.Module}
						blocked, err := move.Build(ctx, unsupported, selected)
						if err != nil || !blocked.ReviewNewSession || len(blocked.Blockers) == 0 {
							t.Fatalf("adapter without append contract accepted return: %+v %v", blocked, err)
						}
						if from == "claude" {
							late := back
							late.Target.Module = &lateClaudeWriter{claude.New()}
							if _, err := move.Apply(ctx, planned, late, move.Env{StateDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "destination copy is open") {
								t.Fatalf("writer started after planning was ignored: %v", err)
							}
							if !bytes.Equal(original, movementBytes(t, left.Path)) {
								t.Fatal("late live writer guard changed original")
							}
						}
						// An original that is gone (deleted, archived) is said plainly, with a
						// new session on the same branch as the way on, never a dead end.
						gone := selected
						gone.TargetSession = agent.SessionKey{Agent: left.Key.Agent, Profile: left.Key.Profile, Session: "deleted-original"}.String()
						missing, err := move.Build(ctx, back, gone)
						if err != nil || !missing.ReviewNewSession || !strings.Contains(strings.Join(missing.Blockers, " "), "not here any more") {
							t.Fatalf("missing original was a dead end: %+v %v", missing, err)
						}
						applyIn := back
						if from == "claude" {
							// An original open in its agent but blocked by hopsesh may be added
							// to; the review says it must be reopened. Without that, still refused.
							late := back
							late.Target.Module = &lateClaudeWriter{claude.New()}
							late.Copies = slices.Clone(back.Copies)
							for i := range late.Copies {
								if late.Copies[i].Summary.Key == left.Key {
									late.Copies[i].Live = agent.LiveInfo{State: agent.Live}
								}
							}
							closed, err := move.Build(ctx, late, selected)
							if err != nil || !slices.Contains(closed.Blockers, "the destination copy is open; quit it first") {
								t.Fatalf("open original accepted without its block: %+v %v", closed, err)
							}
							withOpen := selected
							withOpen.OpenBlockedOriginal = left.Key.String()
							open, err := move.Build(ctx, late, withOpen)
							if err != nil || len(open.Blockers) != 0 || open.Continue.AppendTo == nil || !strings.Contains(strings.Join(open.Warnings, " "), "The original is open") {
								t.Fatalf("blocked open original was not added to: %+v %v", open, err)
							}
							planned, applyIn = open, late
						}
						if _, err := move.Apply(ctx, planned, applyIn, env); err != nil {
							t.Fatal(err)
						}
						home := movementProfileSession(t, a, target, planned.Placement.Key)
						after := movementBytes(t, left.Path)
						if !bytes.HasPrefix(after, original) {
							t.Fatal("return rewrote original native records")
						}
						delta := string(after[len(original):])
						if !strings.Contains(delta, "BINDING-RETURN-WORK") {
							t.Fatal("return lost new work")
						}
						seed := "PLUM-7"
						if from == "codex" {
							seed = "MOVEMENT-SEED"
						}
						if strings.Contains(delta, seed) {
							t.Fatal("return reimported original history")
						}
						// Durable retry of the same operation must not duplicate the append.
						if _, err := move.Apply(ctx, planned, back, env); err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(after, movementBytes(t, left.Path)) {
							t.Fatal("retry duplicated return work")
						}
						g := movementGraph(t, home)
						if g.Family != back.Lineage.Family || g.Branch != back.Lineage.Branch {
							t.Fatal("original return became an unrelated family or separate fork")
						}
						if len(g.ActiveHops()) != 2 {
							t.Fatalf("return lost movement receipts: %+v", g.Hops)
						}
						for _, h := range g.ActiveHops() {
							if h.Fork {
								t.Fatal("portable return created a fork")
							}
							if h.Notify {
								t.Fatal("quiet return enabled notices")
							}
						}
					})
				}
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

// An adapter must explicitly opt into portable original-session appends.
type noPortableAppend struct{ agent.Module }

func (m *noPortableAppend) Profile(in agent.Install) ir.Profile {
	p := m.Module.(agent.Writer).Profile(in)
	p.PortableAppend = false
	return p
}
func (m *noPortableAppend) Write(ctx context.Context, h agent.Host, in agent.Install, r ir.WriteRequest) (ir.WriteResult, error) {
	return m.Module.(agent.Writer).Write(ctx, h, in, r)
}
func (m *noPortableAppend) Read(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, c ir.Cursor) (ir.Segment, error) {
	return m.Module.(agent.Reader).Read(ctx, h, in, s, c)
}

type lateClaudeWriter struct{ *claude.Module }

func (*lateClaudeWriter) Live(_ context.Context, _ agent.Host, _ agent.Install, ids []agent.SessionID) (map[agent.SessionID]agent.LiveInfo, error) {
	out := map[agent.SessionID]agent.LiveInfo{}
	for _, id := range ids {
		out[id] = agent.LiveInfo{State: agent.Live}
	}
	return out, nil
}
