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
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
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
	repo := repoRoot()
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
		if err := demoHome(repo, h); err != nil {
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

// repoRoot is the module's folder (this file's, three levels up).
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// demoHome makes a home with a git repository, Claude Code sessions and Codex threads (the
// modules' fixtures, paths pointed at it), and points every agent and hopsesh at it.
func demoHome(repo, h string) error {
	for k, v := range map[string]string{
		"HOME": h, "USERPROFILE": h, "HOPSESH_CONFIG_DIR": filepath.Join(h, "config"),
		"HOPSESH_STATE_DIR": filepath.Join(h, "state"), "HOPSESH_MACHINE": "studio", "CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "",
		"HOPSESH_TAILSCALE": "off", // never show this machine's real tailnet
	} {
		os.Setenv(k, v)
	}
	demo := filepath.Join(h, "git", "demo")
	if err := os.MkdirAll(demo, 0o700); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"remote", "add", "origin", "https://github.com/example/demo.git"},
		{"-c", "user.name=demo", "-c", "user.email=demo@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = demo
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v %s", args, err, out)
		}
	}
	esc, _ := json.Marshal(demo)
	jsonDemo := string(esc[1 : len(esc)-1])
	copyTree := func(src, dst string) error {
		return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			// No account files, and no running-session registry (it would look open).
			if strings.Contains(rel, "auth.json") || strings.Contains(rel, "credentials") || strings.HasPrefix(filepath.ToSlash(rel), "sessions/4242") || strings.Contains(rel, "locks") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			b = []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", jsonDemo))
			rel = strings.ReplaceAll(rel, "-home-u-git-demo", claude.Slug(demo))
			out := filepath.Join(dst, rel)
			if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
				return err
			}
			return os.WriteFile(out, b, 0o600)
		})
	}
	if err := copyTree(filepath.Join(repo, "agents", "claude", "testdata", "2.1.284"), filepath.Join(h, ".claude")); err != nil {
		return err
	}
	return copyTree(filepath.Join(repo, "agents", "codex", "testdata", "0.153.2"), filepath.Join(h, ".codex"))
}
