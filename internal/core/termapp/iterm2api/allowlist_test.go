package iterm2api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// Rule 13 of the iTerm2 design: the client can encode only the requests hopsesh needs.
// Typing into a session, injecting bytes, reading a screen, buffer, selection or prompt,
// screenshots and keystroke monitors are not in this package, and these tests fail if any
// of them, or any other request, is added.

// The complete set of requests (by envelope field number) the client may send.
var allowedRequests = map[protowire.Number]string{
	103: "NotificationRequest (new session, terminate session, focus change only)",
	106: "ListSessionsRequest",
	108: "CreateTabRequest",
	109: "SplitPaneRequest",
	114: "ActivateRequest",
	115: "VariableRequest (get tty, set user.hopsesh_*)",
	117: "FocusRequest",
}

// Requests that must never exist here, named for the error message.
var forbiddenRequests = map[protowire.Number]string{
	100: "GetBufferRequest (screen contents)",
	101: "GetPromptRequest (prompt and command text)",
	102: "TransactionRequest",
	104: "RegisterToolRequest",
	105: "SetProfilePropertyRequest",
	107: "SendTextRequest (typing)",
	110: "GetProfilePropertyRequest",
	111: "SetPropertyRequest",
	112: "GetPropertyRequest",
	113: "InjectRequest (fake program output)",
	116: "SavedArrangementRequest",
	118: "ListProfilesRequest",
	119: "ServerOriginatedRPCResultRequest",
	120: "RestartSessionRequest",
	121: "MenuItemRequest",
	122: "SetTabLayoutRequest",
	124: "TmuxRequest",
	126: "PreferencesRequest",
	128: "SelectionRequest (selected text)",
	131: "CloseRequest",
	132: "InvokeFunctionRequest",
	133: "ListPromptsRequest",
	134: "ScreenshotRequest",
}

// packageFiles parses the package's non-test sources.
func packageFiles(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) < 4 {
		t.Fatalf("found only %d source files", len(files))
	}
	return fset, files
}

// TestOnlyAllowlistedRequestTypes reads the source: every type with a field() method (the
// request interface) must return one of the allowlisted field constants, and those
// constants must have the allowlisted numbers.
func TestOnlyAllowlistedRequestTypes(t *testing.T) {
	_, files := packageFiles(t)
	consts := map[string]string{} // name → literal value
	var returned []string
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, sp := range d.Specs {
					vs := sp.(*ast.ValueSpec)
					for i, n := range vs.Names {
						if strings.HasPrefix(n.Name, "field") && i < len(vs.Values) {
							if lit, ok := vs.Values[i].(*ast.BasicLit); ok {
								consts[n.Name] = lit.Value
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil || d.Name.Name != "field" {
					continue
				}
				ast.Inspect(d.Body, func(n ast.Node) bool {
					r, ok := n.(*ast.ReturnStmt)
					if !ok {
						return true
					}
					for _, res := range r.Results {
						id, ok := res.(*ast.Ident)
						if !ok {
							t.Errorf("a request's field() returns a non-constant expression")
							continue
						}
						returned = append(returned, id.Name)
					}
					return true
				})
			}
		}
	}
	if len(returned) != len(allowedRequests) {
		t.Errorf("request types = %d (%v), want %d", len(returned), returned, len(allowedRequests))
	}
	seen := map[string]bool{}
	for _, name := range returned {
		if seen[name] {
			t.Errorf("two request types use %s", name)
		}
		seen[name] = true
		lit, ok := consts[name]
		if !ok {
			t.Errorf("field() returns %s, which is not a field constant", name)
			continue
		}
		var num int
		for _, ch := range lit {
			num = num*10 + int(ch-'0')
		}
		n := protowire.Number(num)
		if bad, ok := forbiddenRequests[n]; ok {
			t.Errorf("%s = %d is %s: forbidden", name, n, bad)
		} else if _, ok := allowedRequests[n]; !ok {
			t.Errorf("%s = %d is not on the allowlist", name, n)
		}
	}
}

// TestEncodedRequestsStayInTheAllowlist encodes every request type and checks the field
// number on the wire.
func TestEncodedRequestsStayInTheAllowlist(t *testing.T) {
	props, err := launchProperties("true", "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	get, err := newVariableGet("s", "tty")
	if err != nil {
		t.Fatal(err)
	}
	set, err := newVariableSet("s", map[string]string{"title": "x"})
	if err != nil {
		t.Fatal(err)
	}
	reqs := []request{
		listSessionsRequest{},
		focusRequest{},
		createTabRequest{windowID: "w", props: props},
		splitPaneRequest{session: "s", props: props},
		activateRequest{session: "s"},
		get, set,
	}
	for _, k := range []uint64{notifyNewSession, notifyTerminateSession, notifyFocusChange} {
		r, err := newNotificationRequest(k)
		if err != nil {
			t.Fatal(err)
		}
		reqs = append(reqs, r)
	}
	for _, r := range reqs {
		msg := encodeClientMessage(1, r)
		var fields []protowire.Number
		if err := walk(msg, func(n protowire.Number, typ protowire.Type, v []byte, _ uint64) error {
			if typ == protowire.BytesType {
				fields = append(fields, n)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 1 {
			t.Fatalf("%T: envelope fields %v", r, fields)
		}
		if _, ok := allowedRequests[fields[0]]; !ok {
			t.Errorf("%T encodes request %d", r, fields[0])
		}
	}
}

// TestNoForbiddenIdentifiers scans identifiers (not comments) for the names of the
// capabilities the client must not have.
func TestNoForbiddenIdentifiers(t *testing.T) {
	_, files := packageFiles(t)
	bad := []string{"sendtext", "send_text", "inject", "getbuffer", "screen", "keystroke",
		"selection", "screenshot", "prompt", "invokefunction", "registertool", "restartsession",
		"menuitem", "preference", "setprofileproperty", "transaction", "contents"}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			low := strings.ToLower(id.Name)
			for _, b := range bad {
				if strings.Contains(low, b) {
					t.Errorf("identifier %s mentions %q", id.Name, b)
				}
			}
			return true
		})
		// String literals too: a request could be smuggled in as raw bytes.
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			low := strings.ToLower(lit.Value)
			for _, b := range []string{"initial text", "send text", "inject"} {
				if strings.Contains(low, b) {
					t.Errorf("string %s mentions %q", lit.Value, b)
				}
			}
			return true
		})
	}
}

func TestNotificationAllowlist(t *testing.T) {
	for k := uint64(0); k <= 20; k++ {
		_, err := newNotificationRequest(k)
		ok := k == notifyNewSession || k == notifyTerminateSession || k == notifyFocusChange
		if (err == nil) != ok {
			t.Errorf("notification type %d: allowed=%v, err=%v", k, ok, err)
		}
	}
	// The subscription never carries monitor arguments (keystroke, variable, prompt, RPC).
	r, _ := newNotificationRequest(notifyTerminateSession)
	_ = walk(r.encode(), func(n protowire.Number, _ protowire.Type, _ []byte, _ uint64) error {
		if n > 3 {
			t.Errorf("subscription has field %d", n)
		}
		return nil
	})
}

func TestVariableAllowlist(t *testing.T) {
	for _, name := range []string{"selection", "commandLine", "lastCommand", "jobName", "path", "user.x", "*", "session.tty"} {
		if _, err := newVariableGet("s", name); err == nil {
			t.Errorf("reading %q is allowed", name)
		}
	}
	if _, err := newVariableGet("all", "tty"); err == nil {
		t.Error("reading from all sessions is allowed")
	}
	r, err := newVariableSet("s", map[string]string{"b": "2", "a": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.gets) != 0 || len(r.sets) != 2 || r.sets[0][0] != "user.hopsesh_a" || r.sets[1][0] != "user.hopsesh_b" {
		t.Fatalf("sets = %v", r.sets)
	}
	for _, k := range []string{"", "A", "a.b", "a-b", strings.Repeat("a", 33)} {
		if _, err := newVariableSet("s", map[string]string{k: "v"}); err == nil {
			t.Errorf("label name %q accepted", k)
		}
	}
}

func TestProfileOverrideAllowlist(t *testing.T) {
	props, err := launchProperties("cmd", "/d")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, p := range props {
		keys = append(keys, p.key)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"Command", "Custom Command", "Custom Directory", "Working Directory"}) {
		t.Fatalf("keys = %v", keys)
	}
	if allowedProfileKeys["Initial Text"] {
		t.Fatal("Initial Text (typed into the session) is allowed")
	}
	if len(allowedProfileKeys) != 4 {
		t.Fatalf("profile keys = %v", allowedProfileKeys)
	}
	if props, _ := launchProperties("", ""); len(props) != 0 {
		t.Fatalf("empty launch sets %v", props)
	}
}

// notificationMessage builds a ServerOriginatedMessage carrying a Notification whose field
// f holds a message with a string in its field 1.
func notificationMessage(f int, s string) []byte {
	inner := appendString(nil, 1, s)
	n := appendBytes(nil, protowire.Number(f), inner)
	return appendBytes(nil, envNotification, n)
}

func TestUnrequestedNotificationsAreSkipped(t *testing.T) {
	for f := 1; f <= 13; f++ {
		evs, err := decodeNotification(appendBytes(nil, protowire.Number(f), appendString(nil, 1, "x")))
		if err != nil {
			t.Fatal(err)
		}
		want := f == notifyNewSession || f == notifyTerminateSession || f == notifyFocusChange
		if (len(evs) > 0) != want {
			t.Errorf("notification field %d decoded=%v", f, len(evs) > 0)
		}
	}
}

func TestNoticeExists(t *testing.T) {
	b, err := os.ReadFile("NOTICE.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "GPL") {
		t.Fatal("NOTICE.md does not explain the licence decision")
	}
}
