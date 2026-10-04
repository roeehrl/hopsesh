// Package fakeagent is the stand-in Claude Code and Codex the tests (and the Windows
// screenshots in CI) run in place of the real agents.
package fakeagent

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
)

// The stand-in agents: the programs hopsesh runs, answering the way the real ones do for
// what hopsesh asks (versions, Codex's app-server, and both agents' cloud verbs, which
// internal/testkit/fakecloud plays). Every call is logged to $FAKE_AGENT_LOG, one line
// each: the program, its arguments, and app-server methods.

func logCall(line string) {
	if p := os.Getenv("FAKE_AGENT_LOG"); p != "" {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintln(f, line)
			f.Close()
		}
	}
}

// Claude answers --version and the cloud flags (--cloud, --teleport); anything else is
// logged and succeeds.
func Claude() int {
	logCall("claude " + strings.Join(os.Args[1:], " "))
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("2.1.284 (Claude Code)")
	}
	if handled, code := fakecloud.Claude(proc()); handled {
		return code
	}
	return 0
}

// Codex answers --version, runs app-server and answers codex cloud and codex apply.
func Codex() int {
	logCall("codex " + strings.Join(os.Args[1:], " "))
	switch {
	case len(os.Args) > 1 && os.Args[1] == "--version":
		fmt.Println("codex-cli 0.153.2")
	case len(os.Args) > 1 && os.Args[1] == "app-server":
		return appServer()
	}
	if handled, code := fakecloud.Codex(proc()); handled {
		return code
	}
	return 0
}

// Vendor is the stand-in for a second-wave cloud's CLI (gh, jules, devin, amp), by the
// name it runs under; ok is false for another name.
func Vendor(name string) (code int, ok bool) {
	main := fakecloud.Vendor(name)
	if main == nil {
		return 0, false
	}
	return main(proc()), true
}

// Cloud is the fakecloud program: it plays the cloud agent (fakecloud work) and the fake's
// own cloud CLI (fakecloud remote).
func Cloud() int { return fakecloud.Main(proc()) }

// proc is this run, for the stand-in clouds.
func proc() fakecloud.Proc {
	dir, _ := os.Getwd()
	return fakecloud.Proc{Args: os.Args[1:], Dir: dir, Stdout: os.Stdout, Stderr: os.Stderr}
}

// appServer answers JSON-RPC lines on stdin until it ends. $FAKE_CODEX_EMAIL is the
// ChatGPT account it is signed in to ("": not signed in).
func appServer() int {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".codex")
	}
	out := json.NewEncoder(os.Stdout)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		logCall("codex app-server " + req.Method)
		if req.ID == nil {
			continue // a notification
		}
		answer := func(result any) { _ = out.Encode(map[string]any{"id": *req.ID, "result": result}) }
		switch req.Method {
		case "initialize":
			answer(map[string]any{"userAgent": "codex_cli_rs/0.153.2"})
		case "thread/read":
			var p struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(req.Params, &p)
			answer(map[string]any{"thread": map[string]any{"id": p.ThreadID}})
		case "thread/name/set":
			var p struct {
				ThreadID string `json:"threadId"`
				Name     string `json:"name"`
			}
			_ = json.Unmarshal(req.Params, &p)
			appendIndex(home, p.ThreadID, p.Name)
			answer(map[string]any{})
		case "account/read":
			if e := os.Getenv("FAKE_CODEX_EMAIL"); e != "" {
				answer(map[string]any{"account": map[string]any{"type": "chatgpt", "email": e, "planType": "plus"}})
			} else {
				answer(map[string]any{"account": nil, "requiresOpenaiAuth": true})
			}
		case "externalAgentConfig/import":
			answer(map[string]any{})
			id, err := importSession(home, req.Params)
			done := map[string]any{"itemTypeResults": []any{map[string]any{"successes": []any{map[string]any{"target": id}}}}}
			if err != nil {
				done = map[string]any{"itemTypeResults": []any{map[string]any{"failures": []any{map[string]any{"message": err.Error()}}}}}
			}
			_ = out.Encode(map[string]any{"method": "externalAgentConfig/import/completed", "params": done})
		default:
			_ = out.Encode(map[string]any{"id": *req.ID, "error": map[string]any{"message": "unknown method " + req.Method}})
		}
	}
	return 0
}

// appendIndex names a thread the way Codex does: a line in session_index.jsonl.
func appendIndex(home, id, name string) {
	f, err := os.OpenFile(filepath.Join(home, "session_index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]any{"id": id, "thread_name": name, "updated_at": time.Now().UTC().Format(time.RFC3339)})
	fmt.Fprintln(f, string(b))
}

// importSession writes a thread from a Claude Code session the importer was given: its
// first user message, so the thread is real enough to list and resume.
func importSession(home string, params json.RawMessage) (string, error) {
	var p struct {
		Items []struct {
			Details struct {
				Sessions []struct {
					Cwd   string `json:"cwd"`
					Path  string `json:"path"`
					Title string `json:"title"`
				} `json:"sessions"`
			} `json:"details"`
		} `json:"migrationItems"`
	}
	if err := json.Unmarshal(params, &p); err != nil || len(p.Items) == 0 || len(p.Items[0].Details.Sessions) == 0 {
		return "", fmt.Errorf("no session to import")
	}
	s := p.Items[0].Details.Sessions[0]
	prompt := "(imported)"
	if b, err := os.ReadFile(s.Path); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			var r struct {
				Type    string `json:"type"`
				Message struct {
					Content any `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(l), &r) == nil && r.Type == "user" {
				if t, ok := r.Message.Content.(string); ok {
					prompt = t
					break
				}
			}
		}
	}
	var u [16]byte
	_, _ = rand.Read(u[:])
	id := fmt.Sprintf("%x-%x-7%x-8%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
	now := time.Now().UTC()
	dir := filepath.Join(home, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	ts := now.Format("2006-01-02T15:04:05.000Z")
	lines := []map[string]any{
		{"timestamp": ts, "type": "session_meta", "payload": map[string]any{"id": id, "timestamp": ts, "cwd": s.Cwd, "originator": "codex_cli_rs", "cli_version": "0.153.2", "source": "cli", "model_provider": "openai"}},
		{"timestamp": ts, "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": prompt, "images": []string{}}},
		{"timestamp": ts, "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": prompt}}}},
	}
	var b strings.Builder
	for _, l := range lines {
		j, _ := json.Marshal(l)
		b.Write(j)
		b.WriteByte('\n')
	}
	name := fmt.Sprintf("rollout-%s-%s.jsonl", now.Format("2006-01-02T15-04-05"), id)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	if s.Title != "" {
		appendIndex(home, id, s.Title)
	}
	return id, nil
}
