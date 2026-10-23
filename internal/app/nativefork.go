package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// remoteForks are the sessions whose native forks are verified on another machine: the
// native families (each fork with its parents and its other forks) of the sessions want
// accepts. Verification reads whole transcripts, which over SSH can take longer than a
// scan may (a few GB of Codex rollouts is common), so a listing verifies none of them: its
// remote forks are listed as sessions of their own until an action about one (whose scan
// names it) verifies its family.
func remoteForks(ss []agent.Summary, want func(agent.Summary) bool) []int {
	by, parents := map[agent.SessionID]int{}, map[agent.SessionID]bool{}
	for i, s := range ss {
		by[s.Key.Session] = i
		parents[s.NativeParent] = true
	}
	root := func(i int) int {
		for range ss {
			p, ok := by[ss[i].NativeParent]
			if ss[i].NativeParent == "" || !ok {
				break
			}
			i = p
		}
		return i
	}
	families := map[int]bool{}
	for i, s := range ss {
		if (s.NativeParent != "" || parents[s.Key.Session]) && want(s) {
			families[root(i)] = true
		}
	}
	var pick []int
	for i := range ss {
		if families[root(i)] {
			pick = append(pick, i)
		}
	}
	return pick
}

func nativeForkManifests(ctx context.Context, hm *host.Machine, h agent.Host, mod agent.Module, in agent.Install, ss []agent.Summary, manifests []*lineage.Manifest, problems []string) {
	verifier, ok := mod.(agent.NativeForkVerifier)
	if !ok {
		return
	}
	reader, ok := mod.(agent.Reader)
	if !ok {
		return
	}
	// Verification reads and hashes whole transcripts; an unchanged set is not reverified.
	inputs, cacheable := forkInputs(ctx, hm, h, in, ss, manifests, problems)
	if cacheable && inputs == "" {
		return // no native forks
	}
	subjects := map[agent.SessionID]bool{}
	for _, s := range ss {
		if s.NativeParent != "" {
			subjects[s.Key.Session] = true
			subjects[s.NativeParent] = true
		}
	}
	if r, ok := forks.get(inputs); cacheable && ok {
		for i, s := range ss {
			if subjects[s.Key.Session] {
				if member, ok := r.members[s.Key.String()+"\x00"+s.Path]; ok {
					manifests[i] = member.manifest
					problems[i] = member.problem
				}
			}
		}
		return
	}
	contentReads.Add(1)
	transient := false
	if cacheable {
		defer func() {
			// Problems follow from these file versions; a failed identity or a cancelled
			// scan may not, so those results are not kept.
			if !transient && ctx.Err() == nil {
				members := map[string]forkMember{}
				for i, s := range ss {
					if subjects[s.Key.Session] {
						members[s.Key.String()+"\x00"+s.Path] = forkMember{manifests[i], problems[i]}
					}
				}
				forks.put(inputs, forkResult{members: members})
			}
		}()
	}
	by := map[agent.SessionID]int{}
	for i, s := range ss {
		by[s.Key.Session] = i
	}
	// A self-contained fork whose parent was deleted is a conversation of its own.
	orphans, _ := mod.(agent.NativeForkOrphans)
	orphaned := make([]bool, len(ss))
	for i, s := range ss {
		if _, listed := by[s.NativeParent]; listed || orphans == nil || manifests[i] != nil || problems[i] != "" || s.NativeParent == "" {
			continue
		}
		if gone, err := orphans.NativeParentGone(ctx, h, in, s); err == nil && gone {
			orphaned[i] = true
		}
	}
	for pass := 0; pass < len(ss); pass++ {
		changed := false
		for i, s := range ss {
			if manifests[i] != nil || problems[i] != "" || s.NativeParent == "" || orphaned[i] {
				continue
			}
			parent, ok := by[s.NativeParent]
			if !ok {
				problems[i] = "native fork parent is unavailable; ancestry cannot be verified"
				continue
			}
			pm := manifests[parent].Clone()
			if pm == nil && ss[parent].NativeParent != "" {
				continue
			}
			endpoint, err := hm.PrepareIdentity(ctx)
			if err != nil {
				transient = true
				problems[i] = err.Error()
				continue
			}
			parentSeg, err := reader.Read(ctx, h, in, ss[parent], ir.Cursor{})
			if err != nil {
				problems[i] = err.Error()
				continue
			}
			if pm == nil {
				pm = lineage.NewNative(endpoint, ss[parent].Key)
			}
			pid, _, err := pm.ObserveBinding(lineage.Replica{Key: ss[parent].Key, Endpoint: endpoint, Binding: in.BindingID(), Location: hm.Name, AgentVersion: ss[parent].AgentVersion, Time: parentSeg.Header.Created}, &parentSeg)
			if err != nil {
				problems[i] = err.Error()
				continue
			}
			// A parent whose graph cannot be written cannot carry a fork's ancestry either:
			// say so now, before reading both rollouts to verify the fork.
			if err := pm.Writable(); err != nil {
				problems[i] = fmt.Sprintf("native fork ancestry: %v", err)
				continue
			}
			previous := manifests[parent]
			manifests[parent] = pm // a clone of its own (above)

			proof, err := verifier.VerifyNativeFork(ctx, h, in, ss[parent], s)
			if err != nil {
				problems[i] = err.Error()
				continue
			}
			seg, err := reader.Read(ctx, h, in, s, ir.Cursor{})
			if err != nil {
				problems[i] = err.Error()
				continue
			}
			m, err := pm.AdoptNativeFork(pid, lineage.Replica{Key: s.Key, Endpoint: endpoint, Binding: in.BindingID(), Location: hm.Name, AgentVersion: s.AgentVersion, Time: seg.Header.Created}, proof, &seg)
			if err != nil {
				problems[i] = fmt.Sprintf("native fork ancestry: %v", err)
				if pm.Writable() != nil || errors.Is(err, lineage.ErrTooLarge) {
					manifests[parent] = previous
				}
				continue
			}
			manifests[i] = m
			changed = true
		}
		if !changed {
			break
		}
	}
	for i, s := range ss {
		if s.NativeParent != "" && manifests[i] == nil && problems[i] == "" && !orphaned[i] {
			problems[i] = "native fork ancestry is unresolved or cyclic; preserve a separate branch"
		}
	}

}
