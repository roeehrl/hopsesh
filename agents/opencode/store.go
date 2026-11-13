package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Sessions are changed only through OpenCode's program (agent.SessionStore): a backup is
// OpenCode's export of the session and of each of its children, kept under
// {data}/hopsesh/opencode/backup; a delete is `opencode session delete`, which deletes the
// children too, made only while the session's stamp is the one the caller names; a
// restore imports the backup again.

// writeService probes OpenCode's background service before a change: with it stopped, the
// change runs on a private server, whose OpenCode must read every session in the database.
func writeService(ctx context.Context, h agent.Host, in agent.Install) (service, error) {
	svc := probeService(ctx, h)
	if !svc.standalone() {
		return svc, nil
	}
	newest := ""
	if r, ok, err := openSQL(ctx, h, in); ok {
		if err == nil {
			newest, err = r.maxVersion(ctx)
		}
		if err != nil && !readUnsupported(err) && !errors.Is(err, agent.ErrNotInstalled) {
			return svc, err
		}
	}
	return svc, checkStandalone(svc, in.Version, newest)
}

// backupDir is where backups of sessions hopsesh set aside are kept.
func backupDir(h agent.Host, in agent.Install) string {
	return h.Path().Join(in.Root(rootData), "hopsesh", string(id), "backup")
}

// Backup saves the session and its children as OpenCode exports them: <id>-<time>.json,
// and each child in <id>-<time>.children/<n>-<child>.json, parents first. The stamp is
// the session's before and after the export, which must agree.
func (m *Module) Backup(ctx context.Context, h agent.Host, in agent.Install, sid agent.SessionID) (string, agent.SessionStamp, error) {
	svc := probeService(ctx, h)
	for attempt := 0; ; attempt++ {
		before, err := m.SessionStamp(ctx, h, in, sid)
		if err != nil {
			return "", "", err
		}
		kids, err := m.children(ctx, h, in, sid)
		if err != nil {
			return "", "", err
		}
		var files [][]byte
		for _, s := range append([]agent.SessionID{sid}, kids...) {
			b, err := m.exported(ctx, h, in, svc, s)
			if err != nil {
				return "", "", fmt.Errorf("exporting %s: %w", s, err)
			}
			files = append(files, b)
		}
		after, err := m.SessionStamp(ctx, h, in, sid)
		if err != nil {
			return "", "", err
		}
		if before != after {
			if attempt < 2 {
				continue
			}
			return "", "", fmt.Errorf("%w: the OpenCode session %s kept changing while hopsesh backed it up", agent.ErrDiverged, sid)
		}
		base := h.Path().Join(backupDir(h, in), fmt.Sprintf("%s-%d", sid, time.Now().UnixNano()))
		if err := h.FS().WriteFile(base+".json", files[0], 0o600); err != nil {
			return "", "", err
		}
		for i, kid := range kids {
			p := h.Path().Join(base+".children", fmt.Sprintf("%03d-%s.json", i+1, kid))
			if err := h.FS().WriteFile(p, files[i+1], 0o600); err != nil {
				return "", "", err
			}
		}
		return base + ".json", before, nil
	}
}

// exported is a session as OpenCode's import reads it: its export, or, where OpenCode's
// program does not run, the same made from the database (refused for a session with
// media, which only the export carries).
func (m *Module) exported(ctx context.Context, h agent.Host, in agent.Install, svc service, sid agent.SessionID) ([]byte, error) {
	b, err := exportRaw(ctx, h, svc, sid)
	if err == nil || !notRun(err) {
		return b, err
	}
	s, msgs, err := m.movable(ctx, h, in, sid)
	if err != nil {
		return nil, err
	}
	t := transfer{Info: infoOf(s), Messages: make([]json.RawMessage, 0, len(msgs))}
	t.Info.Version = ""
	for _, msg := range msgs {
		t.Messages = append(t.Messages, msg.Raw)
	}
	return json.Marshal(t)
}

// Delete deletes the session, and with it its children, through OpenCode, only while its
// stamp is expect.
func (m *Module) Delete(ctx context.Context, h agent.Host, in agent.Install, sid agent.SessionID, expect agent.SessionStamp) error {
	if expect == "" {
		return fmt.Errorf("%w: hopsesh deletes an OpenCode session only at a state it recorded", agent.ErrDenied)
	}
	st, err := m.SessionStamp(ctx, h, in, sid)
	if err != nil {
		return err
	}
	if st != expect {
		return fmt.Errorf("%w: the OpenCode session %s was used since", agent.ErrDiverged, sid)
	}
	svc, err := writeService(ctx, h, in)
	if err != nil {
		return err
	}
	argv := []string{"session", "delete", string(sid)}
	if svc.standalone() {
		argv = append(argv, "--standalone")
	}
	res, err := run(ctx, h, argv, agent.RunOptions{})
	if err != nil {
		return err
	}
	switch {
	case res.Code == 0:
		return nil
	case strings.Contains(strings.ToLower(string(res.Stderr)), "session not found"):
		return fmt.Errorf("%w: OpenCode has no session %s", agent.ErrNotFound, sid)
	}
	return fmt.Errorf("OpenCode did not delete the session %s: %w", sid, commandError("session delete", res))
}

// Restore imports a Backup again, the session first, each in the folder it was in. A
// session already there with the backup's messages is left; with others, it is newer
// work, and ErrDiverged.
func (m *Module) Restore(ctx context.Context, h agent.Host, in agent.Install, sid agent.SessionID, backup string) (agent.SessionStamp, error) {
	files := []string{backup}
	dir := strings.TrimSuffix(backup, ".json") + ".children"
	entries, err := h.FS().ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	var kids []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			kids = append(kids, h.Path().Join(dir, e.Name()))
		}
	}
	sort.Strings(kids) // numbered parents first
	files = append(files, kids...)
	svc, err := writeService(ctx, h, in)
	if err != nil {
		return "", err
	}
	for i, f := range files {
		b, err := agent.ReadNative(ctx, h.FS(), f)
		if err != nil {
			return "", err
		}
		s, msgs, err := fromTransfer(b)
		if err != nil {
			return "", fmt.Errorf("the backup %s: %w", f, err)
		}
		if i == 0 && s.ID != string(sid) {
			return "", fmt.Errorf("the backup %s is of the session %s, not %s", f, s.ID, sid)
		}
		want := make([]string, 0, len(msgs))
		for _, msg := range msgs {
			want = append(want, msg.ID)
		}
		file := f
		if err := m.importSession(ctx, h, in, svc, agent.SessionID(s.ID), want, nativeDir(h, s.Directory), func() (string, error) { return file, nil }); err != nil {
			return "", err
		}
	}
	return m.SessionStamp(ctx, h, in, sid)
}
