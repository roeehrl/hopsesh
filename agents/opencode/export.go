package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A session's file, as hopsesh moves and packages it, is JSON lines: its info first
// ({"kind":"info",…}, OpenCode's export info plus the version that wrote it), then one
// line per settled message, each as OpenCode's `session export` writes it. Its child
// sessions are files of their own. The file is made from the database; OpenCode's own
// import reads the same content (as {info, messages}).

// transfer is OpenCode's export: {info, messages}.
type transfer struct {
	Info     info              `json:"info"`
	Messages []json.RawMessage `json:"messages"`
}

// encodeJSONL is a session as JSON lines.
func encodeJSONL(s session, msgs []message) ([]byte, error) {
	in := infoOf(s)
	in.Kind = "info"
	var b bytes.Buffer
	line, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	b.Write(append(line, '\n'))
	for _, m := range msgs {
		b.Write(canonical(m.Raw))
		b.WriteByte('\n')
	}
	return b.Bytes(), nil
}

// decodeJSONL reads a session's JSON lines: its info and its settled messages.
func decodeJSONL(path string, data []byte) (session, []message, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 1<<20), len(data)+1)
	var (
		s     session
		msgs  []message
		lineN int
	)
	for sc.Scan() {
		lineN++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		raw := append(json.RawMessage(nil), line...)
		if lineN == 1 {
			var in info
			if err := json.Unmarshal(raw, &in); err != nil {
				return session{}, nil, &agent.FormatError{Path: path, Line: lineN, Err: err}
			}
			if in.Kind != "info" || in.ID == "" {
				return session{}, nil, &agent.FormatError{Path: path, Line: 1, Err: errors.New("an OpenCode session file starts with its info")}
			}
			s = sessionOf(in)
			continue
		}
		m, err := decodeMessage(upgradeRaw(raw))
		if err != nil {
			return session{}, nil, &agent.FormatError{Path: path, Line: lineN, Err: err}
		}
		if settled(m) {
			msgs = append(msgs, m)
		}
	}
	if err := sc.Err(); err != nil {
		return session{}, nil, &agent.FormatError{Path: path, Err: err}
	}
	if lineN == 0 {
		return session{}, nil, &agent.FormatError{Path: path, Err: errors.New("the file is empty")}
	}
	return s, msgs, nil
}

// fromTransfer reads OpenCode's export of a session.
func fromTransfer(raw []byte) (session, []message, error) {
	var t transfer
	if err := json.Unmarshal(raw, &t); err != nil {
		return session{}, nil, fmt.Errorf("OpenCode's export: %w", err)
	}
	if t.Info.ID == "" {
		return session{}, nil, errors.New("OpenCode's export has no session info")
	}
	var msgs []message
	for _, r := range t.Messages {
		m, err := decodeMessage(upgradeRaw(r)) // an export may keep a pre-2.1 shape
		if err != nil {
			return session{}, nil, fmt.Errorf("OpenCode's export: %w", err)
		}
		if settled(m) {
			msgs = append(msgs, m)
		}
	}
	return sessionOf(t.Info), msgs, nil
}

// exportSession asks OpenCode for a session: `opencode session export <id>`.
func exportSession(ctx context.Context, h agent.Host, svc service, sid agent.SessionID) (session, []message, error) {
	raw, err := exportRaw(ctx, h, svc, sid)
	if err != nil {
		return session{}, nil, err
	}
	return fromTransfer(raw)
}

// exportRaw is OpenCode's export of a session as it prints it: what its import reads.
func exportRaw(ctx context.Context, h agent.Host, svc service, sid agent.SessionID) ([]byte, error) {
	argv := []string{"session", "export", string(sid)}
	if svc.standalone() {
		argv = append(argv, "--standalone")
	}
	res, err := run(ctx, h, argv, agent.RunOptions{})
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		err := commandError("session export", res)
		if strings.Contains(strings.ToLower(string(res.Stderr)), "session not found") {
			return nil, fmt.Errorf("%w (%v)", agent.ErrNotFound, err)
		}
		return nil, err
	}
	return res.Stdout, nil
}

// Materialize is a bundle file's bytes: the session (or the child session the file is)
// as JSON lines, for a move or a package. They are made from OpenCode's own export where
// its program runs, since that is what its import reads: the session's permission rules
// (a column the module cannot name) and the media OpenCode keeps apart (the database holds
// only "media:" references, which an import refuses) are in it. Where the program does not
// run, they are made from the database, and a session with media is refused.
func (m *Module) Materialize(ctx context.Context, h agent.Host, in agent.Install, s agent.Summary, f agent.BundleFile) ([]byte, error) {
	sid := s.Key.Session
	if f.Role != agent.RoleMain {
		child := childOf(s.Key.Session, f.Rel)
		if child == "" {
			return nil, fmt.Errorf("%s is not a file of the OpenCode session %s", f.Rel, s.Key.Session)
		}
		kids, err := m.children(ctx, h, in, s.Key.Session)
		if err != nil {
			return nil, err
		}
		if !containsID(kids, child) {
			return nil, fmt.Errorf("%w: %s is not a child session of %s", agent.ErrNotFound, child, s.Key.Session)
		}
		sid = child
	}
	sess, msgs, err := m.movable(ctx, h, in, sid)
	if err != nil {
		return nil, err
	}
	return encodeJSONL(sess, msgs)
}

// movable is a session as a move carries it: OpenCode's export, with the OpenCode version
// that last wrote it from the database; else (OpenCode's program does not run here, or may
// not: exportService) the database alone, without media.
func (m *Module) movable(ctx context.Context, h agent.Host, in agent.Install, sid agent.SessionID) (session, []message, error) {
	svc, why := exportService(ctx, h, in)
	if why == nil {
		sess, msgs, err := exportSession(ctx, h, svc, sid)
		if err == nil {
			if r, ok, e := openSQL(ctx, h, in); ok && e == nil {
				if row, e := r.session(ctx, sid); e == nil {
					sess.Version = row.Version
				}
			}
			return sess, msgs, nil
		}
		if !notRun(err) {
			return session{}, nil, err
		}
		why = err
	}
	sess, msgs, err := m.transcript(ctx, h, in, sid)
	if err != nil {
		return session{}, nil, err
	}
	if err := noMedia(sid, msgs, why); err != nil {
		return session{}, nil, err
	}
	return sess, msgs, nil
}

// exportService is the service an export a move or a backup carries runs through, or why
// OpenCode's program does not export here (then the session is read from the database).
// With the service stopped, the export runs a private server, which must be an OpenCode
// that reads every session in the database, as for a change (writeService): an older one
// could migrate or misread it.
func exportService(ctx context.Context, h agent.Host, in agent.Install) (service, error) {
	return writeService(ctx, h, in)
}

// noMedia refuses a session read from the database that holds media: only OpenCode's own
// export carries them (the database holds "media:" references, which an import refuses).
// why is the reason OpenCode's export was not used.
func noMedia(sid agent.SessionID, msgs []message, why error) error {
	for _, msg := range msgs {
		if bytes.Contains(msg.Raw, []byte(`"media:`)) {
			return fmt.Errorf("%w: the OpenCode session %s holds media that only OpenCode's own export carries, and hopsesh does not export it here (%v)", agent.ErrUnsupported, sid, why)
		}
	}
	return nil
}

// readFile reads a session's JSON lines from a file only: a copy hopsesh staged or
// placed, never the database (a staged copy's id may be a session the database has).
func readFile(ctx context.Context, h agent.Host, p string) (session, []message, error) {
	b, err := agent.ReadNative(ctx, h.FS(), p)
	if err != nil {
		return session{}, nil, err
	}
	return decodeJSONL(p, b)
}

func containsID(ids []agent.SessionID, x agent.SessionID) bool {
	for _, i := range ids {
		if i == x {
			return true
		}
	}
	return false
}
