// Command webtest serves the desktop app's window in a browser, for tests and screenshots:
// the real assets and the real service (internal/ui/gui) on a demo home made from the
// agents' test fixtures, with a stand-in for the Wails runtime. It is not shipped.
//
//	go run ./internal/devtools/webtest -addr 127.0.0.1:8765 -home /tmp/demo
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/internal/ui/gui"
)

// shim stands in for /wails/runtime.js: calls go to /call, and tests raise events with
// window.__emit(name, data).
const shim = `
const listeners = {};
export const Call = { ByName: async (name, ...args) => {
  const r = await fetch("/call", { method: "POST", body: JSON.stringify({ m: name.split(".").pop(), args }) });
  const j = await r.json();
  if (j.error) throw new Error(j.error);
  return j.result;
} };
export const Events = { On: (name, fn) => { (listeners[name] ||= []).push(fn); return () => { listeners[name] = listeners[name].filter((f) => f !== fn); }; } };
window.__emit = (name, data) => (listeners[name] || []).forEach((f) => f({ data }));
`

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "where to listen")
	home := flag.String("home", "", "the demo home (made afresh; anything there is removed)")
	flag.Parse()
	if *home == "" {
		log.Fatal("-home is required")
	}
	h, err := filepath.Abs(*home)
	if err != nil {
		log.Fatal(err)
	}
	var (
		mu  sync.Mutex
		svc *gui.App
	)
	// fresh starts over from a new demo home (POST /reset, so tests are independent).
	fresh := func() error {
		mu.Lock()
		defer mu.Unlock()
		if svc != nil {
			svc.Shutdown()
		}
		if err := os.RemoveAll(h); err != nil {
			return err
		}
		for k, v := range testkit.Env(h) {
			os.Setenv(k, v)
		}
		if err := testkit.DemoHome(h); err != nil {
			return err
		}
		svc = gui.NewApp(all.Registry())
		return nil
	}
	if err := fresh(); err != nil {
		log.Fatal(err)
	}
	assets, err := fs.Sub(gui.Assets, "assets")
	if err != nil {
		log.Fatal(err)
	}
	http.HandleFunc("/wails/runtime.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, shim)
	})
	http.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		s := svc
		mu.Unlock()
		call(w, r, s)
	})
	http.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if err := fresh(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	files := http.FileServer(http.FS(assets))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
	log.Printf("serving the window at http://%s (home %s)", *addr, h)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// call runs one service method by name with JSON arguments, the way Wails binds them.
func call(w http.ResponseWriter, r *http.Request, svc *gui.App) {
	var req struct {
		M    string            `json:"m"`
		Args []json.RawMessage `json:"args"`
	}
	out := map[string]any{}
	var m reflect.Value
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
		m = reflect.ValueOf(svc).MethodByName(req.M)
	}
	if !m.IsValid() {
		out["error"] = "no method " + req.M
	} else {
		in := make([]reflect.Value, m.Type().NumIn())
		for i := range in {
			p := reflect.New(m.Type().In(i))
			if i < len(req.Args) {
				if err := json.Unmarshal(req.Args[i], p.Interface()); err != nil {
					out["error"] = err.Error()
				}
			}
			in[i] = p.Elem()
		}
		if out["error"] == nil {
			for _, v := range m.Call(in) {
				if e, ok := v.Interface().(error); ok && e != nil {
					out["error"] = e.Error()
				} else if v.Type().String() != "error" {
					out["result"] = v.Interface()
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
