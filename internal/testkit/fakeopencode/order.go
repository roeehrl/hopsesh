package fakeopencode

// OpenCode 2.3.0's message schemas (packages/schema/src/session-message.ts), as the order
// in which it prints their members. An import drops the members they do not declare
// (decode.go); rows written some other way keep theirs, after the known ones.

// messageKeys are each message type's members. Base is id, metadata, time; a type that
// declares time again keeps it in Base's place.
var messageKeys = map[string][]string{
	"agent-switched": {"id", "metadata", "time", "type", "agent", "previous"},
	"model-switched": {"id", "metadata", "time", "type", "model", "previous"},
	// As OpenCode 2.3.0 prints it, though its schema declares location first.
	"location-switched": {"id", "metadata", "time", "type", "projectID", "subpath", "location", "previous"},
	"user":              {"id", "metadata", "time", "text", "files", "agents", "skills", "type"},
	"synthetic":         {"id", "metadata", "time", "text", "description", "type"},
	"system":            {"id", "metadata", "time", "type", "text", "description"},
	"skill":             {"id", "metadata", "time", "type", "skill", "name", "text"},
	"shell":             {"id", "metadata", "time", "type", "shellID", "command", "status", "exit", "output"},
	"assistant":         {"id", "metadata", "time", "type", "agent", "model", "content", "snapshot", "finish", "rawFinish", "native", "cost", "tokens", "error", "retry"},
	"idle":              {"id", "metadata", "time", "type", "outcome", "error"},
}

// compactionKeys are a compaction's members, by its status.
var compactionKeys = map[string][]string{
	"running":   {"type", "id", "metadata", "time", "status", "reason", "summary"},
	"completed": {"type", "id", "metadata", "time", "status", "reason", "model", "native", "summary", "cost", "tokens"},
	"failed":    {"type", "id", "metadata", "time", "status", "reason", "error", "cost", "tokens"},
}

// orderMessage puts a message's members, and theirs, in the schema's order.
func orderMessage(m *value) {
	t := m.text("type")
	if t == "compaction" {
		m.reorder(compactionKeys[m.text("status")]...)
	} else {
		m.reorder(messageKeys[t]...)
	}
	switch t {
	case "assistant":
		m.get("time").reorder("created", "firstToken", "streamed", "completed")
		if c := m.get("content"); c != nil && c.kind == 'a' {
			for _, item := range c.vals {
				orderContent(item)
			}
		}
		m.get("snapshot").reorder("start", "end", "files")
		if r := m.get("retry"); r != nil {
			r.reorder("attempt", "at", "error")
			orderError(r.get("error"))
		}
	case "shell":
		m.get("time").reorder("created", "completed")
		m.get("output").reorder("output", "cursor", "size", "truncated")
	case "location-switched":
		m.get("location").reorder("directory", "workspaceID")
		if p := m.get("previous"); p != nil {
			p.reorder("location", "projectID", "subpath")
			p.get("location").reorder("directory", "workspaceID")
		}
	case "model-switched":
		orderModel(m.get("previous"))
	case "user":
		if a := m.get("agents"); a != nil && a.kind == 'a' {
			for _, x := range a.vals {
				x.reorder("name", "mention")
				x.get("mention").reorder("start", "end", "text")
			}
		}
		if s := m.get("skills"); s != nil && s.kind == 'a' {
			for _, x := range s.vals {
				x.reorder("id", "name", "text", "mention")
				x.get("mention").reorder("start", "end", "text")
			}
		}
	}
	orderModel(m.get("model"))
	orderTokens(m.get("tokens"))
	orderError(m.get("error"))
}

// orderContent orders one item of an assistant message's content.
func orderContent(c *value) {
	switch c.text("type") {
	case "text":
		c.reorder("type", "text", "native")
	case "reasoning":
		c.reorder("type", "text", "native", "time")
		c.get("time").reorder("created", "completed")
	case "tool":
		c.reorder("type", "id", "name", "executed", "native", "state", "time")
		c.get("time").reorder("created", "ran", "completed")
		s := c.get("state")
		switch s.text("status") {
		case "streaming":
			s.reorder("status", "input")
		case "running":
			s.reorder("status", "input", "metadata")
		case "completed":
			s.reorder("status", "input", "content", "metadata")
		case "error":
			s.reorder("status", "input", "error", "content", "metadata")
			orderError(s.get("error"))
		}
		if items := s.get("content"); items != nil && items.kind == 'a' {
			for _, x := range items.vals {
				if x.text("type") == "file" {
					x.reorder("type", "uri", "mime", "name")
				} else {
					x.reorder("type", "text")
				}
			}
		}
	}
}

func orderModel(m *value) { m.reorder("id", "providerID", "variant") }

func orderTokens(t *value) {
	t.reorder("input", "output", "reasoning", "cache")
	t.get("cache").reorder("read", "write")
}

func orderError(e *value) {
	e.reorder("type", "message", "status", "response", "retryAfterMs")
}

// settled reports whether a message is finished: OpenCode's export and import leave out
// an assistant reply still streaming, and a shell command or compaction still running.
func settled(m *value) bool {
	switch m.text("type") {
	case "assistant":
		return m.get("time").get("completed") != nil
	case "shell", "compaction":
		return m.text("status") != "running"
	}
	return true
}
