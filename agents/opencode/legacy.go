package opencode

import "encoding/json"

// Messages written before OpenCode 2.1 keep their old shape in the database: replay data
// named state or providerState (a tool's result data providerResultState), a summary
// compaction's kept text in recent, and a provider compaction's window in
// providerContext. OpenCode upgrades every row it reads and every export it decodes
// (schema/src/session-legacy.ts, SessionLegacy.message and part); the module does the
// same, so it reads, exports and protects them as OpenCode does.

type jsonObject = map[string]json.RawMessage

// upgradeMessage is SessionLegacy.message: a message's fields in the current shape. It
// reports whether it changed them.
func upgradeMessage(typ string, f jsonObject) bool {
	switch typ {
	case "assistant":
		changed := rename(f, "providerState", "native")
		return upgradeContent(f) || changed
	case "compaction":
		changed := rename(f, "providerState", "native")
		changed = keptText(f) || changed
		return providerContext(f) || changed
	}
	return false
}

// upgradeContent upgrades each item of an assistant's content.
func upgradeContent(f jsonObject) bool {
	var items []json.RawMessage
	if json.Unmarshal(f["content"], &items) != nil || items == nil {
		return false
	}
	changed := false
	for i, raw := range items {
		if up, ok := upgradePart(raw); ok {
			items[i], changed = up, true
		}
	}
	if changed {
		f["content"], _ = json.Marshal(items)
	}
	return changed
}

// upgradePart is SessionLegacy.part: a text or reasoning item's state is its replay data;
// a tool's own state is its status, its providerState and providerResultState are.
func upgradePart(raw json.RawMessage) (json.RawMessage, bool) {
	p, ok := record(raw)
	if !ok {
		return raw, false
	}
	changed := false
	switch str(p["type"]) {
	case "text", "reasoning":
		changed = rename(p, "state", "native")
	case "tool":
		changed = rename(p, "providerState", "native")
		if res, found := p["providerResultState"]; found {
			delete(p, "providerResultState")
			changed = true
			if r, ok := record(res); ok {
				native, _ := record(p["native"])
				if native == nil {
					native = jsonObject{}
				}
				for k, v := range r {
					native[k] = v
				}
				p["native"], _ = json.Marshal(native)
			}
		}
	}
	if !changed {
		return raw, false
	}
	b, err := json.Marshal(p)
	if err != nil {
		return raw, false
	}
	return b, true
}

// keptText moves the text a summary compaction kept (recent) into its metadata, where
// replay reads it; metadata's own recent wins.
func keptText(f jsonObject) bool {
	raw, found := f["recent"]
	if !found {
		return false
	}
	delete(f, "recent")
	if text := str(raw); text != "" {
		meta, _ := record(f["metadata"])
		if meta == nil {
			meta = jsonObject{}
		}
		if _, has := meta["recent"]; !has {
			meta["recent"] = raw
		}
		f["metadata"], _ = json.Marshal(meta)
	}
	return true
}

// providerContext moves a provider compaction's window to native.messages (the native
// stored beside it was never replayed), upgrading media written before 2.0.15.
func providerContext(f jsonObject) bool {
	raw, found := f["providerContext"]
	if !found {
		return false
	}
	delete(f, "providerContext")
	pc, ok := record(raw)
	if !ok {
		return true
	}
	native := jsonObject{}
	if msgs, found := pc["messages"]; found {
		var list []json.RawMessage
		if json.Unmarshal(msgs, &list) == nil && list != nil {
			for i, m := range list {
				list[i] = windowMedia(m)
			}
			msgs, _ = json.Marshal(list)
		}
		native["messages"] = msgs
	}
	f["native"], _ = json.Marshal(native)
	return true
}

// windowMedia is a window message whose media parts stored mediaType and base64 data
// directly, in the current shape (media.source).
func windowMedia(raw json.RawMessage) json.RawMessage {
	m, ok := record(raw)
	if !ok {
		return raw
	}
	var parts []json.RawMessage
	if json.Unmarshal(m["content"], &parts) != nil || parts == nil {
		return raw
	}
	changed := false
	for i, pr := range parts {
		p, ok := record(pr)
		if !ok || str(p["type"]) != "media" {
			continue
		}
		if _, has := p["media"]; has {
			continue
		}
		data := p["data"]
		if len(data) == 0 || data[0] != '"' {
			continue // not a string
		}
		source := jsonObject{"type": json.RawMessage(`"base64"`), "data": data}
		if mt, ok := p["mediaType"]; ok {
			source["mediaType"] = mt
		}
		delete(p, "data")
		delete(p, "mediaType")
		p["media"], _ = json.Marshal(jsonObject{"source": mustJSON(source)})
		parts[i], changed = mustJSON(p), true
	}
	if !changed {
		return raw
	}
	m["content"] = mustJSON(parts)
	return mustJSON(m)
}

// rename moves a field to its current name; the current name wins when both are there.
func rename(f jsonObject, from, to string) bool {
	v, found := f[from]
	if !found {
		return false
	}
	delete(f, from)
	if _, has := f[to]; !has {
		f[to] = v
	}
	return true
}

// record decodes a JSON object (nil and false for anything else).
func record(raw json.RawMessage) (jsonObject, bool) {
	var f jsonObject
	if json.Unmarshal(raw, &f) != nil || f == nil {
		return nil, false
	}
	return f, true
}

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// upgradeRaw is a whole message (an export's, or a line of a session's file) in the
// current shape; raw itself when it already is.
func upgradeRaw(raw json.RawMessage) json.RawMessage {
	f, ok := record(raw)
	if !ok || !upgradeMessage(str(f["type"]), f) {
		return raw
	}
	b, err := json.Marshal(f)
	if err != nil {
		return raw
	}
	return b
}
