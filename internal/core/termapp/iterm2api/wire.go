package iterm2api

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

// Field numbers of the request (and matching response) inside the protocol's envelope
// messages. These seven are the whole of what the client can ask iTerm2 for;
// allowlist_test.go checks that no other request type exists in this package.
const (
	fieldNotification protowire.Number = 103
	fieldListSessions protowire.Number = 106
	fieldCreateTab    protowire.Number = 108
	fieldSplitPane    protowire.Number = 109
	fieldActivate     protowire.Number = 114
	fieldVariable     protowire.Number = 115
	fieldFocus        protowire.Number = 117
)

// Envelope fields.
const (
	envID           protowire.Number = 1
	envError        protowire.Number = 2 // in a server message: the request was malformed
	envNotification protowire.Number = 1000
)

// Notification types the client may subscribe to. Everything else (keystrokes, screen
// updates, prompts, custom escape sequences, variable changes, registered functions) is
// refused by newNotificationRequest.
const (
	notifyNewSession       = 6
	notifyTerminateSession = 7
	notifyFocusChange      = 9
)

// Profile keys a created tab or split may override. "Initial Text" and every other key
// that would put input into the session are deliberately absent.
var allowedProfileKeys = map[string]bool{
	"Custom Command":    true,
	"Command":           true,
	"Custom Directory":  true,
	"Working Directory": true,
}

// Variables the client may read. "tty" is the only identity the client needs, and it comes
// from iTerm2's process bookkeeping, not from anything a program printed.
var allowedVariableReads = map[string]bool{"tty": true}

// userVarPrefix is the only namespace the client may write variables in.
const userVarPrefix = "user.hopsesh_"

var labelKey = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// request is one of the client's few request bodies.
type request interface {
	field() protowire.Number
	encode() []byte
}

// encodeClientMessage wraps a request in the protocol's ClientOriginatedMessage.
func encodeClientMessage(id int64, r request) []byte {
	var b []byte
	b = protowire.AppendTag(b, envID, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(id))
	b = appendBytes(b, r.field(), r.encode())
	return b
}

func appendBytes(b []byte, n protowire.Number, v []byte) []byte {
	b = protowire.AppendTag(b, n, protowire.BytesType)
	return protowire.AppendBytes(b, v)
}

func appendString(b []byte, n protowire.Number, s string) []byte {
	b = protowire.AppendTag(b, n, protowire.BytesType)
	return protowire.AppendString(b, s)
}

func appendBool(b []byte, n protowire.Number, v bool) []byte {
	b = protowire.AppendTag(b, n, protowire.VarintType)
	return protowire.AppendVarint(b, protowire.EncodeBool(v))
}

func appendVarint(b []byte, n protowire.Number, v uint64) []byte {
	b = protowire.AppendTag(b, n, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

// ListSessionsRequest: empty.
type listSessionsRequest struct{}

func (listSessionsRequest) field() protowire.Number { return fieldListSessions }
func (listSessionsRequest) encode() []byte          { return nil }

// FocusRequest: empty.
type focusRequest struct{}

func (focusRequest) field() protowire.Number { return fieldFocus }
func (focusRequest) encode() []byte          { return nil }

// profileProperty is a (key, JSON value) override applied to one new session only.
type profileProperty struct{ key, jsonValue string }

func launchProperties(command, dir string) ([]profileProperty, error) {
	var props []profileProperty
	add := func(k, v string) error {
		if !allowedProfileKeys[k] {
			return fmt.Errorf("iterm2api: profile key %q is not allowed", k)
		}
		j, err := json.Marshal(v)
		if err != nil {
			return err
		}
		props = append(props, profileProperty{k, string(j)})
		return nil
	}
	if command != "" {
		if err := add("Custom Command", "Yes"); err != nil {
			return nil, err
		}
		if err := add("Command", command); err != nil {
			return nil, err
		}
	}
	if dir != "" {
		if err := add("Custom Directory", "Yes"); err != nil {
			return nil, err
		}
		if err := add("Working Directory", dir); err != nil {
			return nil, err
		}
	}
	return props, nil
}

func appendProperties(b []byte, n protowire.Number, props []profileProperty) []byte {
	for _, p := range props {
		var m []byte
		m = appendString(m, 1, p.key)
		m = appendString(m, 2, p.jsonValue)
		b = appendBytes(b, n, m)
	}
	return b
}

// CreateTabRequest: profile_name 1, window_id 2, custom_profile_properties 5.
type createTabRequest struct {
	profile, windowID string
	props             []profileProperty
}

func (createTabRequest) field() protowire.Number { return fieldCreateTab }
func (r createTabRequest) encode() []byte {
	var b []byte
	if r.profile != "" {
		b = appendString(b, 1, r.profile)
	}
	if r.windowID != "" {
		b = appendString(b, 2, r.windowID)
	}
	return appendProperties(b, 5, r.props)
}

// SplitPaneRequest: session 1, split_direction 2 (0 vertical divider, 1 horizontal),
// before 3, profile_name 4, custom_profile_properties 5.
type splitPaneRequest struct {
	session    string
	horizontal bool
	before     bool
	profile    string
	props      []profileProperty
}

func (splitPaneRequest) field() protowire.Number { return fieldSplitPane }
func (r splitPaneRequest) encode() []byte {
	var b []byte
	b = appendString(b, 1, r.session)
	dir := uint64(0)
	if r.horizontal {
		dir = 1
	}
	b = appendVarint(b, 2, dir)
	if r.before {
		b = appendBool(b, 3, true)
	}
	if r.profile != "" {
		b = appendString(b, 4, r.profile)
	}
	return appendProperties(b, 5, r.props)
}

// ActivateRequest: session_id 3, order_window_front 4, select_tab 5, select_session 6,
// activate_app 7 {raise_all_windows 1, ignoring_other_apps 2}.
type activateRequest struct{ session string }

func (activateRequest) field() protowire.Number { return fieldActivate }
func (r activateRequest) encode() []byte {
	var b []byte
	b = appendString(b, 3, r.session)
	b = appendBool(b, 4, true)
	b = appendBool(b, 5, true)
	b = appendBool(b, 6, true)
	var app []byte
	app = appendBool(app, 2, true) // ignoring other apps: the user asked to be shown it
	return appendBytes(b, 7, app)
}

// VariableRequest: session_id 1, set 2 {name 1, value 2 (JSON)}, get 3.
type variableRequest struct {
	session string
	sets    [][2]string // name, JSON value
	gets    []string
}

func (variableRequest) field() protowire.Number { return fieldVariable }
func (r variableRequest) encode() []byte {
	var b []byte
	b = appendString(b, 1, r.session)
	for _, s := range r.sets {
		var m []byte
		m = appendString(m, 1, s[0])
		m = appendString(m, 2, s[1])
		b = appendBytes(b, 2, m)
	}
	for _, g := range r.gets {
		b = appendString(b, 3, g)
	}
	return b
}

func newVariableGet(session, name string) (variableRequest, error) {
	if !allowedVariableReads[name] {
		return variableRequest{}, fmt.Errorf("iterm2api: reading variable %q is not allowed", name)
	}
	if session == "" || session == "all" {
		return variableRequest{}, errors.New("iterm2api: a variable read needs one session id")
	}
	return variableRequest{session: session, gets: []string{name}}, nil
}

func newVariableSet(session string, labels map[string]string) (variableRequest, error) {
	if session == "" || session == "all" {
		return variableRequest{}, errors.New("iterm2api: labels need one session id")
	}
	r := variableRequest{session: session}
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		if !labelKey.MatchString(k) {
			return variableRequest{}, fmt.Errorf("iterm2api: label name %q must match %s", k, labelKey)
		}
		if !ValidLabel(labels[k]) {
			return variableRequest{}, fmt.Errorf("iterm2api: label %q is not sanitised", k)
		}
		j, err := json.Marshal(labels[k])
		if err != nil {
			return variableRequest{}, err
		}
		r.sets = append(r.sets, [2]string{userVarPrefix + k, string(j)})
	}
	return r, nil
}

// NotificationRequest: session 1, subscribe 2, notification_type 3. The arguments oneof
// (keystroke, variable, prompt and RPC monitors) is never written.
type notificationRequest struct {
	kind uint64
}

func (notificationRequest) field() protowire.Number { return fieldNotification }
func (r notificationRequest) encode() []byte {
	var b []byte
	b = appendString(b, 1, "all")
	b = appendBool(b, 2, true)
	return appendVarint(b, 3, r.kind)
}

func newNotificationRequest(kind uint64) (notificationRequest, error) {
	switch kind {
	case notifyNewSession, notifyTerminateSession, notifyFocusChange:
		return notificationRequest{kind: kind}, nil
	}
	return notificationRequest{}, fmt.Errorf("iterm2api: notification type %d is not allowed", kind)
}

// serverMessage is a decoded ServerOriginatedMessage envelope.
type serverMessage struct {
	id       int64
	hasID    bool
	errText  string
	isError  bool
	field    protowire.Number // the response or notification field present
	body     []byte
	hasField bool
}

// walk calls f for each field of a message. Bytes fields pass their payload in v, varint
// fields their value in u; other wire types are skipped.
func walk(b []byte, f func(n protowire.Number, t protowire.Type, v []byte, u uint64) error) error {
	for len(b) > 0 {
		n, t, l := protowire.ConsumeTag(b)
		if l < 0 {
			return protowire.ParseError(l)
		}
		b = b[l:]
		switch t {
		case protowire.BytesType:
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return protowire.ParseError(m)
			}
			if err := f(n, t, v, 0); err != nil {
				return err
			}
			b = b[m:]
		case protowire.VarintType:
			u, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return protowire.ParseError(m)
			}
			if err := f(n, t, nil, u); err != nil {
				return err
			}
			b = b[m:]
		default:
			m := protowire.ConsumeFieldValue(n, t, b)
			if m < 0 {
				return protowire.ParseError(m)
			}
			b = b[m:]
		}
	}
	return nil
}

func decodeServerMessage(b []byte) (serverMessage, error) {
	var m serverMessage
	err := walk(b, func(n protowire.Number, t protowire.Type, v []byte, u uint64) error {
		switch {
		case n == envID && t == protowire.VarintType:
			m.id, m.hasID = int64(u), true
		case n == envError && t == protowire.BytesType:
			m.errText, m.isError = string(v), true
		case t == protowire.BytesType:
			m.field, m.body, m.hasField = n, v, true
		}
		return nil
	})
	return m, err
}

// status reads field 1 of a response as its status enum (absent means OK).
func status(body []byte) (uint64, error) {
	var s uint64
	err := walk(body, func(n protowire.Number, t protowire.Type, _ []byte, u uint64) error {
		if n == 1 && t == protowire.VarintType {
			s = u
		}
		return nil
	})
	return s, err
}

// Layout is iTerm2's windows, tabs and sessions, by id only.
type Layout struct {
	Windows []Window
	// Buried lists sessions that are running but not in any tab.
	Buried []string
}

// Window is one terminal window.
type Window struct {
	ID            string
	SelectedTabID string // empty before protocol 1.18
	Tabs          []Tab
}

// Tab is one tab and the sessions (panes) in it.
type Tab struct {
	ID              string
	ActiveSessionID string // empty before protocol 1.18
	Sessions        []string
}

// Sessions lists every session id in the layout, buried ones included.
func (l Layout) Sessions() []string {
	var out []string
	for _, w := range l.Windows {
		for _, t := range w.Tabs {
			out = append(out, t.Sessions...)
		}
	}
	return append(out, l.Buried...)
}

// Locate returns the window and tab holding a session.
func (l Layout) Locate(session string) (windowID, tabID string, ok bool) {
	for _, w := range l.Windows {
		for _, t := range w.Tabs {
			for _, s := range t.Sessions {
				if s == session {
					return w.ID, t.ID, true
				}
			}
		}
	}
	return "", "", false
}

func decodeListSessions(b []byte) (Layout, error) {
	var l Layout
	err := walk(b, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) error {
		if t != protowire.BytesType {
			return nil
		}
		switch n {
		case 1:
			w, err := decodeWindow(v)
			if err != nil {
				return err
			}
			l.Windows = append(l.Windows, w)
		case 2:
			id, err := sessionSummaryID(v)
			if err != nil {
				return err
			}
			if id != "" {
				l.Buried = append(l.Buried, id)
			}
		}
		return nil
	})
	return l, err
}

func decodeWindow(b []byte) (Window, error) {
	var w Window
	err := walk(b, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) error {
		if t != protowire.BytesType {
			return nil
		}
		switch n {
		case 1:
			tab, err := decodeTab(v)
			if err != nil {
				return err
			}
			w.Tabs = append(w.Tabs, tab)
		case 2:
			w.ID = string(v)
		case 5:
			w.SelectedTabID = string(v)
		}
		return nil
	})
	return w, err
}

func decodeTab(b []byte) (Tab, error) {
	var tab Tab
	err := walk(b, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) error {
		if t != protowire.BytesType {
			return nil
		}
		switch n {
		case 2:
			tab.ID = string(v)
		case 3:
			return splitTreeSessions(v, &tab.Sessions, 0)
		case 6:
			id, err := sessionSummaryID(v)
			if err != nil {
				return err
			}
			if id != "" {
				tab.Sessions = append(tab.Sessions, id)
			}
		case 7:
			tab.ActiveSessionID = string(v)
		}
		return nil
	})
	return tab, err
}

// splitTreeSessions collects the session ids of a SplitTreeNode: links 2 {session 1 |
// node 2}.
func splitTreeSessions(b []byte, out *[]string, depth int) error {
	if depth > 64 {
		return errors.New("iterm2api: split tree too deep")
	}
	return walk(b, func(n protowire.Number, t protowire.Type, link []byte, _ uint64) error {
		if n != 2 || t != protowire.BytesType {
			return nil
		}
		return walk(link, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) error {
			if t != protowire.BytesType {
				return nil
			}
			switch n {
			case 1:
				id, err := sessionSummaryID(v)
				if err != nil {
					return err
				}
				if id != "" {
					*out = append(*out, id)
				}
			case 2:
				return splitTreeSessions(v, out, depth+1)
			}
			return nil
		})
	})
}

// sessionSummaryID reads unique_identifier (1) of a SessionSummary and nothing else (the
// summary's title is what the program in the session printed, which the client ignores).
func sessionSummaryID(b []byte) (string, error) {
	var id string
	err := walk(b, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) error {
		if n == 1 && t == protowire.BytesType {
			id = string(v)
		}
		return nil
	})
	return id, err
}

// Opened names the session a created tab or split runs in.
type Opened struct {
	WindowID  string
	TabID     string
	SessionID string
}

// CreateTabResponse: status 1, window_id 2, tab_id 3 (int32), session_id 4.
func decodeCreateTab(b []byte) (uint64, Opened, error) {
	var o Opened
	var st uint64
	err := walk(b, func(n protowire.Number, t protowire.Type, v []byte, u uint64) error {
		switch {
		case n == 1 && t == protowire.VarintType:
			st = u
		case n == 2 && t == protowire.BytesType:
			o.WindowID = string(v)
		case n == 3 && t == protowire.VarintType:
			o.TabID = fmt.Sprint(int32(u))
		case n == 4 && t == protowire.BytesType:
			o.SessionID = string(v)
		}
		return nil
	})
	return st, o, err
}

// SplitPaneResponse: status 1, session_id 2 (repeated).
func decodeSplitPane(b []byte) (uint64, []string, error) {
	var st uint64
	var ids []string
	err := walk(b, func(n protowire.Number, t protowire.Type, v []byte, u uint64) error {
		switch {
		case n == 1 && t == protowire.VarintType:
			st = u
		case n == 2 && t == protowire.BytesType:
			ids = append(ids, string(v))
		}
		return nil
	})
	return st, ids, err
}

// VariableResponse: status 1, values 2 (repeated, JSON).
func decodeVariable(b []byte) (uint64, []string, error) {
	var st uint64
	var vals []string
	err := walk(b, func(n protowire.Number, t protowire.Type, v []byte, u uint64) error {
		switch {
		case n == 1 && t == protowire.VarintType:
			st = u
		case n == 2 && t == protowire.BytesType:
			vals = append(vals, string(v))
		}
		return nil
	})
	return st, vals, err
}

// FocusChange is one FocusChangedNotification. Exactly one of its parts is set.
type FocusChange struct {
	// AppActive is set when iTerm2 became (true) or stopped being (false) the active app.
	AppActive *bool
	// WindowID is set when a window's key state changed; WindowKey tells whether it is now
	// the key window (or the current terminal window while another app's window is key).
	WindowID  string
	WindowKey bool
	// TabID is set when a window's selected tab changed.
	TabID string
	// SessionID is set when a session became the active pane of its tab.
	SessionID string
}

// FocusChangedNotification: application_active 1, window 2 {window_status 1, window_id 2},
// selected_tab 3, session 4.
func decodeFocusChange(b []byte) (FocusChange, error) {
	var f FocusChange
	err := walk(b, func(n protowire.Number, t protowire.Type, v []byte, u uint64) error {
		switch {
		case n == 1 && t == protowire.VarintType:
			a := u != 0
			f.AppActive = &a
		case n == 2 && t == protowire.BytesType:
			var st uint64
			if err := walk(v, func(n protowire.Number, t protowire.Type, v []byte, u uint64) error {
				switch {
				case n == 1 && t == protowire.VarintType:
					st = u
				case n == 2 && t == protowire.BytesType:
					f.WindowID = string(v)
				}
				return nil
			}); err != nil {
				return err
			}
			// 0 became key, 1 is current while another app is key, 2 resigned key.
			f.WindowKey = st == 0 || st == 1
		case n == 3 && t == protowire.BytesType:
			f.TabID = string(v)
		case n == 4 && t == protowire.BytesType:
			f.SessionID = string(v)
		}
		return nil
	})
	return f, err
}

// FocusResponse: notifications 1 (repeated FocusChangedNotification).
func decodeFocus(b []byte) ([]FocusChange, error) {
	var out []FocusChange
	err := walk(b, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) error {
		if n != 1 || t != protowire.BytesType {
			return nil
		}
		f, err := decodeFocusChange(v)
		if err != nil {
			return err
		}
		out = append(out, f)
		return nil
	})
	return out, err
}

// decodeNotification reads the three notification kinds the client subscribes to and
// skips every other field without decoding it.
func decodeNotification(b []byte) ([]Event, error) {
	var evs []Event
	err := walk(b, func(n protowire.Number, t protowire.Type, v []byte, _ uint64) error {
		if t != protowire.BytesType {
			return nil
		}
		switch n {
		case notifyNewSession, notifyTerminateSession:
			id, err := sessionSummaryID(v) // both carry session_id as field 1
			if err != nil {
				return err
			}
			kind := SessionCreated
			if n == notifyTerminateSession {
				kind = SessionTerminated
			}
			evs = append(evs, Event{Kind: kind, SessionID: id})
		case notifyFocusChange:
			f, err := decodeFocusChange(v)
			if err != nil {
				return err
			}
			evs = append(evs, Event{Kind: FocusChanged, Focus: f})
		}
		return nil
	})
	return evs, err
}

// ValidLabel reports whether a label value is already clean: no C0 or C1 controls (so no ESC
// or BEL), no DEL, no bidirectional controls, valid UTF-8, at most 80 characters. The
// caller cleans labels with termapp.Sanitize; this package only refuses unclean ones.
func ValidLabel(v string) bool {
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 80 {
		return false
	}
	return !strings.ContainsFunc(v, func(r rune) bool {
		return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) ||
			r == 0x061c || r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
	})
}
