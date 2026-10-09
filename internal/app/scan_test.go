package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
)

// A Claude Code session in a folder named by CLAUDE_CONFIG_DIR, with text in Japanese and
// Chinese and this machine's own paths, is listed (on every OS; the Windows peer test
// starts from the same session).
func TestScanListsSessionInConfiguredFolder(t *testing.T) {
	work := t.TempDir()
	proj := filepath.Join(work, "proj")
	claudeDir := filepath.Join(work, "claude")
	t.Setenv("HOME", work)
	t.Setenv("USERPROFILE", work)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(work, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(work, "state"))
	t.Setenv("HOPSESH_MACHINE", "here")
	os.MkdirAll(proj, 0o700)
	const id = "3a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	cwd, _ := json.Marshal(proj)
	file := filepath.Join(claudeDir, "projects", claude.Slug(proj), id+".jsonl")
	os.MkdirAll(filepath.Dir(file), 0o700)
	lines := []string{
		`{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"` + id + `","cwd":` + string(cwd) + `,"version":"2.1.284","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"バグを直して 修复错误"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"` + id + `","cwd":` + string(cwd) + `,"timestamp":"2026-10-01T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"直しました。 已修复。"}]}}`,
		`{"type":"custom-title","customTitle":"テスト Windows 测试","sessionId":"` + id + `"}`,
	}
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, all.Registry(), config.StateDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	inv := a.Scan(context.Background(), ScanOptions{SkipGit: true})
	defer inv.Close()
	for _, m := range inv.Machines {
		for _, st := range m.Agents {
			t.Logf("%s %s: present=%v roots=%v error=%q", m.Name, st.Agent, st.Install.Present, st.Install.Roots, st.Error)
		}
	}
	if len(inv.Entries) != 1 || inv.Entries[0].Session.Title != "テスト Windows 测试" || inv.Entries[0].Session.CWD != proj {
		t.Fatalf("entries: %+v", inv.Entries)
	}
}

// A connection the machine drops before login says why that may be.
func TestClassifyDroppedBeforeLogin(t *testing.T) {
	status, _, hint := classify(errors.New("ssh: exit status 255: kex_exchange_identification: read: Connection reset by peer"), config.Host{Name: "box"})
	if status != StatusError || !strings.Contains(hint, "wait a minute") {
		t.Fatalf("%s: %s", status, hint)
	}
}
