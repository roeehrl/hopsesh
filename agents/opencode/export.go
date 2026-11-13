package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
		m, err := decodeMessage(raw)
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
		m, err := decodeMessage(r)
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
	argv := []string{"session", "export", string(sid)}
	if svc.standalone() {
		argv = append(argv, "--standalone")
	}
	res, err := run(ctx, h, argv, agent.RunOptions{})
	if err != nil {
		return session{}, nil, err
	}
	if res.Code != 0 {
		return session{}, nil, commandError("session export", res)
	}
	return fromTransfer(res.Stdout)
}

// Materialize is a bundle file's bytes: the session (or the child session the file is)
// as JSON lines, from the database, or from OpenCode's export where hopsesh cannot read
// the database (the remote API tier).
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
	sess, msgs, err := m.transcript(ctx, h, in, sid)
	if err != nil {
		return nil, err
	}
	return encodeJSONL(sess, msgs)
}

func containsID(ids []agent.SessionID, x agent.SessionID) bool {
	for _, i := range ids {
		if i == x {
			return true
		}
	}
	return false
}
