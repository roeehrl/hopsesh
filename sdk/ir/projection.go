package ir

import (
	"bytes"
	"encoding/json"
)

// ContentHash excludes chain position, timestamps and representation provenance.
func ContentHash(n Node) string {
	if n.Native != nil && n.Reasoning != nil && n.Reasoning.Opaque {
		d := json.NewDecoder(bytes.NewReader(n.Native.Payload))
		d.UseNumber()
		var v any
		if d.Decode(&v) == nil {
			if raw, err := json.Marshal(v); err == nil {
				native := *n.Native
				native.Payload = raw
				n.Native = &native
			}
		}
	}
	return string(ComputeID(n, ""))
}

func ProjectionFor(n Node, item Item) Projection {
	return Projection{Fragments: item.Fragments, Anchor: n.Native.Anchor, Hash: ContentHash(n), Coverage: append([]NodeID(nil), item.Coverage...), Generated: item.Generated, Fidelity: item.Fidelity}
}
