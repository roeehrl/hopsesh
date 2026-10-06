package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func nativeValue(payload []byte) []byte {
	var value any
	d := json.NewDecoder(bytes.NewReader(payload))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return nil
	}
	b, _ := json.Marshal(value)
	return b
}

// VerifyNativeFork uses Codex's forked_from_id and optional exclusive ordinal, then
// compares copied native records. Text, timestamps or a title do not imply ancestry.
func (m *Module) VerifyNativeFork(ctx context.Context, h agent.Host, in agent.Install, parent, child agent.Summary) ([]agent.NativeInheritance, error) {
	pb, err := h.FS().ReadFile(parent.Path, 1<<30)
	if err != nil {
		return nil, err
	}
	cb, err := h.FS().ReadFile(child.Path, 1<<30)
	if err != nil {
		return nil, err
	}
	pm, err := firstMeta(pb)
	if err != nil {
		return nil, err
	}
	cm, err := firstMeta(cb)
	if err != nil {
		return nil, err
	}
	if cm.ForkedFromID != pm.ID || cm.ID == pm.ID || child.Key.Agent != parent.Key.Agent {
		return nil, fmt.Errorf("%w: native fork parent is not declared", agent.ErrDiverged)
	}
	if cm.HistoryMode == "paginated" {
		return nil, fmt.Errorf("%w: paginated fork ancestry is not materialized", agent.ErrUnsupported)
	}
	pr, _, err := readLines(strings.NewReader(string(pb)))
	if err != nil {
		return nil, err
	}
	cr, _, err := readLines(strings.NewReader(string(cb)))
	if err != nil {
		return nil, err
	}
	last := 1
	for last < len(pr) && last < len(cr) {
		if cm.ForkedFromOrdinalExclusive != nil {
			if pr[last].Ordinal == nil {
				return nil, fmt.Errorf("%w: fork boundary requires native ordinals", agent.ErrUnsupported)
			}
			if *pr[last].Ordinal >= *cm.ForkedFromOrdinalExclusive {
				break
			}
		}
		if pr[last].Type != cr[last].Type || pr[last].Timestamp != cr[last].Timestamp || !bytes.Equal(nativeValue(pr[last].Payload), nativeValue(cr[last].Payload)) {
			break
		}
		last++
	}
	ps, err := m.Read(ctx, h, in, parent, ir.Cursor{})
	if err != nil {
		return nil, err
	}
	cs, err := m.Read(ctx, h, in, child, ir.Cursor{})
	if err != nil {
		return nil, err
	}
	by := map[string]ir.Node{}
	for _, n := range ps.Nodes {
		by[n.Native.Anchor] = n
	}
	var out []agent.NativeInheritance
	for _, n := range cs.Nodes {
		var index int
		_, _ = fmt.Sscanf(n.Native.Anchor, "line:%d/", &index)
		if index >= last {
			continue
		}
		pn, ok := by[n.Native.Anchor]
		if !ok || ir.ContentHash(pn) != ir.ContentHash(n) {
			return nil, fmt.Errorf("%w: native fork prefix changed representation", agent.ErrDiverged)
		}
		out = append(out, agent.NativeInheritance{ParentAnchor: pn.Native.Anchor, ChildAnchor: n.Native.Anchor, Hash: ir.ContentHash(n)})
	}
	return out, nil
}
