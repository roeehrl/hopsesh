package iterm2api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

// ListSessions returns iTerm2's windows, tabs and sessions by id.
func (c *Client) ListSessions(ctx context.Context) (Layout, error) {
	body, err := c.call(ctx, listSessionsRequest{})
	if err != nil {
		return Layout{}, err
	}
	return decodeListSessions(body)
}

// KeyWindow returns the id of the current terminal window (the key window, or the last
// one that was), or "" if there is none.
func (c *Client) KeyWindow(ctx context.Context) (string, error) {
	body, err := c.call(ctx, focusRequest{})
	if err != nil {
		return "", err
	}
	fs, err := decodeFocus(body)
	if err != nil {
		return "", err
	}
	for _, f := range fs {
		if f.WindowID != "" && f.WindowKey {
			return f.WindowID, nil
		}
	}
	return "", nil
}

// SessionTTY returns the TTY device of a session ("/dev/ttys003"), as iTerm2's own process
// bookkeeping knows it.
func (c *Client) SessionTTY(ctx context.Context, session string) (string, error) {
	r, err := newVariableGet(session, "tty")
	if err != nil {
		return "", err
	}
	body, err := c.call(ctx, r)
	if err != nil {
		return "", err
	}
	st, vals, err := decodeVariable(body)
	if err != nil {
		return "", err
	}
	if st != 0 {
		return "", &RequestError{Request: "variable", Status: st}
	}
	if len(vals) != 1 {
		return "", errors.New("iterm2api: variable: no value")
	}
	var tty *string
	if err := json.Unmarshal([]byte(vals[0]), &tty); err != nil {
		return "", fmt.Errorf("iterm2api: tty: %w", err)
	}
	if tty == nil {
		return "", nil
	}
	return *tty, nil
}

// Located is a session found in iTerm2's layout.
type Located struct {
	WindowID, TabID, SessionID string
}

// FindTTY finds the session whose TTY is tty ("/dev/ttys003" or "ttys003"). The TTY must
// come from hopsesh's own knowledge (a vendor registry's PID through ps, or hopsesh's
// launch record), never from anything shown in a tab; the caller re-checks that PID right
// before focusing.
func (c *Client) FindTTY(ctx context.Context, tty string) (Located, bool, error) {
	want := normTTY(tty)
	if want == "" {
		return Located{}, false, nil
	}
	l, err := c.ListSessions(ctx)
	if err != nil {
		return Located{}, false, err
	}
	for _, w := range l.Windows {
		for _, t := range w.Tabs {
			for _, s := range t.Sessions {
				got, err := c.SessionTTY(ctx, s)
				var re *RequestError
				if errors.As(err, &re) {
					continue // the session went away while we looked
				}
				if err != nil {
					return Located{}, false, err
				}
				if normTTY(got) == want {
					return Located{WindowID: w.ID, TabID: t.ID, SessionID: s}, true, nil
				}
			}
		}
	}
	return Located{}, false, nil
}

func normTTY(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	return "/dev/" + path.Base(t)
}

// Launch is what a new tab or split runs. Command replaces the profile's command for this
// session only (iTerm2 parses it into arguments itself; QuoteArgv builds one); Dir is its
// working directory. Neither changes the profile.
type Launch struct {
	Profile string // empty: the default profile
	Command string
	Dir     string
}

// OpenTab opens a new tab in window (a new window if window is "") running l.
func (c *Client) OpenTab(ctx context.Context, window string, l Launch) (Opened, error) {
	props, err := launchProperties(l.Command, l.Dir)
	if err != nil {
		return Opened{}, err
	}
	body, err := c.call(ctx, createTabRequest{profile: l.Profile, windowID: window, props: props})
	if err != nil {
		return Opened{}, err
	}
	st, o, err := decodeCreateTab(body)
	if err != nil {
		return Opened{}, err
	}
	// 3 is "created, but not at the requested index", which the client never requests.
	if st != 0 && st != 3 {
		return Opened{}, &RequestError{Request: "create tab", Status: st}
	}
	if o.SessionID == "" {
		return Opened{}, errors.New("iterm2api: create tab: no session id")
	}
	return o, nil
}

// SplitSide is where a split goes relative to the session it splits.
type SplitSide int

const (
	SplitRight SplitSide = iota // beside, on the right (a vertical divider)
	SplitLeft
	SplitBelow // underneath (a horizontal divider)
	SplitAbove
)

// OpenSplit splits session's pane and runs l in the new one.
func (c *Client) OpenSplit(ctx context.Context, session string, side SplitSide, l Launch) (Opened, error) {
	if session == "" || session == "all" {
		return Opened{}, errors.New("iterm2api: split needs one session id")
	}
	props, err := launchProperties(l.Command, l.Dir)
	if err != nil {
		return Opened{}, err
	}
	body, err := c.call(ctx, splitPaneRequest{
		session:    session,
		horizontal: side == SplitBelow || side == SplitAbove,
		before:     side == SplitLeft || side == SplitAbove,
		profile:    l.Profile,
		props:      props,
	})
	if err != nil {
		return Opened{}, err
	}
	st, ids, err := decodeSplitPane(body)
	if err != nil {
		return Opened{}, err
	}
	if st != 0 {
		return Opened{}, &RequestError{Request: "split pane", Status: st}
	}
	if len(ids) == 0 {
		return Opened{}, errors.New("iterm2api: split pane: no session id")
	}
	return Opened{SessionID: ids[0]}, nil
}

// Focus selects a session's tab and pane, brings its window to the front and activates
// iTerm2.
func (c *Client) Focus(ctx context.Context, session string) error {
	if session == "" || session == "all" || session == "active" {
		return errors.New("iterm2api: focus needs one session id")
	}
	body, err := c.call(ctx, activateRequest{session: session})
	if err != nil {
		return err
	}
	st, err := status(body)
	if err != nil {
		return err
	}
	if st != 0 {
		return &RequestError{Request: "activate", Status: st}
	}
	return nil
}

// SetLabels sets user.hopsesh_<name> variables on a session (for badges and titles that
// interpolate them). Names are lower-case letters, digits and underscores; values must
// already be clean (termapp.Sanitize), and one that is not is refused. Labels are for
// people: hopsesh never reads them back as identity.
func (c *Client) SetLabels(ctx context.Context, session string, labels map[string]string) error {
	if len(labels) == 0 {
		return nil
	}
	r, err := newVariableSet(session, labels)
	if err != nil {
		return err
	}
	body, err := c.call(ctx, r)
	if err != nil {
		return err
	}
	st, err := status(body)
	if err != nil {
		return err
	}
	if st != 0 {
		return &RequestError{Request: "variable", Status: st}
	}
	return nil
}

// Subscribe asks for new-session, session-terminated and focus-changed notifications;
// they arrive on Events, which the caller must then drain until it is closed (or Close the
// client). Notifications that arrive before Subscribe are discarded.
func (c *Client) Subscribe(ctx context.Context) error {
	c.subscribed.Store(true)
	for _, k := range []uint64{notifyTerminateSession, notifyNewSession, notifyFocusChange} {
		r, err := newNotificationRequest(k)
		if err != nil {
			return err
		}
		body, err := c.call(ctx, r)
		if err != nil {
			return err
		}
		st, err := status(body)
		if err != nil {
			return err
		}
		if st != 0 && st != 4 { // 4: already subscribed
			return &RequestError{Request: "subscribe", Status: st}
		}
	}
	return nil
}

// QuoteArgv joins argv into one command line for Launch.Command, single-quoting every
// argument that is not plainly safe (iTerm2 splits the command with shell-like quoting).
func QuoteArgv(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = quoteArg(a)
	}
	return strings.Join(parts, " ")
}

func quoteArg(a string) string {
	if a == "" {
		return "''"
	}
	safe := true
	for _, r := range a {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%+=:,./_-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}
