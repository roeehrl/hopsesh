package rewrite

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// kv is one member of a JSON object, kept in source order with its raw value.
type kv struct {
	key string
	val json.RawMessage
}

func parseObject(b []byte) ([]kv, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if _, err := dec.Token(); err != nil { // {
		return nil, err
	}
	var out []kv
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out = append(out, kv{k, raw})
	}
	return out, nil
}

func encodeObject(members []kv) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsonQuote(m.key))
		b.WriteByte(':')
		b.Write(m.val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func jsonQuote(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
}

// dropElems removes elements of the array at e.Array (a dot path from the record) whose
// e.Field is one of e.Values, preserving every other byte's order. It returns how many
// elements were removed.
func dropElems(line []byte, e agent.ElemMatch) ([]byte, int, error) {
	keys := strings.Split(e.Array, ".")
	removed := 0
	out, err := dropIn(line, keys, e, &removed)
	if err != nil || removed == 0 {
		return line, 0, err
	}
	return out, removed, nil
}

func dropIn(obj []byte, keys []string, e agent.ElemMatch, removed *int) ([]byte, error) {
	if len(obj) == 0 || obj[0] != '{' {
		return obj, nil
	}
	members, err := parseObject(obj)
	if err != nil {
		return obj, err
	}
	for i, m := range members {
		if m.key != keys[0] {
			continue
		}
		if len(keys) > 1 {
			v, err := dropIn(m.val, keys[1:], e, removed)
			if err != nil {
				return obj, err
			}
			members[i].val = v
			continue
		}
		if len(m.val) == 0 || m.val[0] != '[' {
			continue
		}
		var elems []json.RawMessage
		if err := json.Unmarshal(m.val, &elems); err != nil {
			return obj, err
		}
		kept := elems[:0]
		for _, el := range elems {
			var f map[string]json.RawMessage
			var v string
			if json.Unmarshal(el, &f) == nil && json.Unmarshal(f[e.Field], &v) == nil && contains(e.Values, v) {
				*removed++
				continue
			}
			kept = append(kept, el)
		}
		var arr bytes.Buffer
		arr.WriteByte('[')
		for k, el := range kept {
			if k > 0 {
				arr.WriteByte(',')
			}
			arr.Write(el)
		}
		arr.WriteByte(']')
		members[i].val = arr.Bytes()
	}
	return encodeObject(members), nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
