package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
)

func TestCloudStartupGUIReviewsFilesWithoutExposingSettingsAndRejectsStalePlan(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	defer a.Shutdown()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(repo, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".claude", "settings.json")
	if err = os.WriteFile(path, []byte(`{"env":{"FIXTURE":"private-review-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := a.CloudStartupPreview("claude-hosted", "0.5.0", cloudintegration.DownloadOrigin, repo)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(preview)
	if strings.Contains(string(public), "private-review-secret") {
		t.Fatal("GUI preview disclosed existing settings")
	}
	if _, err = os.Stat(filepath.Join(repo, ".hopsesh")); !os.IsNotExist(err) {
		t.Fatal("GUI preview installed files")
	}
	if err = a.CloudStartupApply("wrong-review"); err == nil {
		t.Fatal("different review installed startup files")
	}
	if err = os.WriteFile(path, []byte(`{"env":{"FIXTURE":"concurrent-review-edit"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.CloudStartupApply(preview.ID); err == nil {
		t.Fatal("stale GUI review overwrote settings")
	}
	preview, err = a.CloudStartupPreview("claude-hosted", "0.5.0", cloudintegration.DownloadOrigin, repo)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.CloudStartupApply(preview.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.CloudStartupApply(preview.ID); err == nil {
		t.Fatal("consumed review was reused")
	}
	settings, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(settings), "concurrent-review-edit") {
		t.Fatal("installation lost current settings", err)
	}
	if a.RelaySettings().Initialized {
		t.Fatal("setup enrolled a native relay identity")
	}
}
