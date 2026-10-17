package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/ui/gui"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// This world writes only fixture ledgers, then restarts only the browser-test
// service. CachedScan must restore actual catalog metadata without an InitialScan.
func registerCachedMovementWorld(mu *sync.RWMutex, home string, svc **gui.App) {
	http.HandleFunc("/cached-movement-world", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(filepath.Base(home), "hopsesh-webtest-") {
			http.Error(w, "POST on a browser fixture home only", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		cfg, err := config.Load()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		core := app.New(cfg, all.Registry(), config.StateDir(), nil)
		defer core.Catalog.Close()
		inv := core.Scan(r.Context(), app.ScanOptions{SkipGit: true})
		defer inv.Close()
		var pair []app.Entry
		for _, id := range []agent.ID{"claude", "codex"} {
			for _, e := range inv.Entries {
				if e.Agent == id && (id == "codex" || e.Session.Title == "Find the codeword") {
					pair = append(pair, e)
					break
				}
			}
		}
		if len(pair) != 2 {
			http.Error(w, "fixture needs both agents", 500)
			return
		}
		g := lineage.New("cached-browser-movement")
		var ids []lineage.ReplicaID
		var states []lineage.StateID
		for _, e := range pair {
			machine := inv.Machine(e.Machine)
			in, _ := machine.InstallProfile(e.Agent, e.Session.Key.Profile)
			mod, _ := core.Module(e.Agent)
			h, err := machine.Host().For(r.Context(), mod.Spec(), in, nil)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			seg, err := mod.(agent.Reader).Read(r.Context(), h, in, e.Session, ir.Cursor{})
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			id, state, err := g.ObserveBinding(lineage.Replica{Key: e.Session.Key, Endpoint: machine.Host().Facts.Endpoint, Binding: in.BindingID(), Location: e.Machine}, &seg)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			ids, states = append(ids, id), append(states, state.ID)
		}
		at := time.Now().Add(-time.Hour)
		for i := range 4 {
			if err := g.AppendHop(lineage.Hop{ID: fmt.Sprintf("cached-hop-%d", i+1), Time: at.Add(time.Duration(i) * time.Minute), Kind: lineage.HopContinue, From: ids[i%2], To: ids[(i+1)%2], Source: states[i%2], Target: states[(i+1)%2], Notify: true}); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		for _, e := range pair {
			root, rootErr := filepath.EvalSymlinks(home)
			path, pathErr := filepath.EvalSymlinks(e.Session.Path)
			rel, err := filepath.Rel(root, path)
			if rootErr != nil || pathErr != nil || err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				http.Error(w, "session is outside the fixture home", 500)
				return
			}
			if err := os.WriteFile(lineage.PathFor(e.Session.Path), g.Encode(), 0600); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		core.Scan(context.Background(), app.ScanOptions{SkipGit: true}).Close()
		if r.URL.Query().Get("legacy") == "1" {
			// Reproduce dev18's stored catalog, which omitted movement notices.
			core.Catalog.Update(core.CatalogScope(), func(b []byte) any {
				var saved map[string]any
				_ = json.Unmarshal(b, &saved)
				for _, row := range saved["entries"].([]any) {
					delete(row.(map[string]any), "movement")
				}
				return saved
			})
		}
		(*svc).Shutdown()
		*svc = gui.NewApp(all.Registry())
		(*svc).Emitter = relay
		_ = json.NewEncoder(w).Encode(map[string]any{"claude": pair[0], "codex": pair[1]})
	})
}
