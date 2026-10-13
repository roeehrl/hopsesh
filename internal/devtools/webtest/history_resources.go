package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/ui/gui"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Seed only a transferred demo Codex rollout. The oversized ledger is valid SDK
// lineage: actual native message text retained as a covered portable fragment.
func seedHistoryLedger(w http.ResponseWriter, r *http.Request, svc *gui.App, home string) error {
	e, err := svc.ResolveEntry(r.URL.Query().Get("machine"), r.URL.Query().Get("key"))
	if err != nil {
		return err
	}
	root, rootErr := filepath.EvalSymlinks(home)
	path, pathErr := filepath.EvalSymlinks(e.Path)
	rel, relErr := filepath.Rel(root, path)
	if rootErr != nil || pathErr != nil || relErr != nil || e.Agent != "codex" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("expected a demo Codex rollout")
	}
	key, err := agent.ParseKey(e.Key)
	if err != nil {
		return err
	}
	graph, err := lineage.Read(host.LocalFS(), path)
	if err != nil || graph == nil {
		return fmt.Errorf("expected an existing transfer ledger: %v", err)
	}
	var replica lineage.ReplicaID
	for _, v := range graph.Replicas {
		if v.Key == key && v.Location == e.Machine {
			replica = v.ID
		}
	}
	if replica == "" {
		return fmt.Errorf("transferred demo replica not found")
	}
	text := "LEDGER-BEGIN\n" + strings.Repeat("Saved conversation checkpoint. ", (17<<20)/30) + "\nLEDGER-END"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(f).Encode(map[string]any{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []map[string]string{{"type": "output_text", "text": text}}}})
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	mod := codex.New()
	in := agent.Install{Agent: "codex", Roots: map[string]string{"home": filepath.Join(root, ".codex")}, Profile: e.Profile}
	machine := &host.Machine{Name: e.Machine, Local: true, Facts: host.Facts{OS: runtime.GOOS, Home: root}}
	h, err := machine.For(context.Background(), mod.Spec(), in, nil)
	if err != nil {
		return err
	}
	seg, err := mod.Read(context.Background(), h, in, agent.Summary{Key: key, Path: path, CWD: e.CWD}, ir.Cursor{})
	if err != nil {
		return err
	}
	state, err := graph.Observe(replica, &seg)
	if err != nil {
		return err
	}
	projection := slices.Clone(state.Projection)
	last := &projection[len(projection)-1]
	last.Fragments = []ir.Fragment{{Text: text, Coverage: last.Coverage}}
	graph.Deliver(replica, seg.Cursor, projection, state.Heads, nil)
	encoded := graph.Encode()
	if _, err := lineage.Parse(encoded); err != nil {
		return err
	}
	if err := os.WriteFile(lineage.PathFor(path), encoded, 0600); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(text))
	return json.NewEncoder(w).Encode(map[string]any{"path": path, "ledger": lineage.PathFor(path), "bytes": len(encoded), "fragmentBytes": len(text), "fragmentHash": hex.EncodeToString(sum[:])})
}
