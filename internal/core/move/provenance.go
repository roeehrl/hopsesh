package move

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// prepareLineage snapshots the entire causal graph. Planning never mutates inventory.
func prepareLineage(ctx context.Context, p *Plan, in Input, seg *ir.Segment) error {
	m := in.Lineage.Clone()
	fsys, err := in.Source.Machine.FS(ctx)
	if err != nil {
		return err
	}
	actual, err := lineage.Read(fsys, in.Session.Path)
	if err != nil {
		return err
	}
	if actual != nil {
		if m == nil {
			m = actual.Clone()
		} else if err = m.Merge(actual); err != nil {
			return err
		}
	}
	if m == nil {
		id, err := in.Source.Machine.PrepareIdentity(ctx)
		if err != nil {
			return err
		}
		m = lineage.NewNative(id, p.Key)
	}
	for _, c := range in.Copies {
		if c.Lineage != nil && c.Lineage.Family == m.Family {
			if err := m.Merge(c.Lineage); err != nil {
				return fmt.Errorf("merging destination lineage: %w", err)
			}
		}
	}
	p.manifest = m
	p.sourceLine = m.Branch
	p.targetLine = m.Branch
	p.OperationID = inOperation(in, p.Options)
	if !validOperation.MatchString(p.OperationID) {
		return fmt.Errorf("invalid transfer operation ID")
	}
	sourceID, err := in.Source.Machine.PrepareIdentity(ctx)
	if err != nil {
		return err
	}
	targetID, e := in.Target.Machine.PrepareIdentity(ctx)
	err = e
	if err != nil {
		return err
	}
	p.Source.ID, p.Target.ID = sourceID, targetID
	p.Source.Profile, p.Source.Binding = in.Source.Install.ProfileID(), in.Source.Install.BindingID()
	p.Target.Profile, p.Target.Binding = in.Target.Install.ProfileID(), in.Target.Install.BindingID()
	if in.Source.Install.Profile != nil {
		p.Source.ProfileName = in.Source.Install.Profile.Name
	}
	if in.Target.Install.Profile != nil {
		p.Target.ProfileName = in.Target.Install.Profile.Name
	}
	var st lineage.State
	p.sourceReplica, st, err = m.ObserveBinding(lineage.Replica{Endpoint: sourceID, Binding: in.Source.Install.BindingID(), Key: p.Key, Location: p.Source.Location, AgentVersion: p.Source.Version, Time: seg.Header.Created}, seg)
	snapshot := false
	if err != nil && (p.Options.Fork || p.Options.Conflict == ConflictKeepBoth) {
		old, _ := m.LatestState(p.sourceReplica)
		line := m.Fork(p.OperationID+"/rewritten", old.Heads)
		m.Branch = line
		p.sourceLine, p.targetLine = line, line
		p.sourceReplica = m.Upsert(lineage.Replica{Endpoint: sourceID, Binding: in.Source.Install.BindingID(), Key: p.Key, Line: line, Location: p.Source.Location, AgentVersion: p.Source.Version, Time: seg.Header.Created})
		// This is an explicitly chosen independent snapshot, not verified replay of
		// rewritten records. It retains ancestry but cannot claim exact coverage.
		m.ReplaceProjection(p.sourceReplica, ir.Cursor{}, nil, old.Heads, []string{"native history changed; separate branch snapshot; inherited representation cannot be verified"})
		st, err = m.Observe(p.sourceReplica, seg)
		snapshot = true
		p.Options.Fork = true
		p.Mark = MarkOff
		p.Warnings = append(p.Warnings, "Native history changed; this transfer starts a separate branch from its current snapshot.")
	}
	if err != nil {
		return err
	}
	p.sourceState = st
	if p.Options.Fork && !snapshot {
		forkLine(p)
	}
	return nil
}
func forkLine(p *Plan) {
	p.targetLine = p.manifest.Fork(p.OperationID, p.sourceState.Heads)
	p.Options.Fork = true
	p.Mark = MarkOff
}
func targetState(ctx context.Context, p *Plan, in Input, c Copy) (lineage.State, ir.Segment, error) {
	reader, ok := in.Target.Module.(agent.Reader)
	if !ok {
		return lineage.State{}, ir.Segment{}, agent.ErrUnsupported
	}
	h, err := in.Target.Machine.For(ctx, in.Target.Module.Spec(), in.Target.Install, nil)
	if err != nil {
		return lineage.State{}, ir.Segment{}, err
	}
	seg, err := reader.Read(ctx, h, in.Target.Install, c.Summary, ir.Cursor{})
	if err != nil {
		return lineage.State{}, seg, err
	}
	if p.ExpectedDestination == nil {
		p.ExpectedDestination = map[string]ir.Cursor{}
	}
	p.ExpectedDestination[c.Summary.Path] = seg.Cursor
	if p.Options.OtherAccount {
		st, err := portableTargetState(p, in, c, &seg)
		return st, seg, err
	}
	replica, id, ok := p.manifest.FindBinding(c.Summary.Key, in.Target.Machine.Facts.Endpoint, in.Target.Install.BindingID())
	if !ok || replica.Binding != in.Target.Install.BindingID() {
		return lineage.State{}, seg, fmt.Errorf("destination session has no verified lineage; choose a separate fork")
	}
	if p.ExpectedDestination == nil {
		p.ExpectedDestination = map[string]ir.Cursor{}
	}
	p.ExpectedDestination[c.Summary.Path] = seg.Cursor
	st, err := p.manifest.Observe(id, &seg)
	return st, seg, err
}
func currentBranchCopies(in Input) []Copy {
	var out []Copy
	for _, c := range in.Copies {
		if c.Summary.Key.Profile != in.Target.Install.ProfileID() {
			continue
		}
		if in.Lineage != nil {
			if c.Lineage != nil && (c.Lineage.Family != in.Lineage.Family || c.Lineage.Branch != in.Lineage.Branch) {
				continue
			}
			known := false
			for _, r := range in.Lineage.Replicas {
				if r.Key == c.Summary.Key && r.Line == in.Lineage.Branch {
					known = true
				}
			}
			// A destination may know a replica the source has not acknowledged yet.
			// Its matching branch receipt is still a candidate; fresh native validation
			// occurs before using it. Source knowledge alone cannot hide that replica.
			if !known && c.Lineage != nil {
				for _, r := range c.Lineage.Replicas {
					if r.Key == c.Summary.Key && r.Line == in.Lineage.Branch {
						known = true
						break
					}
				}
			}
			if !known && c.Summary.Key != in.Session.Key {
				continue
			}
		} else if c.Summary.Key != in.Session.Key {
			continue
		}
		out = append(out, c)
	}
	return out
}

// nativeProjection matches only module-supplied native anchors. Path rewriting can
// change a content hash, but never grants coverage to an unrelated native record.
func nativeProjection(source, target ir.Segment) ([]ir.Projection, error) {
	by := map[string]ir.Node{}
	for _, n := range source.Nodes {
		if n.Native != nil {
			by[n.Native.Anchor] = n
		}
	}
	var ps []ir.Projection
	for _, n := range target.Nodes {
		if n.Native == nil {
			return nil, fmt.Errorf("native reader did not supply an anchor")
		}
		s, ok := by[n.Native.Anchor]
		if !ok {
			return nil, fmt.Errorf("native record %s has no source provenance", n.Native.Anchor)
		}
		ps = append(ps, ir.Projection{Anchor: n.Native.Anchor, Hash: ir.ContentHash(n), Coverage: s.Coverage, Fragments: s.Fragments, Generated: s.Generated, Fidelity: "native"})
	}
	return ps, nil
}

func conversionLoss(r convert.Report) []string {
	var out []string
	if r.Method == "vendor-import" {
		out = append(out, "vendor import; per-turn fidelity not verified")
	}
	if r.BriefShortened {
		out = append(out, "briefing shortened")
	}
	if r.Fidelity == convert.Note && r.Method != "vendor-import" {
		out = append(out, "briefing only; full history stays at source")
	}
	if r.Redactions > 0 {
		out = append(out, "secrets redacted")
	}
	if r.Reasoning > 0 {
		out = append(out, "reasoning omitted")
	}
	if r.Summarised > 0 {
		out = append(out, "history summarized")
	}
	if r.Truncated > 0 {
		out = append(out, "history truncated")
	}
	if r.Attachments > 0 {
		out = append(out, "attachments represented as text")
	}
	return out
}
func readSegment(ctx context.Context, side Side, s agent.Summary) (ir.Segment, error) {
	reader, ok := side.Module.(agent.Reader)
	if !ok {
		return ir.Segment{}, agent.ErrUnsupported
	}
	h, err := side.Machine.For(ctx, side.Module.Spec(), side.Install, nil)
	if err != nil {
		return ir.Segment{}, err
	}
	return reader.Read(ctx, h, side.Install, s, ir.Cursor{})
}
func recordNativeBackup(ctx context.Context, p *Plan, in Input, m *lineage.Manifest, path string, cursor ir.Cursor, when time.Time) error {
	src, err := readSegment(ctx, in.Source, in.Session)
	if err != nil {
		return err
	}
	if _, err = m.Observe(p.sourceReplica, &src); err != nil {
		return err
	}
	native := p.nativeIn.Target
	sum := agent.Summary{Key: p.native.Placement.Key, Path: path, CWD: p.Target.CWD}
	tgt, err := readSegment(ctx, native, sum)
	if err != nil {
		return err
	}
	if tgt.Cursor != cursor {
		return fmt.Errorf("native backup changed after installation")
	}
	ps, err := nativeProjection(src, tgt)
	if err != nil {
		return err
	}
	id := m.Upsert(lineage.Replica{Endpoint: in.Target.Machine.Facts.Endpoint, Binding: in.Target.Install.BindingID(), Key: sum.Key, Line: p.sourceLine, Location: p.Target.Location, AgentVersion: p.native.Target.Version, Time: when})
	st := m.ReplaceProjection(id, cursor, ps, p.sourceState.Heads, nil)
	if err := m.AppendHop(lineage.Hop{ID: p.OperationID + "/native", From: p.sourceReplica, To: id, Time: when, Kind: lineage.HopMove, Backup: true, Source: p.sourceState.ID, Target: st.ID}); err != nil {
		return err
	}
	return nil
}

var validOperation = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func inOperation(in Input, opt Options) string {
	if opt.OperationID != "" {
		return opt.OperationID
	}
	return newID()
}
