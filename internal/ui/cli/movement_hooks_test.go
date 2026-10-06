package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

type movementNoticeLookup func(context.Context, agent.ID, string, string, string, string) (string, error)

func runNoticeHook(ctx context.Context, in io.Reader, out io.Writer, id agent.ID, profile string, lookup movementNoticeLookup) error {
	return runPreparedNoticeHook(ctx, in, out, id, profile, func(ctx context.Context, id agent.ID, profile, session, path, event string) (preparedMovementNotice, error) {
		text, err := lookup(ctx, id, profile, session, path, event)
		return preparedMovementNotice{Text: text}, err
	})
}

func TestNoticeHookOnlyReturnsHookJSONAndPassesExactIdentity(t *testing.T) {
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		var out bytes.Buffer
		input := `{"session_id":"session-123","transcript_path":"/untrusted/path.jsonl","hook_event_name":"` + event + `","prompt":"must not echo this"}`
		lookup := func(ctx context.Context, id agent.ID, profile, session, path, gotEvent string) (string, error) {
			if id != "claude" || profile != "exact-profile" || session != "session-123" || path != "/untrusted/path.jsonl" || gotEvent != event {
				t.Fatal("identity not passed exactly")
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("no deadline")
			}
			return "Hopsesh status: moved.\x1b[31m\n" + strings.Repeat("界", 3000), nil
		}
		if err := runNoticeHook(context.Background(), strings.NewReader(input), &out, "claude", "exact-profile", lookup); err != nil {
			t.Fatal(err)
		}
		if !utf8.Valid(out.Bytes()) || out.Len() > 2*noticeHookTextLimit+512 {
			t.Fatalf("unbounded or invalid output %d", out.Len())
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc) != 2 || doc["systemMessage"] == nil || doc["hookSpecificOutput"] == nil {
			t.Fatalf("unexpected fields %s", out.String())
		}
		if bytes.Contains(out.Bytes(), []byte(`\u001b`)) || bytes.Contains(out.Bytes(), []byte("must not echo")) || bytes.Contains(out.Bytes(), []byte("transcript_path")) {
			t.Fatalf("unsafe echo %s", out.String())
		}
		var detail struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		}
		_ = json.Unmarshal(doc["hookSpecificOutput"], &detail)
		if detail.Event != event || len(detail.Context) > noticeHookTextLimit {
			t.Fatalf("%+v", detail)
		}
	}
}

func TestNoticeHookFailsOpenOnInvalidInput(t *testing.T) {
	for _, input := range []string{"", `null`, `{`, `{}`, `{"session_id":"s","hook_event_name":"Stop"}`, `{"session_id":"s","hook_event_name":"SessionStart"} {}`, `{"session_id":42,"hook_event_name":"SessionStart"}`, strings.Repeat(" ", noticeHookInputLimit+1)} {
		var out bytes.Buffer
		called := false
		err := runNoticeHook(context.Background(), strings.NewReader(input), &out, "codex", "", func(context.Context, agent.ID, string, string, string, string) (string, error) {
			called = true
			return "bad", nil
		})
		if err != nil || called || out.Len() != 0 {
			t.Fatalf("invalid input invoked lookup or output: %q %v", input[:min(len(input), 100)], err)
		}
	}
	for _, failure := range []error{nil, errors.New("private filesystem error")} {
		var out bytes.Buffer
		_ = runNoticeHook(context.Background(), strings.NewReader(`{"session_id":"s","hook_event_name":"SessionStart"}`), &out, "codex", "", func(context.Context, agent.ID, string, string, string, string) (string, error) { return "", failure })
		if out.Len() != 0 {
			t.Fatal("empty/error result emitted output")
		}
	}
}

func TestNoticeHookDeadlineCoversStalledStdinAndLookup(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	var out bytes.Buffer
	_ = runNoticeHook(ctx, r, &out, "claude", "", func(context.Context, agent.ID, string, string, string, string) (string, error) {
		t.Error("unexpected lookup")
		return "", nil
	})
	if time.Since(start) > time.Second || out.Len() != 0 {
		t.Fatal("stdin was not bounded")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	release := make(chan struct{})
	defer close(release)
	_ = runNoticeHook(ctx2, strings.NewReader(`{"session_id":"s","hook_event_name":"SessionStart"}`), &out, "claude", "", func(context.Context, agent.ID, string, string, string, string) (string, error) {
		<-release
		return "late notice", nil
	})
	if out.Len() != 0 {
		t.Fatal("wrote after timeout")
	}
}

func TestMovementCommandsRegistered(t *testing.T) {
	root := NewRoot(io.Discard, all.Registry())
	for _, args := range [][]string{{"notice-hook"}, {"notices", "status"}, {"notices", "install"}, {"notices", "remove"}} {
		cmd, _, err := root.Find(args)
		if err != nil || cmd.Name() != args[len(args)-1] {
			t.Fatalf("%v: %v %v", args, cmd, err)
		}
	}
}

func TestNoticeHookCommandEndToEndNeverChangesTranscript(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "claude")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	t.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
	t.Setenv("HOPSESH_CONFIG_DIR", filepath.Join(dir, "config"))
	t.Setenv("HOPSESH_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("HOPSESH_MACHINE", "alice-desktop")
	// A source sidecar is sufficient: no full scan, vendor CLI, or model call.
	path := filepath.Join(root, "projects", "test", "alice-session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("native transcript must remain byte-identical\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	g := lineage.New("notice-command")
	from := g.Upsert(lineage.Replica{Location: "alice-desktop", Key: agent.SessionKey{Agent: "claude", Session: "alice-session"}})
	to := g.Upsert(lineage.Replica{Location: "bob-laptop", Key: agent.SessionKey{Agent: "codex", Session: "bob-session"}})
	source := ir.Segment{Nodes: []ir.Node{{Kind: ir.KindMessage, Actor: ir.User, Text: "Fix tests"}}, Cursor: ir.Cursor{Head: "one", Offset: 20}}
	st, err := g.Observe(from, &source)
	if err != nil {
		t.Fatal(err)
	}
	dst := g.Deliver(to, source.Cursor, st.Projection, st.Heads, nil)
	if err = g.AppendHop(lineage.Hop{Kind: lineage.HopContinue, ID: "move-one", From: from, To: to, Source: st.ID, Target: dst.ID, Notify: true}); err != nil {
		t.Fatal(err)
	}
	side := g.Encode()
	if err = os.WriteFile(lineage.PathFor(path), side, 0600); err != nil {
		t.Fatal(err)
	}
	configDir, stateDir := config.Dir(), config.StateDir()
	wrongConfig := filepath.Join(dir, "wrong config")
	wrongState := filepath.Join(dir, "wrong state")
	if err := os.MkdirAll(wrongConfig, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrongConfig, "config.toml"), []byte("movement_notices = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(event, profile string) string {
		t.Helper()
		var out bytes.Buffer
		rootCmd := NewRoot(&out, all.Registry())
		// Hooks launched by agents may inherit different directories. Explicit
		// installation flags must win for settings, cache, and delivery receipts.
		_ = os.Setenv("HOPSESH_CONFIG_DIR", wrongConfig)
		_ = os.Setenv("HOPSESH_STATE_DIR", wrongState)
		rootCmd.SetArgs([]string{"notice-hook", "--agent", "claude", "--profile", profile, "--config-dir", configDir, "--state-dir", stateDir})
		input, _ := json.Marshal(map[string]string{"session_id": "alice-session", "transcript_path": path, "hook_event_name": event})
		rootCmd.SetIn(bytes.NewReader(input))
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	first := run("SessionStart", "")
	if !json.Valid([]byte(first)) || !strings.Contains(first, "Prepared in Codex on bob-laptop") {
		t.Fatalf("missing hook notice: %q", first)
	}
	if got := run("UserPromptSubmit", ""); got != "" {
		t.Fatalf("repeated unchanged prompt notice: %s", got)
	}
	if got := run("SessionStart", ""); got != first {
		t.Fatalf("resume did not restore notice: %s", got)
	}
	if got := run("SessionStart", "not-registered"); got != "" {
		t.Fatalf("wrong profile disclosed notice: %s", got)
	}
	// A->B->A->B can happen before any scan and without an intervening hook.
	// Identical display words must not suppress the second operation.
	if _, err := os.Stat(filepath.Join(stateDir, "movement")); !os.IsNotExist(err) {
		t.Fatal("hook wrote scan evidence")
	}
	if err := g.AppendHop(lineage.Hop{Kind: lineage.HopContinue, ID: "return-one", From: to, To: from, Parents: []string{"move-one"}, Notify: true}); err != nil {
		t.Fatal(err)
	}
	if err := g.AppendHop(lineage.Hop{Kind: lineage.HopContinue, ID: "move-two", From: from, To: to, Parents: []string{"return-one"}, Notify: true}); err != nil {
		t.Fatal(err)
	}
	side = g.Encode()
	if err := os.WriteFile(lineage.PathFor(path), side, 0600); err != nil {
		t.Fatal(err)
	}
	a := app.New(config.Config{}, all.Registry(), stateDir, nil)
	details, err := a.MovementNoticeDetailsForPath(context.Background(), "claude", "", "alice-session", path)
	if err != nil || details.Operation != "move-two" {
		t.Fatalf("details: %+v %v", details, err)
	}
	identity := app.MovementNoticeIdentity{Operation: details.Operation, Status: details.Status}
	if a.MovementNoticeDelivery("claude", "", "alice-session", details.Text, identity) {
		t.Fatal("first hop delivered second operation")
	}
	if got := run("UserPromptSubmit", ""); got != strings.Replace(first, `"hookEventName":"SessionStart"`, `"hookEventName":"UserPromptSubmit"`, 1) {
		t.Fatalf("same-word new operation suppressed: %q", got)
	}
	if !a.MovementNoticeDelivery("claude", "", "alice-session", details.Text, identity) {
		t.Fatal("successful new operation missing delivery evidence")
	}
	if got := run("UserPromptSubmit", ""); got != "" {
		t.Fatalf("new operation repeated: %s", got)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "movement")); !os.IsNotExist(err) {
		t.Fatal("hook rewrote scan evidence")
	}
	b, _ := os.ReadFile(path)
	after, _ := os.ReadFile(lineage.PathFor(path))
	if !bytes.Equal(b, original) || !bytes.Equal(after, side) {
		t.Fatal("hook appended transcript or lineage work")
	}
	cfg := config.Defaults()
	off := false
	cfg.MovementNotices = &off
	if err = config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if got := run("SessionStart", ""); got != "" {
		t.Fatalf("disabled setting leaked notice: %s", got)
	}
}

type noticeWriterFunc func([]byte) (int, error)

func (f noticeWriterFunc) Write(b []byte) (int, error) { return f(b) }

func TestNoticeHookFailedOrTimedOutStdoutRetries(t *testing.T) {
	for _, mode := range []string{"broken", "short", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			a := app.New(config.Config{}, all.Registry(), t.TempDir(), nil)
			const notice = "Prepared on bob-laptop"
			prepare := func(ctx context.Context, id agent.ID, profile, session, path, event string) (preparedMovementNotice, error) {
				claim, err := a.ClaimMovementNotice(ctx, id, profile, session, event, notice)
				if err != nil || claim == nil {
					return preparedMovementNotice{}, err
				}
				return preparedMovementNotice{Text: notice, Written: claim.Written}, nil
			}
			run := func(ctx context.Context, event string, out io.Writer) {
				t.Helper()
				input := `{"session_id":"s","hook_event_name":"` + event + `"}`
				if err := runPreparedNoticeHook(ctx, strings.NewReader(input), out, "claude", "p", prepare); err != nil {
					t.Fatal(err)
				}
			}
			supplied := func() bool { return a.MovementNoticeDelivery("claude", "p", "s", notice) }
			var first bytes.Buffer
			run(context.Background(), "SessionStart", &first)
			if !supplied() {
				t.Fatal("successful delivery missing")
			}
			release, finished := make(chan struct{}), make(chan struct{})
			bad := noticeWriterFunc(func(b []byte) (int, error) {
				switch mode {
				case "broken":
					return 0, io.ErrClosedPipe
				case "short":
					return len(b) - 1, nil
				default:
					<-release
					close(finished)
					return len(b), nil
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			started := time.Now()
			run(ctx, "SessionStart", bad)
			if time.Since(started) > time.Second {
				t.Fatal("stdout blocked executable exit")
			}
			if mode == "blocked" {
				close(release)
				<-finished
			}
			if supplied() {
				t.Fatal("failed resume claimed supplied-to-hook")
			}
			var retry bytes.Buffer
			run(context.Background(), "UserPromptSubmit", &retry)
			if !json.Valid(retry.Bytes()) || !supplied() {
				t.Fatalf("next event suppressed after %s: %s", mode, retry.Bytes())
			}
			var repeat bytes.Buffer
			run(context.Background(), "UserPromptSubmit", &repeat)
			if repeat.Len() != 0 {
				t.Fatal("successfully written notice repeated")
			}
		})
	}
}
