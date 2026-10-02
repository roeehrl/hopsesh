package ir

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"
)

// identity is the part of a node its id covers: what the model sees, never timestamps,
// native ids or provenance.
type identity struct {
	Kind   Kind            `json:"k"`
	Actor  Actor           `json:"a"`
	Text   string          `json:"t,omitempty"`
	Tool   string          `json:"tn,omitempty"`
	Input  json.RawMessage `json:"ti,omitempty"`
	Status ToolStatus      `json:"rs,omitempty"`
	Exit   *int            `json:"rx,omitempty"`
	Output string          `json:"ro,omitempty"`
	Plan   []PlanEntry     `json:"pl,omitempty"`
	Att    string          `json:"at,omitempty"` // SHA-256 of attachment data
	Opaque bool            `json:"op,omitempty"`
	Parent NodeID          `json:"p,omitempty"`
}

// ComputeID returns a node's content address given its parent.
func ComputeID(n Node, parent NodeID) NodeID {
	id := identity{Kind: n.Kind, Actor: n.Actor, Text: n.Text, Parent: parent, Plan: n.Plan}
	if n.Tool != nil {
		id.Tool, id.Input = n.Tool.Name, n.Tool.Input
	}
	if n.Result != nil {
		id.Status, id.Exit, id.Output = n.Result.Status, n.Result.ExitCode, n.Result.Output
	}
	if n.Attachment != nil {
		sum := sha256.Sum256(n.Attachment.Data)
		id.Att = hex.EncodeToString(sum[:])
	}
	if n.Reasoning != nil && n.Reasoning.Opaque && n.Native != nil {
		// Opaque reasoning has no readable text; its native bytes are its identity.
		id.Text = string(n.Native.Payload)
		id.Opaque = true
	}
	b, err := Canonical(id)
	if err != nil {
		panic(err) // identity is plain data; marshalling cannot fail
	}
	sum := sha256.Sum256(b)
	return NodeID(hex.EncodeToString(sum[:]))
}

// Chain sets Parent and ID on nodes in order, starting after parent.
func Chain(nodes []Node, parent NodeID) {
	for i := range nodes {
		nodes[i].Parent = parent
		nodes[i].ID = ComputeID(nodes[i], parent)
		parent = nodes[i].ID
	}
}

// Canonical encodes v as RFC 8785 (JSON Canonicalization Scheme) JSON: object keys
// sorted by UTF-16 code units, no insignificant whitespace, minimal string escaping.
// Numbers must be integers (hopsesh hashes no floats).
func Canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, x); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(b *bytes.Buffer, x any) error {
	switch v := x.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return fmt.Errorf("canonical JSON: non-integer number %s", v)
		}
		b.WriteString(strconv.FormatInt(i, 10))
	case string:
		writeString(b, v)
	case []any:
		b.WriteByte('[')
		for i, e := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, k)
			b.WriteByte(':')
			if err := writeCanonical(b, v[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("canonical JSON: unexpected %T", x)
	}
	return nil
}

func writeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(b, `\u%04x`, r)
		case r == utf8.RuneError:
			b.WriteString("�")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// utf16Less orders strings by UTF-16 code units, as RFC 8785 requires.
func utf16Less(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	for i := 0; i < len(ra) && i < len(rb); i++ {
		ua, ub := utf16Units(ra[i]), utf16Units(rb[i])
		for j := 0; j < len(ua) && j < len(ub); j++ {
			if ua[j] != ub[j] {
				return ua[j] < ub[j]
			}
		}
		if len(ua) != len(ub) {
			return len(ua) < len(ub)
		}
	}
	return len(ra) < len(rb)
}

func utf16Units(r rune) []uint16 {
	if r < 0x10000 {
		return []uint16{uint16(r)}
	}
	r -= 0x10000
	return []uint16{uint16(0xD800 + (r >> 10)), uint16(0xDC00 + (r & 0x3FF))}
}
