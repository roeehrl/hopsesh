package rewrite

import (
	"bytes"
	"encoding/json"
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

// dropThinking removes thinking and redacted_thinking blocks from message.content,
// preserving every other byte's order. Returns how many blocks were removed.
func dropThinking(line []byte) ([]byte, int, error) {
	top, err := parseObject(line)
	if err != nil {
		return line, 0, err
	}
	removed := 0
	for i, m := range top {
		if m.key != "message" || len(m.val) == 0 || m.val[0] != '{' {
			continue
		}
		msg, err := parseObject(m.val)
		if err != nil {
			return line, 0, err
		}
		for j, mm := range msg {
			if mm.key != "content" || len(mm.val) == 0 || mm.val[0] != '[' {
				continue
			}
			var blocks []json.RawMessage
			if err := json.Unmarshal(mm.val, &blocks); err != nil {
				return line, 0, err
			}
			kept := blocks[:0]
			for _, blk := range blocks {
				var t struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal(blk, &t)
				if t.Type == "thinking" || t.Type == "redacted_thinking" {
					removed++
					continue
				}
				kept = append(kept, blk)
			}
			var arr bytes.Buffer
			arr.WriteByte('[')
			for k, blk := range kept {
				if k > 0 {
					arr.WriteByte(',')
				}
				arr.Write(blk)
			}
			arr.WriteByte(']')
			msg[j].val = arr.Bytes()
		}
		top[i].val = encodeObject(msg)
	}
	if removed == 0 {
		return line, 0, nil
	}
	return encodeObject(top), removed, nil
}
