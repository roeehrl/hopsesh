package fakeopencode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// OpenCode writes JSON with JavaScript's JSON.stringify: keys in the order its schemas
// declare them, numbers as JavaScript prints them, and only quotes, backslashes and
// control characters escaped. value keeps a document's keys in order, so the stand-in can
// put them in the schema's order and print them the same way.

type value struct {
	kind byte     // 'o' object, 'a' array, 's' string, 'l' a literal: number, true, false or null
	keys []string // an object's keys, in order
	vals []*value // an object's values (by keys) or an array's items
	s    string   // a string, or a literal's text
}

func str(s string) *value    { return &value{kind: 's', s: s} }
func lit(s string) *value    { return &value{kind: 'l', s: s} }
func num(n int64) *value     { return lit(strconv.FormatInt(n, 10)) }
func obj() *value            { return &value{kind: 'o'} }
func arr() *value            { return &value{kind: 'a'} }
func (v *value) isObj() bool { return v != nil && v.kind == 'o' }

// parseJSON reads a document, numbers kept as written.
func parseJSON(b []byte) (*value, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, err := parseValue(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("more than one JSON value")
	}
	return v, nil
}

func parseValue(d *json.Decoder) (*value, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			v := obj()
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return nil, err
				}
				e, err := parseValue(d)
				if err != nil {
					return nil, err
				}
				v.set(k.(string), e)
			}
			_, err := d.Token()
			return v, err
		case '[':
			v := arr()
			for d.More() {
				e, err := parseValue(d)
				if err != nil {
					return nil, err
				}
				v.vals = append(v.vals, e)
			}
			_, err := d.Token()
			return v, err
		}
		return nil, fmt.Errorf("unexpected %v", x)
	case string:
		return str(x), nil
	case json.Number:
		return lit(x.String()), nil
	case bool:
		return lit(strconv.FormatBool(x)), nil
	case nil:
		return lit("null"), nil
	}
	return nil, fmt.Errorf("unexpected %v", t)
}

// get is an object's member (nil when it has none, or v is not an object).
func (v *value) get(k string) *value {
	if !v.isObj() {
		return nil
	}
	for i, key := range v.keys {
		if key == k {
			return v.vals[i]
		}
	}
	return nil
}

// set replaces a member, or adds it at the end.
func (v *value) set(k string, x *value) {
	for i, key := range v.keys {
		if key == k {
			v.vals[i] = x
			return
		}
	}
	v.keys = append(v.keys, k)
	v.vals = append(v.vals, x)
}

func (v *value) del(k string) {
	for i, key := range v.keys {
		if key == k {
			v.keys = append(v.keys[:i:i], v.keys[i+1:]...)
			v.vals = append(v.vals[:i:i], v.vals[i+1:]...)
			return
		}
	}
}

// text is a string member's value ("" when it is not a string).
func (v *value) text(k string) string {
	if x := v.get(k); x != nil && x.kind == 's' {
		return x.s
	}
	return ""
}

// number is a numeric member's value.
func (v *value) number(k string) (float64, bool) {
	x := v.get(k)
	if x == nil || x.kind != 'l' {
		return 0, false
	}
	f, err := strconv.ParseFloat(x.s, 64)
	return f, err == nil
}

// int is a whole-number member's value.
func (v *value) int(k string) (int64, bool) {
	f, ok := v.number(k)
	if !ok || f != math.Trunc(f) {
		return 0, false
	}
	return int64(f), true
}

func (v *value) clone() *value {
	if v == nil {
		return nil
	}
	c := &value{kind: v.kind, s: v.s, keys: append([]string(nil), v.keys...)}
	for _, e := range v.vals {
		c.vals = append(c.vals, e.clone())
	}
	return c
}

// reorder puts the given keys first, in that order, and the others after them as they
// were: how JavaScript orders the members of a schema-encoded object.
func (v *value) reorder(keys ...string) {
	if !v.isObj() {
		return
	}
	var ks []string
	var vs []*value
	used := map[string]bool{}
	for _, k := range keys {
		if x := v.get(k); x != nil && !used[k] {
			ks, vs = append(ks, k), append(vs, x)
			used[k] = true
		}
	}
	for i, k := range v.keys {
		if !used[k] {
			ks, vs = append(ks, k), append(vs, v.vals[i])
		}
	}
	v.keys, v.vals = ks, vs
}

// compact prints v as JSON.stringify(v) does.
func (v *value) compact() []byte {
	var b bytes.Buffer
	v.write(&b)
	return b.Bytes()
}

// pretty prints v as JSON.stringify(v, null, 2) does.
func (v *value) pretty() []byte {
	var b bytes.Buffer
	if err := json.Indent(&b, v.compact(), "", "  "); err != nil {
		panic(err) // compact always prints valid JSON
	}
	return b.Bytes()
}

func (v *value) write(b *bytes.Buffer) {
	switch v.kind {
	case 'o':
		b.WriteByte('{')
		for i, k := range v.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, k)
			b.WriteByte(':')
			v.vals[i].write(b)
		}
		b.WriteByte('}')
	case 'a':
		b.WriteByte('[')
		for i, e := range v.vals {
			if i > 0 {
				b.WriteByte(',')
			}
			e.write(b)
		}
		b.WriteByte(']')
	case 's':
		writeString(b, v.s)
	default:
		b.WriteString(v.s)
	}
}

// writeString quotes s as JSON.stringify does: no HTML escapes, U+2028 and U+2029 as
// they are.
func writeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
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
		case r == utf8.RuneError && size == 1:
			b.WriteString(`�`)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	b.WriteByte('"')
}

// jsNumber prints a float as JavaScript's Number#toString does.
func jsNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	if a := math.Abs(f); a >= 1e-6 && a < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'e', -1, 64) // 1e-07
	mant, exp, _ := strings.Cut(s, "e")
	sign := exp[0]
	exp = strings.TrimLeft(exp[1:], "0")
	if sign == '+' {
		return mant + "e+" + exp
	}
	return mant + "e-" + exp
}
