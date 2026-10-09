package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
)

func TestCloudPrepareRetainsActualHookReasonAndManualOrigin(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("CLAUDE_CODE_REMOTE", "true")
	for _, source := range []string{"startup", "resume", "clear", "compact", "fork", "manual"} {
		cmd := cloudPrepareCmd()
		if source == "manual" {
			cmd.SetArgs([]string{"--provider", "codex-current", "--session", "native-test", "--workspace", dir})
		} else {
			body, _ := json.Marshal(cloudintegration.SessionStart{Event: "SessionStart", Source: source, Session: "native-test", Workspace: dir})
			cmd.SetIn(bytes.NewReader(body))
			cmd.SetArgs([]string{"--claude-hook"})
		}
		var output bytes.Buffer
		cmd.SetOut(&output)
		if err := cmd.ExecuteContext(t.Context()); err != nil {
			t.Fatal(source, err)
		}
		var incarnation cloudintegration.Incarnation
		if err := json.Unmarshal(output.Bytes(), &incarnation); err != nil {
			t.Fatal(err)
		}
		saved, err := cloudintegration.Load(t.Context(), incarnation.Directory)
		if err != nil || saved.Source != source || saved.ExportTranscript {
			t.Fatal("prepare discarded startup reason or granted export", source, saved.Source, err)
		}
	}
}
