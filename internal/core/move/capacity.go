package move

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/convert"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// portableWrite prepares cloud returns through the same complete-payload policy as
// local/SSH continuations. A capacity rollover preserves the original byte-for-byte.
func portableWrite(ctx context.Context, h agent.Host, m agent.Module, in agent.Install, original *agent.Summary, full []ir.Node, req ir.WriteRequest, r convert.Request) (ir.WriteRequest, convert.Result, bool, error) {
	if original != nil {
		reader, ok := m.(agent.Reader)
		if !ok {
			return req, convert.Result{}, false, fmt.Errorf("cannot preserve the original's portable history")
		}
		seg, err := reader.Read(ctx, h, in, *original, ir.Cursor{})
		if err != nil {
			return req, convert.Result{}, false, err
		}
		if seg.Cursor != req.Expect {
			return req, convert.Result{}, false, fmt.Errorf("original changed since planning; make a new plan")
		}
		full = append(append([]ir.Node(nil), seg.Nodes...), full...)
	}
	capacity, err := agent.CapacityFor(ctx, m, h, in, original)
	if err != nil {
		return req, convert.Result{}, false, err
	}
	rolled := original != nil && (capacity.Unknown || capacity.Allowance() < 4096)
	if rolled {
		capacity, err = agent.CapacityFor(ctx, m, h, in, nil)
		if err != nil {
			return req, convert.Result{}, false, err
		}
		req.Mode = ir.WriteNew
		req.SessionID = newID()
		req.Expect = ir.Cursor{}
		r.Nodes = full
	}
	allowance := capacity.Allowance()
	r.Window = capacity.EffectiveWindow()
	r.Limit = &allowance
	if req.SessionID == "" || req.SessionID == "." || req.SessionID == ".." || strings.ContainsAny(req.SessionID, "/\\") {
		return req, convert.Result{}, rolled, fmt.Errorf("invalid session ID for portable archive")
	}
	path := h.Path().Join(in.Root(m.Spec().Roots[0].Name), "hopsesh", "archives", req.SessionID+".jsonl")
	r.Briefing.HistoryFile = path
	r.Briefing.TargetOS = h.Facts().OS
	rendered := convert.Render(r)
	rendered.Report.Capacity = capacity
	rendered.Report.Archive = path
	if rendered.Report.Blocked != "" {
		return req, rendered, rolled, fmt.Errorf("%s", rendered.Report.Blocked)
	}
	if err = capacity.Check(rendered.Items); err != nil {
		return req, rendered, rolled, err
	}
	sourceID := r.Briefing.SourceID
	if original != nil {
		sourceID = string(original.Key.Session)
	}
	archive, err := preservedArchive(h, m, in, sourceID, full, r)
	if err != nil {
		return req, rendered, rolled, err
	}
	if err = h.FS().WriteFile(path, archive, 0o600); err != nil {
		return req, rendered, rolled, fmt.Errorf("preserving portable history: %w", err)
	}
	req.Items = rendered.Items
	return req, rendered, rolled, nil
}

// Archive lookup is deterministic under the selected agent/account root. Neither
// a model message nor an imported archive can nominate an arbitrary file to read.
func preservedArchive(h agent.Host, m agent.Module, in agent.Install, id string, nodes []ir.Node, r convert.Request) ([]byte, error) {
	fresh, err := convert.Archive(nodes, r)
	if err != nil {
		return nil, err
	}
	if id == "" || strings.ContainsAny(id, "/\\") || id == ".." {
		return fresh, nil
	}
	path := h.Path().Join(in.Root(m.Spec().Roots[0].Name), "hopsesh", "archives", id+".jsonl")
	prior, err := h.FS().ReadFile(path, ir.MaxTranscriptBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return fresh, nil
	}
	if err != nil {
		return nil, err
	}
	// Prior archives pass the same selected redaction/mapping policy again.
	var oldNodes []ir.Node
	dec := json.NewDecoder(bytes.NewReader(prior))
	for {
		var n ir.Node
		err = dec.Decode(&n)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		oldNodes = append(oldNodes, n)
	}
	prior, err = convert.Archive(oldNodes, r)
	if err != nil {
		return nil, err
	}
	return convert.MergeArchives(prior, fresh)
}

// activeCopies excludes a retained rollover replica only while its native cursor is
// unchanged. New work in the retained original remains visible as a conflict.
func activeCopies(ctx context.Context, in Input, opt Options) []Copy {
	if opt.TargetSession != "" {
		return in.Copies
	}
	manifests := []*lineage.Manifest{in.Lineage}
	for _, c := range in.Copies {
		manifests = append(manifests, c.Lineage)
	}
	undone := map[string]bool{}
	for _, m := range manifests {
		if m != nil {
			for _, c := range m.Compensations {
				undone[c.Operation] = true
			}
		}
	}
	present := func(r lineage.Replica) bool {
		if r.Key == in.Session.Key && r.Endpoint == in.Source.Machine.Facts.Endpoint {
			return true
		}
		for _, c := range in.Copies {
			if r.Key == c.Summary.Key && r.Endpoint == in.Target.Machine.Facts.Endpoint {
				return true
			}
		}
		return false
	}
	var out []Copy
	for _, c := range in.Copies {
		retired := false
		if c.Live.State != agent.Live {
			for _, m := range manifests {
				if m == nil {
					continue
				}
				for _, hop := range m.Hops {
					if hop.Rollover == nil || undone[hop.ID] || !present(m.Replica(hop.To)) {
						continue
					}
					old := m.Replica(hop.Rollover.Replica)
					if old.Key != c.Summary.Key || old.Endpoint != in.Target.Machine.Facts.Endpoint {
						continue
					}
					seg, err := readSegment(ctx, in.Target, c.Summary)
					if err == nil && seg.Cursor.Head != "" && seg.Cursor.Head == hop.Rollover.Cursor.Head {
						retired = true
					}
				}
			}
		}
		if !retired {
			out = append(out, c)
		}
	}
	return out
}
