package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A return that added to an open, blocked original leaves that window stale: its hook
// refuses prompts with how to reopen, a compaction there does not count, and a real
// resume or start clears it and says the new work is included.
func TestReopenNoticeUntilTheOriginalIsReloaded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	a := New(config.Defaults(), all.Registry(), t.TempDir(), nil)
	t.Cleanup(func() { _ = a.Catalog.Close() })
	ctx := context.Background()
	scope, err := a.noticeScope(ctx, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	key := agent.SessionKey{Agent: "claude", Profile: scope.ID, Session: "original"}
	if _, _, handled := a.ReopenNotice(ctx, "claude", "", "original", "UserPromptSubmit", ""); handled {
		t.Fatal("nothing to reopen, yet handled")
	}
	b, _ := json.Marshal(reopenRecord{Key: key, Operation: "back", From: "Codex", At: time.Now()})
	path := a.reopenPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	text, block, handled := a.ReopenNotice(ctx, "claude", "", "original", "UserPromptSubmit", "")
	if !handled || !block || !strings.Contains(text, "reopen it in Claude Code") || !strings.Contains(text, "Codex") {
		t.Fatalf("stale window not refused: %q %v %v", text, block, handled)
	}
	if _, block, handled = a.ReopenNotice(ctx, "claude", "", "original", "SessionStart", "compact"); !handled || block {
		t.Fatal("compaction in the old window counted as a reopen")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("compaction cleared the reopen record")
	}
	text, block, handled = a.ReopenNotice(ctx, "claude", "", "original", "SessionStart", "resume")
	if !handled || block || !strings.Contains(text, "now includes the work added from Codex") {
		t.Fatalf("resume did not confirm: %q %v %v", text, block, handled)
	}
	if _, _, handled = a.ReopenNotice(ctx, "claude", "", "original", "UserPromptSubmit", ""); handled {
		t.Fatal("still refused after the reopen")
	}
}
