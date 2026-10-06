package app

import (
	"context"
	"fmt"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func nativeForkManifests(ctx context.Context, hm *host.Machine, h agent.Host, mod agent.Module, in agent.Install, ss []agent.Summary, manifests []*lineage.Manifest, problems []string) {
	verifier, ok := mod.(agent.NativeForkVerifier)
	if !ok {
		return
	}
	reader, ok := mod.(agent.Reader)
	if !ok {
		return
	}
	by := map[agent.SessionID]int{}
	for i, s := range ss {
		by[s.Key.Session] = i
	}
	for pass := 0; pass < len(ss); pass++ {
		changed := false
		for i, s := range ss {
			if manifests[i] != nil || problems[i] != "" || s.NativeParent == "" {
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
				id := pm.Upsert(lineage.Replica{Key: ss[parent].Key, Endpoint: endpoint, Location: hm.Name, AgentVersion: ss[parent].AgentVersion, Time: parentSeg.Header.Created})
				if _, err = pm.Observe(id, &parentSeg); err != nil {
					problems[i] = err.Error()
					continue
				}
				manifests[parent] = pm.Clone()
			}
			_, pid, ok := pm.FindEndpoint(ss[parent].Key, endpoint)
			if !ok && len(pm.Replicas) == 0 {
				pid = pm.Upsert(lineage.Replica{Endpoint: endpoint, Key: ss[parent].Key, Location: hm.Name, Time: parentSeg.Header.Created, AgentVersion: ss[parent].AgentVersion})
				ok = true
			}
			if !ok {
				problems[i] = "native fork parent endpoint has no receipt"
				continue
			}
			if _, err := pm.Observe(pid, &parentSeg); err != nil {
				problems[i] = err.Error()
				continue
			}
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
			m, err := pm.AdoptNativeFork(pid, lineage.Replica{Key: s.Key, Endpoint: endpoint, Location: hm.Name, AgentVersion: s.AgentVersion, Time: seg.Header.Created}, proof, &seg)
			if err != nil {
				problems[i] = fmt.Sprintf("native fork ancestry: %v", err)
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
		if s.NativeParent != "" && manifests[i] == nil && problems[i] == "" {
			problems[i] = "native fork ancestry is unresolved or cyclic; preserve a separate branch"
		}
	}

}
