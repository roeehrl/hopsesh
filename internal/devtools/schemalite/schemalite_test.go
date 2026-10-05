package schemalite

import (
	"strings"
	"testing"
)

const schema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "items"],
  "properties": {
    "id": {"type": "string", "pattern": "^[a-z]+$", "maxLength": 5},
    "kind": {"enum": ["a", "b"]},
    "v": {"const": 1},
    "n": {"type": "integer", "minimum": 1, "maximum": 3},
    "items": {"type": ["array", "null"], "minItems": 1, "items": {"$ref": "#/$defs/item"}},
    "extra": {"type": "object", "additionalProperties": {"type": "string"}}
  },
  "$defs": {"item": {"type": "object", "required": ["x"], "properties": {"x": {"type": "number"}}}}
}`

func TestValidate(t *testing.T) {
	s, err := Parse([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		doc  string
		want []string // substrings, one per error; nil for a valid document
	}{
		{`{"id": "abc", "items": [{"x": 1.5}], "kind": "a", "v": 1, "n": 2, "extra": {"k": "v"}}`, nil},
		{`{"id": "abc", "items": null}`, nil},
		{`{"id": "ABC", "items": []}`, []string{"$.id: \"ABC\" does not match", "$.items: has fewer than 1"}},
		{`{"id": "abcdef", "items": [{}], "kind": "c", "v": 2, "n": 4, "zz": 1}`, []string{
			"$.id: longer than 5", "$.items[0]: x is required", "$.kind: is \"c\", want one of", "$.n: 4 is more than 3",
			"$.v: is 2, want 1", "$: zz is not allowed"}},
		{`{"items": [{"x": "1"}], "extra": {"k": 1}, "n": 1.5}`, []string{
			"$.extra.k: is integer, want string", "$.items[0].x: is string, want number", "$.n: is number, want integer", "$: id is required"}},
		{`[1]`, []string{"$: is array, want object"}},
		{`{} {}`, []string{"more than one value"}},
	} {
		got := s.Validate([]byte(c.doc))
		if len(got) != len(c.want) {
			t.Errorf("%s: got %q, want %d errors", c.doc, got, len(c.want))
			continue
		}
		for i, w := range c.want {
			// Errors are sorted; the expectations are listed in the same order.
			if !strings.Contains(got[i], w) {
				t.Errorf("%s: error %d is %q, want it to contain %q", c.doc, i, got[i], w)
			}
		}
	}
}

func TestUnsupportedKeyword(t *testing.T) {
	for _, s := range []string{
		`{"oneOf": []}`,
		`{"properties": {"a": {"format": "uri"}}}`,
		`{"$ref": "#/definitions/x"}`,
		`{"$ref": "#/$defs/missing"}`,
		`{"pattern": "("}`,
	} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("%s: accepted", s)
		}
	}
}
