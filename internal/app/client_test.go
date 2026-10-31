package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/clientapi"
)

// seedClaude copies the Claude Code fixture into home, its sessions in repo.
func seedClaude(t *testing.T, home, repo string) {
	t.Helper()
	fix := "../../agents/claude/testdata/2.1.284"
	err := filepath.Walk(fix, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(fix, p)
		if strings.HasPrefix(filepath.ToSlash(rel), "sessions/") {
			return nil // its process ids could alias processes running here
		}
		b, _ := os.ReadFile(p)
		q, _ := json.Marshal(repo)
		b = []byte(strings.ReplaceAll(string(b), "/home/u/git/demo", strings.Trim(string(q), `"`)))
		rel = strings.ReplaceAll(rel, "-home-u-git-demo", claude.Slug(repo))
		dst := filepath.Join(home, ".claude", rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

type clientConn struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan map[string]any
	n     int
}

func (c *clientConn) call(method string, params any) (result map[string]any, events []map[string]any) {
	c.t.Helper()
	c.n++
	req, _ := json.Marshal(map[string]any{"id": c.n, "method": method, "params": params})
	if _, err := c.in.Write(append(req, '\n')); err != nil {
		c.t.Fatal(err)
	}
	for {
		select {
		case m := <-c.lines:
			if m["event"] != nil {
				events = append(events, m)
				continue
			}
			if m["id"] != float64(c.n) {
				c.t.Fatalf("answer for %v, want %d: %v", m["id"], c.n, m)
			}
			if e, ok := m["error"].(map[string]any); ok {
				return map[string]any{"error": e}, events
			}
			r, _ := m["result"].(map[string]any)
			return r, events
		case <-time.After(2 * time.Minute):
			c.t.Fatalf("no answer to %s", method)
		}
	}
}

func TestClientAPIContinueApplyUndo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(home, "state"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	repo := filepath.Join(home, "git", "demo")
	for _, d := range []string{repo, filepath.Join(home, ".codex")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	seedClaude(t, home, repo)
	cfg := config.Defaults()
	cfg.ReposDir = filepath.Join(home, "git")
	a := New(cfg, all.Registry(), filepath.Join(home, "state"), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.ServeClient(ctx, inR, outW); _ = outW.Close() }()
	c := &clientConn{t: t, in: inW, lines: make(chan map[string]any, 64)}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var m map[string]any
			_ = json.Unmarshal(sc.Bytes(), &m)
			c.lines <- m
		}
	}()
	defer inW.Close()

	hello, _ := c.call(clientapi.MethodHello, clientapi.HelloParams{API: 1, Client: clientapi.Client{Name: "test"}})
	if hello["api"] != float64(1) || hello["machine"] != LocalName() {
		t.Fatalf("hello: %v", hello)
	}
	cat, _ := c.call(clientapi.MethodCatalog, clientapi.CatalogParams{Local: true})
	sessions, _ := cat["sessions"].([]any)
	var ref string
	for _, s := range sessions {
		row := s.(map[string]any)
		if row["key"].(map[string]any)["agent"] == "claude" {
			ref = row["ref"].(string)
			if !strings.HasPrefix(ref, LocalName()+":claude") || row["dir"] != repo {
				t.Fatalf("row: %v", row)
			}
			break
		}
	}
	if ref == "" {
		t.Fatalf("the fixture's Claude Code session is not in the catalog: %v", cat)
	}

	plan, _ := c.call(clientapi.MethodPlan, clientapi.PlanParams{Session: ref, Agent: "codex", Dir: repo})
	if plan["error"] != nil {
		t.Fatalf("plan: %v", plan)
	}
	if plan["kind"] != "continue" || plan["id"] == "" {
		t.Fatalf("plan: %v", plan)
	}
	if bl, _ := plan["blockers"].([]any); len(bl) > 0 {
		t.Fatalf("blockers: %v", bl)
	}

	receipt, steps := c.call(clientapi.MethodApply, clientapi.ApplyParams{Plan: plan["id"].(string)})
	if receipt["error"] != nil {
		t.Fatalf("apply: %v", receipt)
	}
	if receipt["journal"] == "" || receipt["key"].(map[string]any)["agent"] != "codex" || !strings.HasPrefix(receipt["ref"].(string), LocalName()+":codex") {
		t.Fatalf("receipt: %v", receipt)
	}
	running, done := 0, 0
	for _, e := range steps {
		if e["event"] != clientapi.EventStep {
			continue
		}
		switch e["data"].(map[string]any)["state"] {
		case "running":
			running++
		case "done":
			done++
		}
	}
	if running == 0 || running != done {
		t.Fatalf("steps: %d running, %d done: %v", running, done, steps)
	}
	again, _ := c.call(clientapi.MethodApply, clientapi.ApplyParams{Plan: plan["id"].(string)})
	if e, _ := again["error"].(map[string]any); e == nil || e["code"] != clientapi.CodeNotFound {
		t.Fatalf("a plan applies once: %v", again)
	}

	undone, _ := c.call(clientapi.MethodUndo, clientapi.UndoParams{Journal: receipt["journal"].(string)})
	if undone["error"] != nil || undone["journal"] != receipt["journal"] {
		t.Fatalf("undo: %v", undone)
	}
	twice, _ := c.call(clientapi.MethodUndo, clientapi.UndoParams{Journal: receipt["journal"].(string)})
	if e, _ := twice["error"].(map[string]any); e == nil || e["code"] != clientapi.CodeNotFound {
		t.Fatalf("undo twice: %v", twice)
	}
}
