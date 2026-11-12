package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// cdFloor is the first Codex that records a resumed thread's settings, its folder among
// them, as a thread_settings_applied event.
const cdFloor = "0.133.0"

// PlanChangeDir has Codex itself change the thread's folder, the way `codex resume <id> -C
// <dir>` does: Codex resumes the thread in dir (no turn, no model call) and records the
// folder in the rollout (thread_settings_applied) and its own index. Nothing moves: a
// rollout's place does not depend on its folder. Codex's own /cd is not used: it forks the
// thread into a new one.
func (m *Module) PlanChangeDir(_ context.Context, h agent.Host, in agent.Install, s agent.Summary, dir string) (agent.DirPlan, error) {
	p := agent.DirPlan{From: s.CWD, To: dir, Method: agent.DirAgent, Main: s.Path}
	switch {
	case in.Binary == "":
		p.Blockers = append(p.Blockers, "codex is not installed here")
	case in.Version != "" && !agent.HookVersionAtLeast(in.Version, cdFloor):
		p.Blockers = append(p.Blockers, fmt.Sprintf("Codex %s does not record a resumed thread's folder; update it to %s or later", in.Version, cdFloor))
	}
	if !h.Facts().Local {
		p.Blockers = append(p.Blockers, "hopsesh changes a Codex session's folder on this machine only")
	}
	if strings.Contains(toSlash(s.Path), "/archived_sessions/") {
		p.Blockers = append(p.Blockers, "the session is archived in Codex; unarchive it first")
	}
	p.Records = append(p.Records, "Codex's own record of the new folder (thread_settings_applied), from codex app-server")
	p.Notes = append(p.Notes,
		"Codex reads "+dir+"'s AGENTS.md and makes it the sandbox's writable folder from the next turn, and asks to trust it if it is new to it",
		"undo changes the folder back the same way (Codex keeps both records)")
	return p, nil
}

// ChangeDir asks a one-shot `codex app-server` to resume the thread in the new folder,
// then reads the rollout back: Codex must have recorded the folder.
func (m *Module) ChangeDir(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, p agent.DirPlan) error {
	if len(p.Blockers) > 0 {
		return errors.New(p.Blockers[0])
	}
	reqs := []map[string]any{{"id": 2, "method": "thread/resume", "params": map[string]any{
		"threadId": string(s.Key.Session), "cwd": p.To, "excludeTurns": true}}}
	lines, err := appServer(ctx, h, in, reqs, `{"id":2,`, 30*time.Second)
	if err != nil {
		return fmt.Errorf("asking codex app-server to resume the session in %s: %w", p.To, err)
	}
	answered := false
	for _, l := range lines {
		var r reply
		if json.Unmarshal(l, &r) != nil || r.ID == nil || *r.ID != 2 {
			continue
		}
		if r.Error != nil {
			return errors.New("codex app-server did not resume the session: " + r.Error.Message)
		}
		answered = true
	}
	if !answered {
		return errors.New("no answer from codex app-server in time")
	}
	got, err := recordedCWD(h, s.Path, string(s.Key.Session), s.NativeParent != "")
	if err != nil {
		return err
	}
	if got != p.To {
		return fmt.Errorf("codex resumed the session but recorded %q as its folder, not %q", got, p.To)
	}
	return nil
}

// InSession is "": Codex's /cd starts a new thread (a fork), so the session itself can
// only change folder while it is closed.
func (m *Module) InSession(agent.Install, string) string { return "" }

// recordedCWD is the folder the newest thread_settings_applied in the rollout's end names
// for this thread ("" when there is none).
func recordedCWD(h agent.Host, path, id string, forked bool) (string, error) {
	f, err := h.FS().Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := max(fi.Size()-tailChunk, 0)
	b := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(b, start); err != nil && err != io.EOF {
		return "", err
	}
	return settingsCWD(lines(b, start > 0), id, forked), nil
}

// settingsCWD is the folder of the newest thread_settings_applied in ls that is this
// thread's own. A fork's rollout starts with its parent's records, so there an event must
// name the thread.
func settingsCWD(ls []line, id string, forked bool) string {
	for i := len(ls) - 1; i >= 0; i-- {
		if ls[i].Type != "event_msg" || !strings.Contains(string(ls[i].Payload), `"thread_settings_applied"`) {
			continue
		}
		var e struct {
			Type     string `json:"type"`
			Thread   string `json:"thread_id"`
			Settings struct {
				CWD string `json:"cwd"`
			} `json:"thread_settings"`
		}
		if json.Unmarshal(ls[i].Payload, &e) != nil || e.Type != "thread_settings_applied" || e.Settings.CWD == "" {
			continue
		}
		if e.Thread == id || e.Thread == "" && !forked {
			return e.Settings.CWD
		}
	}
	return ""
}
