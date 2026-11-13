package opencode

import (
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// dbFile is the database OpenCode keeps its sessions in (OpenCode's databasePath):
// OPENCODE_DB when set (Detect resolved it into the "db" root), else opencode.db in the
// data root, which the release channels use. A build of another channel names its own
// (opencode-<channel>.db); the newest one is used when there is no opencode.db.
func dbFile(h agent.Host, in agent.Install) string {
	if p := in.Root(rootDB); p != "" {
		return p
	}
	data := in.Root(rootData)
	def := h.Path().Join(data, "opencode.db")
	entries, err := h.FS().ReadDir(data)
	if err != nil {
		return def
	}
	pick, newest := "", int64(0)
	for _, e := range entries {
		n := e.Name()
		if n == "opencode.db" {
			return def
		}
		if strings.HasPrefix(n, "opencode-") && strings.HasSuffix(n, ".db") && !e.IsDir() {
			if t := e.ModTime().UnixNano(); pick == "" || t > newest {
				pick, newest = n, t
			}
		}
	}
	if pick != "" {
		return h.Path().Join(data, pick)
	}
	return def
}

// resolveDB is the database OPENCODE_DB names, relative to the data root as OpenCode
// resolves it; "" when it is unset or OpenCode keeps no file (":memory:").
func resolveDB(h agent.Host, data, env string) string {
	switch env {
	case "", ":memory:":
		return ""
	}
	if h.Path().IsAbs(env) {
		return h.Path().Clean(env)
	}
	return h.Path().Join(data, env)
}

// anchorOf is the anchor file hopsesh keeps for a session in the data root
// (agent.AnchorRel): the lineage manifest sits beside it, and a moved copy is staged
// there before OpenCode imports it.
func anchorOf(h agent.Host, in agent.Install, sid agent.SessionID) string {
	return h.Path().Join(in.Root(rootData), agent.AnchorRel(id, sid))
}

// childRel is where a child session's file goes in a bundle, beside its root session's
// anchor: hopsesh/opencode/sessions/<root>.children/<child>.jsonl.
func childRel(root, child agent.SessionID) string {
	return strings.TrimSuffix(agent.AnchorRel(id, root), ".jsonl") + ".children/" + string(child) + ".jsonl"
}

// childOf reads a child's id from its bundle path ("" when rel is no child's).
func childOf(root agent.SessionID, rel string) agent.SessionID {
	prefix := strings.TrimSuffix(agent.AnchorRel(id, root), ".jsonl") + ".children/"
	name, ok := strings.CutPrefix(rel, prefix)
	if !ok || !strings.HasSuffix(name, ".jsonl") || strings.Contains(name, "/") {
		return ""
	}
	return agent.SessionID(strings.TrimSuffix(name, ".jsonl"))
}

// desktopCLIDir is where OpenCode desktop stages the command-line programs it runs, one
// folder per version and architecture (<version>-<arch>/opencode-cli), on macOS.
func desktopCLIDir(h agent.Host) string {
	return h.Path().Join(h.Facts().Home, "Library", "Application Support", "ai.opencode.desktop", "cli")
}

// desktopApps are where OpenCode desktop installs on macOS.
var desktopApps = []string{"/Applications/OpenCode.app", "~/Applications/OpenCode.app"}
