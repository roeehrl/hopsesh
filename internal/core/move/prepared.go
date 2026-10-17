package move

import (
	"encoding/json"
	"errors"

	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// preparedPlan is private durable planner state, not a public DTO. Reconnecting
// reconstructs live hosts separately, then restores the exact accepted IDs,
// cursors and conversion items. It never accepts a replacement destination plan.
type preparedPlan struct {
	Format        int                 `json:"format"`
	Plan          *Plan               `json:"plan"`
	Manifest      *lineage.Manifest   `json:"manifest"`
	SourceLine    string              `json:"sourceLine"`
	TargetLine    string              `json:"targetLine"`
	SourceReplica lineage.ReplicaID   `json:"sourceReplica"`
	SourceState   lineage.State       `json:"sourceState"`
	Bundle        agent.Bundle        `json:"bundle"`
	Resume        agent.ResumeOptions `json:"resume"`
	StartPrompt   string              `json:"startPrompt"`
	Items         []ir.Item           `json:"items,omitempty"`
	Header        ir.Header           `json:"header"`
	Expect        ir.Cursor           `json:"expect"`
	Head          ir.Cursor           `json:"head"`
	Archive       []byte              `json:"archive,omitempty"`
	Native        json.RawMessage     `json:"native,omitempty"`
}

func (p *Plan) FreezePrepared() ([]byte, error) {
	if p == nil || (p.Kind != KindMove && p.Kind != KindContinue) || !validOperation.MatchString(p.OperationID) {
		return nil, errors.New("only a native session transfer can be persisted as a prepared plan")
	}
	r := preparedPlan{Format: 1, Plan: p, Manifest: p.manifest, SourceLine: p.sourceLine, TargetLine: p.targetLine, SourceReplica: p.sourceReplica, SourceState: p.sourceState, Bundle: p.bundle, Resume: p.resumeOpts, StartPrompt: p.StartPrompt}
	if c := p.Continue; c != nil {
		r.Items, r.Header, r.Expect, r.Head, r.Archive = c.items, c.header, c.expect, c.head, c.archive
	}
	if p.native != nil {
		var err error
		r.Native, err = p.native.FreezePrepared()
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(r)
}

func RestorePrepared(body []byte, current *Plan) (*Plan, error) {
	var r preparedPlan
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	p := r.Plan
	if r.Format != 1 || p == nil || current == nil || p.Key != current.Key || p.OperationID != current.OperationID || p.Source.ID != current.Source.ID || p.Target.ID != current.Target.ID || p.Target.Profile != current.Target.Profile || p.Source.Binding != current.Source.Binding || p.Target.Binding != current.Target.Binding || p.Target.CWD != current.Target.CWD {
		return nil, errors.New("persisted plan no longer matches its source, destination or account binding")
	}
	if p.Kind != KindMove && p.Kind != KindContinue {
		return nil, errors.New("invalid persisted transfer kind")
	}
	p.manifest, p.sourceLine, p.targetLine, p.sourceReplica, p.sourceState, p.bundle, p.resumeOpts, p.StartPrompt = r.Manifest, r.SourceLine, r.TargetLine, r.SourceReplica, r.SourceState, r.Bundle, r.Resume, r.StartPrompt
	if c := p.Continue; c != nil {
		c.items, c.header, c.expect, c.head, c.archive = r.Items, r.Header, r.Expect, r.Head, r.Archive
	}
	if len(r.Native) > 0 {
		var err error
		// The nested native backup gets a fresh planning ID on every build.
		// Restore its persisted ID only within this already-bound parent transfer.
		var native preparedPlan
		if err = json.Unmarshal(r.Native, &native); err != nil {
			return nil, err
		}
		if current.native == nil || native.Plan == nil {
			return nil, errors.New("native backup plan is no longer available")
		}
		current.native.OperationID = native.Plan.OperationID
		p.native, err = RestorePrepared(r.Native, current.native)
		if err != nil {
			return nil, err
		}
		p.nativeIn = current.nativeIn
	}
	return p, nil
}
