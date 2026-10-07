package convert

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Archive retains portable history independently of working-context limits. Vendor
// private state, signed reasoning and attachment bytes never cross this boundary.
func Archive(nodes []ir.Node, r Request) ([]byte, error) {
	// Keep optional briefing data even when the working copy must shorten it.
	if r.Briefing.Note != "" || len(r.Briefing.Rules) > 0 || len(r.Briefing.Missing) > 0 {
		raw, err := json.Marshal(r.Briefing)
		if err != nil {
			return nil, err
		}
		nodes = append(append([]ir.Node(nil), nodes...), ir.Node{Kind: ir.KindMessage, Actor: ir.Agent, Generated: true, Text: "[Hopsesh transfer briefing data]\n" + string(raw)})
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	res := Result{}
	for _, original := range nodes {
		n := original
		if n.Kind == ir.KindReasoning {
			continue
		}
		n.Native = nil
		n.Reasoning = nil
		if n.Attachment != nil {
			a := *n.Attachment
			a.Data = nil
			n.Attachment = &a
		}
		raw, err := json.Marshal(n)
		if err != nil {
			return nil, err
		}
		// Transform every portable textual field, including tool arguments and fragments.
		var data any
		if err = json.Unmarshal(raw, &data); err != nil {
			return nil, err
		}
		var transform func(any) any
		transform = func(v any) any {
			switch x := v.(type) {
			case string:
				return res.mapText(r, x)
			case []any:
				for i := range x {
					x[i] = transform(x[i])
				}
			case map[string]any:
				for k, v := range x {
					x[k] = transform(v)
				}
			}
			return v
		}
		if err = enc.Encode(transform(data)); err != nil {
			return nil, err
		}
		if b.Len() > ir.MaxTranscriptBytes {
			return nil, fmt.Errorf("portable archive exceeds analysis limit")
		}
	}
	return b.Bytes(), nil
}

// MergeArchives deduplicates immutable portable records by their encoded content.
// It preserves differing renderings rather than silently dropping fork evidence.
func MergeArchives(parts ...[]byte) ([]byte, error) {
	var out bytes.Buffer
	seen := map[string]bool{}
	for _, part := range parts {
		for _, raw := range bytes.Split(part, []byte{'\n'}) {
			if len(bytes.TrimSpace(raw)) == 0 {
				continue
			}
			var n ir.Node
			if err := json.Unmarshal(raw, &n); err != nil {
				return nil, err
			}
			if n.Native != nil || n.Reasoning != nil || n.Kind == ir.KindReasoning || n.Attachment != nil && len(n.Attachment.Data) > 0 {
				return nil, fmt.Errorf("archive contains vendor-private state")
			}
			sum := sha256.Sum256(raw)
			key := string(sum[:])
			if seen[key] {
				continue
			}
			seen[key] = true
			out.Write(raw)
			out.WriteByte('\n')
			if out.Len() > ir.MaxTranscriptBytes {
				return nil, fmt.Errorf("portable archive exceeds analysis limit")
			}
		}
	}
	return out.Bytes(), nil
}
