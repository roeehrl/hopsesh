package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// JSONMovementHookFile implements the shared command-hook JSON schema used by
// Claude and Codex. Keeping unknown values as RawMessage avoids numeric rounding
// or dropping future vendor fields. Only exact generated handlers are removed.
func JSONMovementHookFile(path, command string) *HookFile {
	return JSONMovementHookFileWithHandler(path, map[string]any{"type": "command", "command": command, "timeout": 5})
}

// JSONMovementHookFileWithHandler retains module-owned shell and platform fields.
func JSONMovementHookFileWithHandler(path string, handler map[string]any) *HookFile {
	own, _ := json.Marshal(handler)
	events := []string{"SessionStart", "UserPromptSubmit"}
	edit := func(existing []byte, install bool) ([]byte, error) {
		doc, hooks, err := parseHookDocument(existing)
		if err != nil {
			return nil, err
		}
		changed := false
		for _, event := range events {
			var groups []map[string]json.RawMessage
			if raw, ok := hooks[event]; ok {
				if err := json.Unmarshal(raw, &groups); err != nil || groups == nil {
					return nil, fmt.Errorf("invalid %s hook groups; not changing settings", event)
				}
			}
			found := false
			kept := make([]map[string]json.RawMessage, 0, len(groups))
			for _, g := range groups {
				if g == nil {
					return nil, fmt.Errorf("invalid %s hook group", event)
				}
				var hs []json.RawMessage
				if err := json.Unmarshal(g["hooks"], &hs); err != nil || hs == nil {
					return nil, fmt.Errorf("invalid %s hook handlers", event)
				}
				unrestricted := len(g) == 1 || len(g) == 2 && (string(g["matcher"]) == `""` || string(g["matcher"]) == `"*"`)
				next := make([]json.RawMessage, 0, len(hs))
				for _, h := range hs {
					match := equalHookJSON(h, own) && unrestricted
					if match {
						found = true
					}
					if match && !install {
						changed = true
						continue
					}
					next = append(next, h)
				}
				if len(next) == 0 && len(hs) > 0 && !install && unrestricted {
					continue
				}
				if len(next) != len(hs) {
					g["hooks"], _ = json.Marshal(next)
				}
				kept = append(kept, g)
			}
			if install && !found {
				raw, _ := json.Marshal([]json.RawMessage{own})
				kept = append(kept, map[string]json.RawMessage{"hooks": raw})
				changed = true
			}
			if len(kept) > 0 {
				hooks[event], _ = json.Marshal(kept)
			} else {
				delete(hooks, event)
			}
		}
		if !changed {
			return existing, nil
		}
		if len(hooks) == 0 {
			delete(doc, "hooks")
		} else {
			doc["hooks"], _ = json.Marshal(hooks)
		}
		b, err := json.MarshalIndent(doc, "", "  ")
		return append(b, '\n'), err
	}
	return &HookFile{Path: path,
		Merge:  func(b []byte) ([]byte, error) { return edit(b, true) },
		Remove: func(b []byte) ([]byte, error) { return edit(b, false) },
		Has:    func(b []byte) bool { next, err := edit(b, true); return err == nil && bytes.Equal(next, b) },
	}
}

func parseHookDocument(b []byte) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	doc := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, &doc); err != nil || doc == nil {
			return nil, nil, fmt.Errorf("hook settings must be a JSON object; not changing settings")
		}
	}
	hooks := map[string]json.RawMessage{}
	if raw, ok := doc["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil || hooks == nil {
			return nil, nil, fmt.Errorf("hooks must be a JSON object; not changing settings")
		}
	}
	return doc, hooks, nil
}
func equalHookJSON(a, b []byte) bool {
	decode := func(raw []byte) any {
		var v any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		_ = d.Decode(&v)
		return v
	}
	return reflect.DeepEqual(decode(a), decode(b))
}
