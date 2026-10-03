package integrate

import (
	"os"
	"testing"
)

func TestAdoptLoginEnv(t *testing.T) {
	SetLoginVars([]string{"HOPSESH_TEST_AGENT_HOME"})
	t.Setenv("HOPSESH_TEST_AGENT_HOME", "")
	login := func() map[string]string { return map[string]string{"HOPSESH_TEST_AGENT_HOME": "/tmp/agent-alt"} }
	adoptLoginEnv(func(string) string { return "" }, login)
	if got := os.Getenv("HOPSESH_TEST_AGENT_HOME"); got != "/tmp/agent-alt" {
		t.Fatalf("variable = %q", got)
	}
	// A value already in the process wins.
	adoptLoginEnv(func(string) string { return "/mine" }, func() map[string]string { return map[string]string{"HOPSESH_TEST_AGENT_HOME": "/other"} })
	if got := os.Getenv("HOPSESH_TEST_AGENT_HOME"); got != "/tmp/agent-alt" {
		t.Fatalf("overwrote an existing value: %q", got)
	}
}
