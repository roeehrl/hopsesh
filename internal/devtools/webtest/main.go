// Command webtest serves the desktop app's window in a browser, for tests and screenshots:
// the real assets and the real service (internal/ui/gui) on a demo home made from the
// agents' test fixtures, with a stand-in for the Wails runtime. It is not shipped.
//
// The demo home also has stand-in Claude Code and Codex clouds: this program, run as claude
// or codex, is the stand-in claude or codex; the demo repository's GitHub remote is a local
// bare repository; POST /cloud adds a cloud session (?cloud=codex-cloud: a Codex cloud task,
// with ?env=; it sets the failure the next driver call plays); POST /dirty
// leaves work in progress in the demo repository, for a hand-off; and a command
// the window opens "in a terminal" runs in the background instead.
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

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/internal/testkit/fakeagent"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
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
	switch strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe") {
	case "claude":
		os.Exit(fakeagent.Claude())
	case "codex":
		os.Exit(fakeagent.Codex())
	}
	addr := flag.String("addr", "127.0.0.1:8765", "where to listen")
	home := flag.String("home", "", "the demo home (made afresh; anything there is removed)")
	prepare := flag.Bool("prepare", false, "only make the demo home and print its environment as JSON (the real-window tests start the app on it)")
	world := flag.String("world", "test", "with -prepare: test (the fixtures' sessions) or empty (for demoseed to fill)")
	flag.Parse()
	if *home == "" {
		log.Fatal("-home is required")
	}
	h, err := filepath.Abs(*home)
	if err != nil {
		log.Fatal(err)
	}
	if *prepare {
		if err := os.RemoveAll(h); err != nil {
			log.Fatal(err)
		}
		env := testkit.Env(h)
		for k, v := range env {
			os.Setenv(k, v)
		}
		if *world == "empty" {
			err = os.MkdirAll(h, 0o700)
		} else {
			err = testkit.DemoHome(h)
		}
		if err != nil {
			log.Fatal(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(env); err != nil {
			log.Fatal(err)
		}
		return
	}
	var (
		mu  sync.RWMutex // calls read-lock it: a reset waits for the calls still running (a scan runs git in the demo home)
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
		if err := cloudWorld(h); err != nil {
			return err
		}
		svc = gui.NewApp(all.Registry())
		return nil
	}
	gui.SetTerminal(background)
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
		mu.RLock()
		defer mu.RUnlock()
		call(w, r, svc)
	})
	http.HandleFunc("/cloud", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		q := r.URL.Query()
		seed := seedCloud
		if q.Get("cloud") == fakecloud.CodexCloud {
			seed = func(title string, work bool) (string, error) { return seedCodex(title, q.Get("env"), work) }
		}
		id, err := seed(q.Get("title"), q.Get("work") != "0")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		os.Setenv("FAKE_CLOUD_FAIL", r.URL.Query().Get("fail"))
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	})
	http.HandleFunc("/dirty", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if err := dirty(h); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
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

// origin is the demo repository's stand-in GitHub remote.
var origin fakecloud.Origin

// cloudWorld puts the stand-in claude and codex first on PATH (the account has the Codex
// cloud environments env_api, "acme-api", and env_web, "acme-web") and points the demo
// repository's GitHub remote at a local bare repository, with main pushed there.
func cloudWorld(h string) error {
	bin := filepath.Join(h, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	for _, name := range []string{"claude", "codex"} {
		if runtime.GOOS == "windows" {
			b, err := os.ReadFile(self)
			if err == nil {
				err = os.WriteFile(filepath.Join(bin, name+".exe"), b, 0o700)
			}
			if err != nil {
				return err
			}
		} else if err := os.Symlink(self, filepath.Join(bin, name)); err != nil {
			return err
		}
	}
	gitConfig := filepath.Join(h, "gitconfig")
	for k, v := range map[string]string{"PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL": gitConfig,
		"GIT_CONFIG_NOSYSTEM": "1", "FAKE_CLOUD_DIR": filepath.Join(h, "cloud"), "FAKE_CLOUD_FAIL": "", "FAKE_CODEX_ENVS": "env_api=acme-api,env_web=acme-web",
		"GIT_AUTHOR_NAME": "Sam Doe", "GIT_AUTHOR_EMAIL": "sam@example.com", "GIT_COMMITTER_NAME": "Sam Doe", "GIT_COMMITTER_EMAIL": "sam@example.com"} {
		os.Setenv(k, v)
	}
	if origin, err = fakecloud.NewOrigin(h, "https://github.com/example/demo.git"); err != nil {
		return err
	}
	if err := origin.Redirect(gitConfig); err != nil {
		return err
	}
	if out, err := exec.Command("git", "-C", filepath.Join(h, "git", "demo"), "push", "-q", "origin", "main").CombinedOutput(); err != nil {
		return fmt.Errorf("push: %v: %s", err, out)
	}
	return nil
}

// seedCloud adds a Claude Code cloud session on the demo repository (worked: it pushed its
// work to a claude/… branch) and returns its id.
func seedCloud(title string, work bool) (string, error) {
	if title == "" {
		title = "Add rate limiting"
	}
	s, err := fakecloud.Open(os.Getenv("FAKE_CLOUD_DIR")).Seed(fakecloud.Session{Cloud: fakecloud.ClaudeCloud, Title: title, Repo: "github.com/example/demo",
		CloneURL: origin.FileURL(), Branch: "main", Code: "branch",
		Messages: []fakecloud.Message{{Role: "user", Text: "[hopsesh] " + title}, {Role: "assistant", Text: "On it."}}})
	if err != nil || !work {
		return s.ID, err
	}
	return s.ID, fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, s.ID, false)
}

// seedCodex adds a Codex cloud task on the demo repository's main in an environment
// (worked: done, with a diff) and returns its id.
func seedCodex(title, env string, work bool) (string, error) {
	if title == "" {
		title = "Add a changelog entry"
	}
	if env == "" {
		env = "env_api"
	}
	label := map[string]string{"env_api": "acme-api", "env_web": "acme-web"}[env]
	head, err := exec.Command("git", "-C", filepath.Dir(origin.Bare), "--git-dir", origin.Bare, "rev-parse", "main").Output()
	if err != nil {
		return "", err
	}
	s, err := fakecloud.Open(os.Getenv("FAKE_CLOUD_DIR")).Seed(fakecloud.Session{Cloud: fakecloud.CodexCloud, Title: title, Repo: "github.com/example/demo",
		CloneURL: origin.FileURL(), Branch: "main", Base: strings.TrimSpace(string(head)), Code: "branch", Env: env, EnvLabel: label,
		Messages: []fakecloud.Message{{Role: "user", Text: title}}})
	if err != nil || !work {
		return s.ID, err
	}
	return s.ID, fakecloud.Work(fakecloud.Proc{Vars: map[string]string{}}, s.ID, false)
}

// dirty leaves work in progress in the demo repository, as a hand-off finds it: a commit
// not pushed, an untracked note and files that look like credentials (POST /dirty).
func dirty(h string) error {
	demo := filepath.Join(h, "git", "demo")
	for p, s := range map[string]string{"parser.go": "package demo\n", "docs/notes.md": "notes\n", ".env": "TOKEN=not-a-real-one\n", "certs/dev.pem": "not a real key\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(demo, p)), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(demo, p), []byte(s), 0o600); err != nil {
			return err
		}
	}
	for _, args := range [][]string{{"add", "parser.go"}, {"commit", "-q", "-m", "work in progress"}} {
		if out, err := exec.Command("git", append([]string{"-C", demo}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v: %s", args, err, out)
		}
	}
	return nil
}

// background runs a command line the window would open in a terminal, as that terminal
// would: in the background, with this program's environment.
func background(line string) error {
	cmd := exec.Command("sh", "-c", line)
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-Command", line)
	}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
