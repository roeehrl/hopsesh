// Package schemalite validates JSON documents against the small subset of JSON Schema
// (2020-12) that the drift manifest and the test bundle's schemas use, so that those
// schemas stay the single description of the formats without a third-party validator.
// A schema that uses a keyword outside the subset is refused when it is loaded, never
// silently ignored. It is a development tool, not part of hopsesh.
//
// Supported: type (a name or a list), properties, required, additionalProperties (a
// boolean or a schema), items, enum, const, pattern, minLength, maxLength, minItems,
// maxItems, minimum, maximum, $ref to "#/$defs/<name>", $defs, and the annotations
// $schema, $id, $comment, title, description and examples.
package schemalite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Schema is a loaded schema.
type Schema struct {
	root map[string]any
}

var known = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true, "items": true,
	"enum": true, "const": true, "pattern": true, "minLength": true, "maxLength": true, "minItems": true,
	"maxItems": true, "minimum": true, "maximum": true, "$ref": true, "$defs": true,
	"$schema": true, "$id": true, "$comment": true, "title": true, "description": true, "examples": true,
}

// Load reads a schema file.
func Load(path string) (*Schema, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse reads a schema and checks that it uses only the supported keywords.
func Parse(b []byte) (*Schema, error) {
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	s := &Schema{root: root}
	if err := s.check(root, "#"); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Schema) check(n map[string]any, at string) error {
	for k, v := range n {
		if !known[k] {
			return fmt.Errorf("schema %s: keyword %q is not supported", at, k)
		}
		switch k {
		case "properties", "$defs":
			m, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("schema %s/%s: not an object", at, k)
			}
			for name, sub := range m {
				sm, ok := sub.(map[string]any)
				if !ok {
					return fmt.Errorf("schema %s/%s/%s: not a schema", at, k, name)
				}
				if err := s.check(sm, at+"/"+k+"/"+name); err != nil {
					return err
				}
			}
		case "items", "additionalProperties":
			switch sv := v.(type) {
			case bool:
				if k == "items" {
					return fmt.Errorf("schema %s/items: must be a schema", at)
				}
			case map[string]any:
				if err := s.check(sv, at+"/"+k); err != nil {
					return err
				}
			default:
				return fmt.Errorf("schema %s/%s: not a schema", at, k)
			}
		case "pattern":
			p, ok := v.(string)
			if !ok {
				return fmt.Errorf("schema %s/pattern: not a string", at)
			}
			if _, err := regexp.Compile(p); err != nil {
				return fmt.Errorf("schema %s/pattern: %w", at, err)
			}
		case "$ref":
			r, ok := v.(string)
			if !ok {
				return fmt.Errorf("schema %s/$ref: not a string", at)
			}
			if _, err := s.resolve(r); err != nil {
				return fmt.Errorf("schema %s: %w", at, err)
			}
		}
	}
	return nil
}

func (s *Schema) resolve(ref string) (map[string]any, error) {
	name, ok := strings.CutPrefix(ref, "#/$defs/")
	if !ok {
		return nil, fmt.Errorf("$ref %q: only #/$defs/<name> is supported", ref)
	}
	defs, _ := s.root["$defs"].(map[string]any)
	d, ok := defs[name].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("$ref %q: no such definition", ref)
	}
	return d, nil
}

// Validate checks a JSON document. It returns every problem found, each with the JSON
// path where it is ("$.targets[2].id"), sorted.
func (s *Schema) Validate(doc []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	if dec.More() {
		return []string{"not JSON: more than one value"}
	}
	var errs []string
	s.validate(s.root, v, "$", &errs)
	sort.Strings(errs)
	return errs
}

func typeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		if _, err := x.Int64(); err == nil {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func typeMatches(want, got string) bool {
	return want == got || want == "number" && got == "integer"
}

func (s *Schema) validate(n map[string]any, v any, at string, errs *[]string) {
	bad := func(f string, a ...any) { *errs = append(*errs, at+": "+fmt.Sprintf(f, a...)) }
	if r, ok := n["$ref"].(string); ok {
		d, _ := s.resolve(r) // checked at load
		s.validate(d, v, at, errs)
	}
	got := typeOf(v)
	if t, ok := n["type"]; ok {
		var types []string
		switch tt := t.(type) {
		case string:
			types = []string{tt}
		case []any:
			for _, x := range tt {
				if xs, ok := x.(string); ok {
					types = append(types, xs)
				}
			}
		}
		if !slices.ContainsFunc(types, func(w string) bool { return typeMatches(w, got) }) {
			bad("is %s, want %s", got, strings.Join(types, " or "))
			return
		}
	}
	if c, ok := n["const"]; ok && !equal(c, v) {
		bad("is %s, want %s", show(v), show(c))
	}
	if e, ok := n["enum"].([]any); ok && !slices.ContainsFunc(e, func(x any) bool { return equal(x, v) }) {
		var opts []string
		for _, x := range e {
			opts = append(opts, show(x))
		}
		bad("is %s, want one of %s", show(v), strings.Join(opts, ", "))
	}
	switch x := v.(type) {
	case string:
		if p, ok := n["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(x) {
			bad("%s does not match %s", show(v), p)
		}
		l := float64(len([]rune(x)))
		if m, ok := num(n["minLength"]); ok && l < m {
			bad("shorter than %v characters", m)
		}
		if m, ok := num(n["maxLength"]); ok && l > m {
			bad("longer than %v characters", m)
		}
	case json.Number:
		f, _ := x.Float64()
		if m, ok := num(n["minimum"]); ok && f < m {
			bad("%v is less than %v", f, m)
		}
		if m, ok := num(n["maximum"]); ok && f > m {
			bad("%v is more than %v", f, m)
		}
	case []any:
		if m, ok := num(n["minItems"]); ok && float64(len(x)) < m {
			bad("has fewer than %v items", m)
		}
		if m, ok := num(n["maxItems"]); ok && float64(len(x)) > m {
			bad("has more than %v items", m)
		}
		if it, ok := n["items"].(map[string]any); ok {
			for i, e := range x {
				s.validate(it, e, fmt.Sprintf("%s[%d]", at, i), errs)
			}
		}
	case map[string]any:
		if req, ok := n["required"].([]any); ok {
			for _, r := range req {
				if k, ok := r.(string); ok {
					if _, has := x[k]; !has {
						bad("%s is required", k)
					}
				}
			}
		}
		props, _ := n["properties"].(map[string]any)
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if p, ok := props[k].(map[string]any); ok {
				s.validate(p, x[k], at+"."+k, errs)
				continue
			}
			switch ap := n["additionalProperties"].(type) {
			case bool:
				if !ap {
					bad("%s is not allowed", k)
				}
			case map[string]any:
				s.validate(ap, x[k], at+"."+k, errs)
			}
		}
	}
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return math.NaN(), false
}

// equal compares a schema value (decoded without UseNumber) with a document value.
func equal(a, b any) bool {
	return show(a) == show(b)
}

func show(v any) string {
	if n, ok := v.(json.Number); ok {
		f, _ := n.Float64()
		v = f
	}
	b, _ := json.Marshal(v)
	return string(b)
}
