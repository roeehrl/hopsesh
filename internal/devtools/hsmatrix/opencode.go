package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/testkit/fakeopencode"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// OpenCode keeps its sessions in a database, which the helper reaches only on a machine
// whose `opencode` is the stand-in (internal/devtools/fakeagent built as opencode): a
// session is imported and deleted through that program, a turn is added to the
// stand-in's database as OpenCode would add it, and sessions are found by their export.
// The helper refuses everything else, so it never touches a real OpenCode's data.

// standIn is whether the opencode on PATH is the stand-in, asked once.
var standIn = sync.OnceValues(func() (bool, error) {
	if _, err := exec.LookPath("opencode"); err != nil {
		return false, nil // no OpenCode on this machine
	}
	// The stand-in logs every call to $FAKE_AGENT_LOG; OpenCode does not know it.
	f, err := os.CreateTemp("", "hsmatrix-opencode-*.log")
	if err != nil {
		return false, err
	}
	f.Close()
	defer os.Remove(f.Name())
	cmd := exec.Command("opencode", "--version")
	cmd.Env = append(os.Environ(), "FAKE_AGENT_LOG="+f.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("opencode --version: %v: %s", err, out)
	}
	if b, _ := os.ReadFile(f.Name()); !strings.Contains(string(b), "opencode --version") {
		return false, errors.New("the opencode on PATH is not the stand-in (internal/devtools/fakeagent built as opencode); the matrix never runs a real OpenCode")
	}
	return true, nil
})

// needStandIn is an error unless this machine's opencode is the stand-in.
func needStandIn() error {
	ok, err := standIn()
	if err == nil && !ok {
		err = errors.New("opencode is not on PATH")
	}
	return err
}

// ocRun runs the stand-in opencode in dir and returns its standard output.
func ocRun(dir string, args ...string) (string, error) {
	cmd := exec.Command("opencode", args...)
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("opencode %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// ocPaths are the stand-in's data folder and database, as it reports them.
var ocPaths = sync.OnceValues(func() ([2]string, error) {
	var p [2]string
	for i, name := range []string{"data", "db"} {
		out, err := ocRun("", "debug", "paths", name)
		if err != nil {
			return p, err
		}
		p[i] = strings.TrimSpace(out)
	}
	return p, nil
})

// ocRanOnce makes OpenCode's data folder when this machine's opencode is the stand-in, as
// OpenCode leaves it after its first start: hopsesh counts OpenCode as installed only then.
func ocRanOnce() error {
	if ok, err := standIn(); err != nil || !ok {
		return err
	}
	p, err := ocPaths()
	if err != nil {
		return err
	}
	return os.MkdirAll(p[0], 0o700)
}

// ocAnchor is where hopsesh keeps a session's anchor and lineage beside it.
func ocAnchor(data, id string) string {
	return filepath.Join(data, filepath.FromSlash(agent.AnchorRel("opencode", agent.SessionID(id))))
}

// opencodeSession imports a session in OpenCode's transfer form, a prompt and its reply
// (and the padding of a large one), and returns its anchor.
func opencodeSession(r SeedReq, cwd string) (string, error) {
	if err := needStandIn(); err != nil {
		return "", err
	}
	p, err := ocPaths()
	if err != nil {
		return "", err
	}
	t0 := time.Now().UTC().Add(-2 * time.Hour)
	ms := func(d time.Duration) int64 { return t0.Add(d).UnixMilli() }
	file := filepath.Join(cwd, "src", "main.go")
	tokens := map[string]any{"input": 0, "output": 0, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}}
	reply := func(at time.Duration, text string) map[string]any {
		return map[string]any{"id": fakeopencode.MessageID(t0.Add(at)), "time": map[string]any{"created": ms(at), "completed": ms(at)}, "type": "assistant",
			"agent": "build", "model": map[string]any{"id": "hsmatrix", "providerID": "hsmatrix"}, "content": []any{map[string]any{"type": "text", "text": text}},
			"finish": "stop", "cost": 0, "tokens": tokens}
	}
	msgs := []any{
		map[string]any{"id": fakeopencode.MessageID(t0), "time": map[string]any{"created": ms(0)}, "text": r.Text + " (" + file + ")", "files": []any{}, "type": "user"},
		reply(time.Minute, "Edited "+file),
	}
	for _, pad := range padding(r) {
		msgs = append(msgs, reply(2*time.Minute, pad))
	}
	msgs = append(msgs, map[string]any{"id": fakeopencode.MessageID(t0.Add(3 * time.Minute)), "time": map[string]any{"created": ms(3 * time.Minute)}, "type": "idle", "outcome": "succeeded"})
	info := map[string]any{"id": r.ID, "projectID": "global", "cost": 0, "tokens": tokens, "time": map[string]any{"created": ms(0), "updated": ms(time.Hour)},
		"title": r.Title, "location": map[string]any{"directory": cwd}}
	b, err := json.Marshal(map[string]any{"info": info, "messages": msgs})
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "hsmatrix-transfer-*.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	out, err := ocRun(cwd, "session", "import", f.Name(), "--directory", cwd, "--standalone")
	if err != nil {
		return "", err
	}
	if !strings.Contains(out, "Imported session: "+r.ID) {
		return "", fmt.Errorf("opencode did not import %s: %s", r.ID, out)
	}
	return ocAnchor(p[0], r.ID), nil
}

// opencodeFind is every top-level OpenCode session whose export holds the marker.
func opencodeFind(r FindReq) ([]Found, error) {
	ok, err := standIn()
	if err != nil || !ok {
		return nil, err
	}
	p, err := ocPaths()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(p[1]); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	db, err := fakeopencode.Open(p[1])
	if err != nil {
		return nil, err
	}
	rows, err := db.Query("SELECT id, title FROM session_v2 WHERE parent_id IS NULL")
	if err != nil {
		db.Close()
		return nil, err
	}
	titles := map[string]string{}
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			rows.Close()
			db.Close()
			return nil, err
		}
		titles[id] = title
	}
	rows.Close()
	db.Close()
	var out []Found
	for id, title := range titles {
		b, err := fakeopencode.Export(p[1], id)
		if err != nil {
			return nil, err
		}
		if !bytes.Contains(b, []byte(r.Marker)) {
			continue
		}
		text := string(b)
		anchor := ocAnchor(p[0], id)
		f := Found{SHA256: fmt.Sprintf("%x", sha256.Sum256(b)), Agent: "opencode", ID: id, Path: anchor, Bytes: int64(len(b)), Has: map[string]bool{}}
		for _, n := range r.Needles {
			f.Has[n] = strings.Contains(text, n) || strings.Contains(text, jsonEscape(n))
		}
		if strings.HasPrefix(title, "↪ ") {
			f.Label = title
		}
		if f.Graph, err = lineage.Read(host.LocalFS(), anchor); err != nil {
			return nil, fmt.Errorf("lineage beside %s: %w", anchor, err)
		}
		out = append(out, f)
	}
	return out, nil
}

// opencodeTurn adds a prompt to an OpenCode session, as a prompt in OpenCode adds it: a
// user message after the last one, and the session's update time.
func opencodeTurn(r AppendReq) error {
	if err := needStandIn(); err != nil {
		return err
	}
	p, err := ocPaths()
	if err != nil {
		return err
	}
	db, err := fakeopencode.Open(p[1])
	if err != nil {
		return err
	}
	defer db.Close()
	now := time.Now()
	data, _ := json.Marshal(map[string]any{"time": map[string]any{"created": now.UnixMilli()}, "text": r.Text, "files": []any{}})
	res, err := db.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data)
		SELECT ?, ?, 'user', coalesce(max(seq), 0) + 1, ?, ?, ? FROM session_message WHERE session_id = ?`,
		fakeopencode.MessageID(now), r.ID, now.UnixMilli(), now.UnixMilli(), string(data), r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("no OpenCode session %s", r.ID)
	}
	_, err = db.Exec(`UPDATE session_v2 SET time_updated = max(time_updated + 1, ?) WHERE id = ?`, now.UnixMilli(), r.ID)
	return err
}

// opencodeRemove deletes an OpenCode session through the stand-in, which leaves
// hopsesh's anchor and the lineage beside it.
func opencodeRemove(id string) error {
	if err := needStandIn(); err != nil {
		return err
	}
	_, err := ocRun("", "session", "delete", id, "--standalone")
	return err
}
