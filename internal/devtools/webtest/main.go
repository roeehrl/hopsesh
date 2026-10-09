// Command webtest serves the desktop app's window in a browser, for tests and screenshots:
// the real assets and the real service (internal/ui/gui) on a demo home made from the
// agents' test fixtures, with a stand-in for the Wails runtime. It is not shipped.
//
// The demo home also has stand-in Claude Code and Codex clouds and a Copilot cloud agent:
// this program, run as claude, codex or gh, is the stand-in claude, codex or gh; the demo
// repository's GitHub remote is a local bare repository; POST /cloud adds a cloud session
// (?cloud=copilot-cloud for a Copilot task, ?cloud=codex-cloud for a Codex cloud task with
// ?env=; ?handed=1 records it as one hopsesh handed off; it sets the failure the next
// driver call plays); POST /dirty leaves work in
// progress in the demo repository, for a hand-off; and a command the window opens "in a
// terminal" runs in the background instead.
//
// The terminal window: /terminal/ is the real page (with its content security policy), and
// its two streams are WebSockets to /stream. POST /reset?terminal=here makes sessions and
// steps open in the app's own terminal (other resets choose the user's terminal app, which
// the older tests expect), and &close=1 closes tabs whose program ended well; POST /terminal-test/open?title=…[&trust=1][&quiet=1][&kind=step][&as=claude] opens a tab running
// termfake (internal/testkit/termfake), as an entry point would; GET /terminal-test/links
// lists the links the window asked hopsesh to open (no browser opens), and "app:<name>"
// for each desktop app it asked to bring forward (none opens). POST
// /live?session=<id>&entrypoint=claude-desktop makes a Claude Code session open in the
// Claude app (entrypoint=cli: in a terminal). POST /seed?n=220 adds n made-up Claude Code
// sessions over a dozen made-up repositories and the last 90 days (for a long list).
//
//	go run ./internal/devtools/webtest -addr 127.0.0.1:8765 -home /tmp/demo
package main

import (
	"context"
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
	"time"

	"github.com/coder/websocket"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/pty"
	"github.com/roeehrl/hopsesh/internal/testkit"
	"github.com/roeehrl/hopsesh/internal/testkit/fakeagent"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/internal/testkit/termfake"
	"github.com/roeehrl/hopsesh/internal/ui/cli"
	"github.com/roeehrl/hopsesh/internal/ui/gui"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// live makes a demo Claude Code session open (in Claude Code's own process registry, with
// this program's pid, which runs): started by entrypoint ("cli" in a terminal,
// "claude-desktop" in the Claude app).
func live(home, session, entrypoint string) error {
	if session == "" {
		return fmt.Errorf("session is required")
	}
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": session, "kind": "interactive", "entrypoint": entrypoint, "status": "idle"})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", os.Getpid())), b, 0o600)
}

// shim stands in for /wails/runtime.js: calls go to /call, tests raise events with
// window.__emit(name, data), and streams are WebSockets to /stream.
const shim = `
export const Stream = (name) => {
  const ws = new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/stream?name=" + encodeURIComponent(name));
  ws.binaryType = "arraybuffer";
  return ws;
};
const listeners = {};
export const Call = { ByName: async (name, ...args) => {
  const r = await fetch("/call", { method: "POST", body: JSON.stringify({ m: name.split(".").pop(), args }) });
  const j = await r.json();
  if (j.error) throw new Error(j.error);
  return j.result;
} };
export const Events = { On: (name, fn) => { (listeners[name] ||= []).push(fn); return () => { listeners[name] = listeners[name].filter((f) => f !== fn); }; } };
window.__emit = (name, data) => (listeners[name] || []).forEach((f) => f({ data }));
// The terminal's events from the service (the tabs' states, quitting, a sign-in's end).
if (!location.pathname.startsWith("/terminal/")) {
  const events = () => {
    const ws = new WebSocket((location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/events");
    ws.onmessage = (ev) => { const m = JSON.parse(ev.data); window.__emit(m.name, m.data); };
    ws.onclose = () => setTimeout(events, 500);
  };
  events();
}
`

func main() {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	switch name {
	case "claude":
		if os.Getenv("FAKE_MOVEMENT_ACCOUNT") == "1" && strings.Join(os.Args[1:], " ") == "auth status --json" {
			fmt.Println(`{"loggedIn":true,"email":"alice@example.com","authMethod":"claude.ai","apiProvider":"firstParty","orgId":"org-fixture","subscriptionType":"max"}`)
			return
		}
		os.Exit(fakeagent.Claude())
	case "codex":
		os.Exit(fakeagent.Codex())
	case "termfake":
		os.Exit(termfake.Main())
	}
	if code, ok := fakeagent.Vendor(name); ok {
		os.Exit(code)
	}
	if len(os.Args) > 1 && (os.Args[1] == "terminal-step" || os.Args[1] == "terminal-open") {
		// The line the window opens "in a terminal" (a hand-off's step, or a ticket for a
		// session or a teleport): the command line's own verb, which this program carries.
		if err := cli.NewRoot(os.Stdout, all.Registry()).Execute(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
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
		_ = os.Unsetenv("FAKE_MOVEMENT_ACCOUNT")
		_ = os.Unsetenv("FAKE_CODEX_EMAIL")
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
	// fresh starts over from a new demo home (POST /reset, so tests are independent);
	// where is where sessions and steps open ("terminal" unless a test asks); closeEnded is
	// Settings → Terminal's "Close a tab when its program ends" (off unless a test asks: the
	// older tests read ended tabs).
	fresh := func(where string, closeEnded bool) error {
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
		svc = gui.NewApp(all.Registry(), gui.WithoutSessionWatching())
		svc.Emitter = relay
		if where == "" {
			where = gui.WhereTerminal
		}
		set := svc.TerminalSettings()
		return svc.SetTerminalSettings(gui.TerminalSettingsInput{Where: where, KeepTabs: true, Notify: true, CloseEnded: closeEnded, FontSize: set.FontSize, Scrollback: set.Scrollback})
	}
	http.HandleFunc("/events", serveEvents)
	gui.SetTerminal(background)
	if self, err := os.Executable(); err == nil {
		gui.SetStepProgram(self)
	}
	if err := fresh("", false); err != nil {
		log.Fatal(err)
	}
	var (
		linksMu sync.Mutex
		links   []string
	)
	gui.SetLinkHook(func(u string) {
		linksMu.Lock()
		links = append(links, u)
		linksMu.Unlock()
	})
	gui.SetAppHook(func(name string, command agent.Command) error {
		linksMu.Lock()
		links = append(links, "app:"+name)
		if len(command.Argv) > 0 {
			links = append(links, "desktop:"+strings.Join(command.Argv[1:], " "))
		}
		linksMu.Unlock()
		return nil
	})
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
		seed := func(title string, work bool) (string, error) { return seedCloud(q.Get("cloud"), title, work) }
		if q.Get("cloud") == fakecloud.CodexCloud {
			seed = func(title string, work bool) (string, error) { return seedCodex(title, q.Get("env"), work) }
		}
		id, err := seed(q.Get("title"), q.Get("work") != "0")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if q.Get("handed") == "1" {
			// As if hopsesh had handed it off: the bring-back checks the copy against the
			// briefing it sent.
			title := q.Get("title")
			if title == "" {
				title = "Add rate limiting"
			}
			if err := move.SaveHandoff(config.StateDir(), &move.Handoff{Journal: "handed-" + id, Cloud: fakecloud.ClaudeCloud, Repo: "github.com/example/demo",
				Session: agent.SessionKey{Agent: "claude", Session: agent.SessionID(id)}, Brief: "[hopsesh] " + title}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		os.Setenv("FAKE_CLOUD_FAIL", r.URL.Query().Get("fail"))
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	})
	http.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if err := live(h, r.URL.Query().Get("session"), r.URL.Query().Get("entrypoint")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	http.HandleFunc("/seed", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		n := 220
		if raw := r.URL.Query().Get("n"); raw != "" {
			if _, err := fmt.Sscan(raw, &n); err != nil {
				http.Error(w, "invalid session count", http.StatusBadRequest)
				return
			}
		}
		if err := seedMany(h, n); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	// This fixture endpoint writes only a scanned Codex rollout inside the demo
	// home, representing real native work after a transfer without editing DTOs.
	http.HandleFunc("/movement-work", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		e, err := svc.ResolveEntry(r.URL.Query().Get("machine"), r.URL.Query().Get("key"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		demoRoot, rootErr := filepath.EvalSymlinks(h)
		path, pathErr := filepath.EvalSymlinks(e.Path)
		rel, err := filepath.Rel(demoRoot, path)
		if rootErr != nil || pathErr != nil || err != nil || e.Agent != "codex" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			http.Error(w, "expected a demo Codex rollout", http.StatusBadRequest)
			return
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()
		for _, message := range []struct{ role, kind, text string }{{"user", "input_text", "Add a fixture return checkpoint"}, {"assistant", "output_text", "Fixture return checkpoint completed."}} {
			record := map[string]any{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "type": "response_item", "payload": map[string]any{"type": "message", "role": message.role, "content": []map[string]string{{"type": message.kind, "text": message.text}}}}
			if err := json.NewEncoder(f).Encode(record); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
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
		linksMu.Lock()
		links = nil
		linksMu.Unlock()
		if err := fresh(r.URL.Query().Get("terminal"), r.URL.Query().Get("close") == "1"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		if r.URL.Query().Get("movement") == "1" {
			_ = os.Setenv("FAKE_MOVEMENT_ACCOUNT", "1")
			_ = os.Setenv("FAKE_CODEX_EMAIL", "alice@example.com")
		}
		if r.URL.Query().Get("terminal") == gui.WhereHere {
			// In the app's own terminal the stand-in Claude Code asks whether it trusts a
			// folder it has not seen (the user answers in the tab).
			os.Setenv("FAKE_CLAUDE_TRUSTED", filepath.Join(h, "cloud", "trusted-folders"))
		}
		if says, ok := r.URL.Query()["says"]; ok {
			// What the stand-in Claude Code's user sends in a teleport ("": nothing yet, so
			// the user types it in the tab).
			os.Setenv("FAKE_CLAUDE_SAYS", says[0])
		}
	})
	http.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		terms := svc.Terms
		mu.RUnlock()
		serveStream(w, r, terms)
	})
	http.HandleFunc("/terminal-test/open", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		mu.RLock()
		terms := svc.Terms
		mu.RUnlock()
		info, err := openFake(terms, r.URL.Query().Get("title"), r.URL.Query().Get("trust") == "1", r.URL.Query().Get("quiet") == "1", r.URL.Query().Get("kind"), r.URL.Query().Get("as"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(info)
	})
	http.HandleFunc("/terminal-test/links", func(w http.ResponseWriter, _ *http.Request) {
		linksMu.Lock()
		defer linksMu.Unlock()
		_ = json.NewEncoder(w).Encode(append([]string{}, links...))
	})
	files := http.FileServer(http.FS(assets))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, gui.TerminalPage) {
			// The terminal page runs under the app's policy here too, its streams being
			// WebSockets to this server (WebKit does not count ws: as 'self').
			w.Header().Set("Content-Security-Policy", strings.Replace(gui.TerminalCSP, "connect-src 'self'", "connect-src 'self' ws://"+r.Host, 1))
		}
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

// cloudWorld puts the stand-in claude, codex, gh and jules first on PATH (the account has the Codex
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
	for _, name := range []string{"claude", "codex", "gh", "jules"} {
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
		"GIT_CONFIG_NOSYSTEM": "1", "FAKE_CLOUD_DIR": filepath.Join(h, "cloud"), "FAKE_CLOUD_FAIL": "", "FAKE_CLAUDE_SAYS": "ok", "FAKE_CLAUDE_TRUSTED": "", "FAKE_CODEX_ENVS": "env_api=acme-api,env_web=acme-web",
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

// seedCloud adds a session of a cloud (Claude Code cloud by default, or copilot-cloud) on
// the demo repository (worked: it pushed its work to a claude/… or copilot/… branch) and
// returns its id.
func seedCloud(cloud, title string, work bool) (string, error) {
	if title == "" {
		title = "Add rate limiting"
	}
	if cloud == "" {
		cloud = fakecloud.ClaudeCloud
	}
	s, err := fakecloud.Open(os.Getenv("FAKE_CLOUD_DIR")).Seed(fakecloud.Session{Cloud: cloud, Title: title, Repo: "github.com/example/demo",
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

// seedMany writes n made-up Claude Code sessions over made-up repositories (each a local
// checkout with a GitHub remote), spread over the last 90 days, newest first.
func seedMany(h string, n int) error {
	repos := []string{"api", "webapp", "infra", "mobile", "docs", "billing", "search", "notify", "auth", "ledger", "gateway", "reports"}
	tasks := []string{"Retry the upload when a part stalls", "Bound the merge phase", "Add a peer-visibility journal", "Tidy the memory guard",
		"Group the session list", "Container query for compact rows", "Fix title bar spacing", "Speed up the session scan", "Sign the Windows installer",
		"Bench the layer-1 forward pass", "Probe the feed rate limits", "Tests for the session file reader", "Clean up old worktrees", "Import the tool table",
		"Profile the idle CPU burn", "Release notes for the next version", "Cache the scan by path, size and mtime", "Explain the epoch timer",
		"Port the settings page to the new layout", "Rate-limit the public endpoints", "Migrate the queue to the new broker", "Find the flaky login test"}
	branches := []string{"main", "fix/retry", "feat/journal", "list-groups", "compact-rows", "titlebar", "scan-cache", "win-sign", "perf/l1"}
	seed := uint32(7)
	rnd := func(k int) int { seed = seed*1664525 + 1013904223; return int(seed>>8) % k }
	now := time.Now().UTC()
	for _, name := range repos {
		dir := filepath.Join(h, "git", name)
		if _, err := os.Stat(dir); err == nil {
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://github.com/example/" + name + ".git"},
			{"-c", "user.name=demo", "-c", "user.email=demo@example.com", "commit", "-q", "--allow-empty", "-m", "init"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("git %v: %v %s", args, err, out)
			}
		}
	}
	for i := 0; i < n; i++ {
		name := repos[rnd(len(repos))]
		cwd := filepath.Join(h, "git", name)
		// The newest few minutes ago, the oldest about 90 days ago.
		at := now.Add(-time.Duration(float64(i*i)/float64(n*n)*90*24) * time.Hour).Add(-time.Duration(rnd(50)+3) * time.Minute)
		id := fmt.Sprintf("5eed%04x-0000-4000-8000-%012x", i, i)
		branch := branches[rnd(len(branches))]
		task := tasks[rnd(len(tasks))]
		ts := func(d time.Duration) string { return at.Add(d).Format("2006-01-02T15:04:05.000Z") }
		rec := func(m map[string]any) string { b, _ := json.Marshal(m); return string(b) }
		base := func(typ, uuid, parent string, d time.Duration) map[string]any {
			m := map[string]any{"type": typ, "uuid": uuid, "sessionId": id, "cwd": cwd, "version": "2.1.284", "gitBranch": branch, "timestamp": ts(d), "isSidechain": false, "userType": "external"}
			if parent == "" {
				m["parentUuid"] = nil
			} else {
				m["parentUuid"] = parent
			}
			return m
		}
		u1 := base("user", "u1", "", -4*time.Minute)
		u1["message"] = map[string]any{"role": "user", "content": strings.ToLower(task[:1]) + task[1:] + ", and keep the tests green"}
		a1 := base("assistant", "a1", "u1", -3*time.Minute)
		a1["message"] = map[string]any{"role": "assistant", "content": []map[string]any{{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "go test ./..."}}}}
		r1 := base("user", "u2", "a1", -2*time.Minute)
		r1["message"] = map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": "t1", "content": "ok"}}}
		r1["toolUseResult"] = map[string]any{"stdout": "ok"}
		a2 := base("assistant", "a2", "u2", -time.Minute)
		a2["message"] = map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": "Done: " + strings.ToLower(task[:1]) + task[1:] + ". The tests pass, and nothing is pushed yet."}}}
		lines := []string{rec(u1), rec(a1), rec(r1), rec(a2)}
		if i%5 != 3 {
			lines = append(lines, rec(map[string]any{"type": "custom-title", "customTitle": task, "sessionId": id}))
		}
		lines = append(lines, rec(map[string]any{"type": "last-prompt", "lastPrompt": u1["message"].(map[string]any)["content"], "sessionId": id, "leafUuid": "a2"}))
		dir := filepath.Join(h, ".claude", "projects", claude.Slug(cwd))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		p := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			return err
		}
		if err := os.Chtimes(p, at, at); err != nil {
			return err
		}
	}
	return nil
}

// background runs a command line the window would open in a terminal, as that terminal
// would: in the background, with this program's environment. With FAKE_CLOUD_FAIL=hold,
// a hand-off's terminal step is left alone, as by a user who has not answered yet.
func background(line string) error {
	if os.Getenv("FAKE_CLOUD_FAIL") == "hold" && strings.Contains(line, "terminal-step") {
		return nil
	}
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

// serveStream is one of the terminal window's streams, as a WebSocket: the app's own
// handlers serve it (the browser tests have only the terminal page, so the window check
// the app makes has nothing to check).
func serveStream(w http.ResponseWriter, r *http.Request, terms *gui.Terminals) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(4 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	conn := &wsConn{c: c, ctx: ctx, cancel: cancel}
	switch r.URL.Query().Get("name") {
	case gui.TerminalStream:
		terms.ServeTab(conn)
	case gui.TerminalTabsStream:
		terms.ServeList(conn)
	default:
		_ = c.Close(websocket.StatusPolicyViolation, "no such stream")
	}
}

// wsConn is a WebSocket as the terminal's stream connection (pty.Conn).
type wsConn struct {
	c      *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	wmu    sync.Mutex
}

func (w *wsConn) Send(b []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	return w.c.Write(w.ctx, websocket.MessageBinary, b)
}

func (w *wsConn) Receive() ([]byte, error) {
	_, b, err := w.c.Read(w.ctx)
	if err != nil {
		w.cancel()
	}
	return b, err
}

func (w *wsConn) Context() context.Context { return w.ctx }

func (w *wsConn) Close() error {
	w.cancel()
	return w.c.Close(websocket.StatusNormalClosure, "")
}

// openFake opens a tab running termfake (this program), as one of the app's entry points
// would: kind session (default), step (with the hand-off's banner), signin (recorded
// nowhere) or shell.
func openFake(terms *gui.Terminals, title string, trust, quiet bool, kind, as string) (any, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// One folder per webtest process: a link or copy left by an earlier build would run that
	// build's stand-in (and a running copy can't be replaced on Windows).
	dir := filepath.Join(os.TempDir(), "hopsesh-termfake", fmt.Sprint(os.Getpid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	prog := filepath.Join(dir, "termfake"+map[bool]string{true: ".exe", false: ""}[runtime.GOOS == "windows"])
	if _, err := os.Stat(prog); err != nil {
		if runtime.GOOS == "windows" {
			b, err := os.ReadFile(self)
			if err == nil {
				err = os.WriteFile(prog, b, 0o700)
			}
			if err != nil {
				return nil, err
			}
		} else if err := os.Symlink(self, prog); err != nil {
			return nil, err
		}
	}
	argv := []string{prog}
	if trust {
		argv = append(argv, "trust")
	}
	if quiet {
		argv = append(argv, "quiet")
	}
	if title == "" {
		title = "Fix the parser · termfake"
	}
	if kind == "" {
		kind = gui.TabSession
	}
	home, _ := os.UserHomeDir()
	spec := pty.Spec{Argv: argv, Dir: home, Title: title, Private: kind == gui.TabSignIn || kind == gui.TabShell}
	meta := gui.TabMeta{Kind: kind, Command: strings.Join(append([]string{"termfake"}, argv[1:]...), " "), Agent: "Termfake", Rerun: kind != gui.TabStep, External: false}
	if as == "claude" && kind == gui.TabStep {
		// For screenshots: the tab presents itself as a real hand-off step does, the stand-in
		// running in the demo repository's hand-off folder.
		state := os.Getenv("HOPSESH_STATE_DIR")
		if state == "" {
			state = filepath.Join(home, ".local", "state", "hopsesh")
		}
		spec.Dir = filepath.Join(state, "handoff", "github.com", "example", "demo")
		if err := os.MkdirAll(spec.Dir, 0o700); err != nil {
			return nil, err
		}
		meta.Command, meta.Agent = `claude --cloud "`+strings.SplitN(title, " · ", 2)[0]+`"`, "Claude Code"
	}
	if kind == gui.TabStep {
		spec.Capture = pty.CaptureStep
		meta.CloudTitle = "Claude Code cloud"
	}
	return terms.Open(spec, gui.TabSetup{Meta: meta})
}

// The events the window gets from the service in the browser tests: the terminal's (the
// other events keep the older tests as they were written).
var (
	eventsMu sync.Mutex
	eventWS  = map[*websocket.Conn]bool{}
)

func relay(name string, data any) {
	switch name {
	case gui.AccountEvent, gui.DiscoveryEvent, "hopsesh:terminal-main", gui.TerminalPreferencesEvent, gui.TerminalWorkspaceEvent, gui.AppearanceEvent, gui.TerminalEvent, gui.QuitEvent, gui.SignedInEvent, gui.TerminalAppEvent, gui.ExternalExitEvent:
	default:
		return
	}
	b, err := json.Marshal(map[string]any{"name": name, "data": data})
	if err != nil {
		return
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	for c := range eventWS {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if c.Write(ctx, websocket.MessageText, b) != nil {
			delete(eventWS, c)
		}
		cancel()
	}
}

func serveEvents(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	eventsMu.Lock()
	eventWS[c] = true
	eventsMu.Unlock()
	_, _, _ = c.Read(r.Context()) // until the page goes
	eventsMu.Lock()
	delete(eventWS, c)
	eventsMu.Unlock()
	_ = c.CloseNow()
}
