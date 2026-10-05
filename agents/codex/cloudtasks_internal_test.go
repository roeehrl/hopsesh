package codex

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// The listing's shape comes from source, not from a real task, so odd shapes are read where
// they can be and spoil only their own task.
func TestParseList(t *testing.T) {
	out := `warning: something first
{
  "tasks": [
    {
      "id": "task_e_aaaa1111",
      "url": "https://chatgpt.com/codex/tasks/task_e_aaaa1111",
      "title": "Fix the parser",
      "status": "ready",
      "updated_at": "2026-10-04T09:58:01.123456Z",
      "environment_id": "env_1",
      "environment_label": "acme-api",
      "summary": {"files_changed": 2, "lines_added": 12, "lines_removed": 3},
      "is_review": false,
      "attempt_total": 2
    },
    {"id": "task_e_bbbb2222", "title": "Older", "status": "Pending", "updated_at": 1791100000.5, "environment_id": null,
     "environment_label": null, "summary": "+1/-0 • 1 file", "is_review": false, "attempt_total": null},
    {"id": "task_e_cccc3333", "title": "Review", "status": {"state": "error"}, "is_review": true, "attempt_total": "3"},
    {"id": 42, "status": []},
    "not an object"
  ],
  "cursor": "abc=="
}
`
	p, err := parseList([]byte(out))
	if err != nil || len(p.Tasks) != 5 || p.Cursor != "abc==" {
		t.Fatalf("%+v %v", p, err)
	}
	a := p.Tasks[0]
	if a.ID != "task_e_aaaa1111" || a.Status != "ready" || a.EnvID != "env_1" || a.EnvLabel != "acme-api" || a.Changes != "+12 −3 · 2 files" ||
		a.Attempts != 2 || a.Updated != time.Date(2026, 10, 4, 9, 58, 1, 123456000, time.UTC) {
		t.Errorf("first: %+v", a)
	}
	b := p.Tasks[1]
	if b.Status != "pending" || b.EnvID != "" || b.Changes != "+1/-0 • 1 file" || b.Updated.Unix() != 1791100000 || b.Attempts != 0 {
		t.Errorf("second: %+v", b)
	}
	if c := p.Tasks[2]; !c.IsReview || c.Status != "error" || c.Attempts != 3 {
		t.Errorf("third: %+v", c)
	}
	if p.Tasks[3].unreadable == nil || p.Tasks[4].unreadable == nil {
		t.Error("unreadable tasks are marked")
	}
	if p, err := parseList([]byte(`{"tasks": [], "cursor": null}`)); err != nil || len(p.Tasks) != 0 || p.Cursor != "" {
		t.Errorf("empty (as seen on a real account): %+v %v", p, err)
	}
	if _, err := parseList([]byte("No tasks found.\n")); err == nil {
		t.Error("text is not JSON")
	}
	for status, want := range map[string]agent.CloudState{"pending": agent.CloudRunning, "ready": agent.CloudDone, "applied": agent.CloudDone,
		"error": agent.CloudFailed, "something new": agent.CloudUnknown} {
		if got := taskState(status); got != want {
			t.Errorf("%s: %s", status, got)
		}
	}
}

func TestParseStatus(t *testing.T) {
	st, ok := parseStatus("\x1b[32m[READY]\x1b[0m Fix the parser\n\x1b[2macme-api\x1b[0m  •  3m ago\n+12/-3 • 2 files\n")
	if !ok || st.Status != "ready" || st.Title != "Fix the parser" || st.Env != "acme-api" || st.Changes != "+12 −3 · 2 files" {
		t.Errorf("ready: %+v", st)
	}
	st, ok = parseStatus("[PENDING] Still going\n12s ago\nno diff\n")
	if !ok || st.Status != "pending" || st.Env != "" || st.Changes != "" {
		t.Errorf("pending: %+v", st)
	}
	if _, ok := parseStatus("Error: http error: get_task_details failed: 404 Not Found\n"); ok {
		t.Error("an error is not a status")
	}
}

func TestRefused(t *testing.T) {
	for msg, want := range map[string]error{
		"Not signed in. Please run 'codex login' to sign in with ChatGPT, then re-run 'codex cloud'.":                           agent.ErrSignedOut,
		"Error: environment 'acme' not found; run `codex cloud` to list available environments":                                 agent.ErrNoEnvironment,
		"Error: no cloud environments are available for this workspace":                                                         agent.ErrNoEnvironment,
		"Error: environment label 'acme' is ambiguous; run `codex cloud` to pick the desired environment id":                    agent.ErrNoEnvironment,
		"Error: http error: list_tasks failed: 403 Forbidden":                                                                   agent.ErrNotEligible,
		"Error: http error: create_task failed: 400 Bad Request: the environment's repository is not connected to github.com/x": agent.ErrRepoUnsupported,
	} {
		if err := refused(msg); !errors.Is(err, want) {
			t.Errorf("%s: %v", msg, err)
		}
	}
	if err := refused("Error: no cloud environments are available for this workspace"); err == nil || !strings.Contains(err.Error(), NewCloudEnvs) {
		t.Errorf("no environments: %v", err)
	}
	if err := refused("Error: No diff available for task task_e_6840130401ff; it may still be running."); errors.Is(err, agent.ErrSignedOut) || errors.Is(err, agent.ErrNotEligible) {
		t.Errorf("digits in an id are not an HTTP status: %v", err)
	}
}

func TestLoginKind(t *testing.T) {
	if k := loginKind("logged in using an api key - sk-proj-***abcd"); k != "an api key" || strings.Contains(k, "sk-") {
		t.Error(k)
	}
	if k := loginKind("logged in using access token"); k != "access token" {
		t.Error(k)
	}
}

func TestCreatedTask(t *testing.T) {
	for out, want := range map[string]string{
		"https://chatgpt.com/codex/tasks/task_e_68f2c41a9b7c81909d3e\n": "task_e_68f2c41a9b7c81909d3e",
		"Created task_e_abcdef12\n":                                     "task_e_abcdef12",
		"Something else\n":                                              "",
		// The link codex printed comes last; a link on any other host is not the task's.
		"https://chatgpt.com/codex/tasks/task_e_first1\nhttps://chatgpt.com/codex/tasks/task_e_second2": "task_e_second2",
		"Reading query from stdin...\nhttps://example.com/codex/tasks/task_e_abcdef12":                  "",
		"https://chatgpt.com.evil.example/codex/tasks/task_e_evil1234":                                  "",
		"https://evil.example/https://chatgpt.com/codex/tasks/task_e_evil1234":                          "",
		"https://evil.example/?next=https://chatgpt.com/codex/tasks/task_e_evil1234":                    "",
		"https://user@chatgpt.com/codex/tasks/task_e_evil1234":                                          "",
		"https://chatgpt.com:8443/codex/tasks/task_e_evil1234":                                          "",
		"http://chatgpt.com/codex/tasks/task_e_evil1234":                                                "",
		"https://xn--chtgpt-9ya.com/codex/tasks/task_e_evil1234":                                        "",
		"https://chatgpt.com/other/tasks/task_e_evil1234":                                               "",
		// A hostile link beside the real one never wins, wherever it is.
		"https://chatgpt.com/codex/tasks/task_e_real1234 https://evil.example/codex/tasks/task_e_evil1234": "task_e_real1234",
	} {
		id, url := createdTask(out)
		if id != want {
			t.Errorf("%q: %q", out, id)
		}
		if id != "" && strings.Contains(out, "://") && url != "https://chatgpt.com/codex/tasks/"+id {
			t.Errorf("%q: the link is %q", out, url)
		}
	}
}
