package fakeopencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The cases below are the ones a differential run against the real OpenCode 2.3.0 (in an
// isolated sandbox, --standalone) answered; the stand-in must answer them the same way.

// Import decodes the transfer as OpenCode's schema does: a wrong shape is a Bad Request,
// and members the schema does not declare are dropped.
func TestImportShapes(t *testing.T) {
	m := newMachine(t)
	repo := filepath.Join(m.home, "demo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	msg := func(d map[string]any, typ string) map[string]any {
		for _, x := range d["messages"].([]any) {
			if x.(map[string]any)["type"] == typ {
				return x.(map[string]any)
			}
		}
		t.Fatalf("no %s message", typ)
		return nil
	}
	content := func(d map[string]any, typ string) map[string]any {
		for _, x := range msg(d, "assistant")["content"].([]any) {
			if x.(map[string]any)["type"] == typ {
				return x.(map[string]any)
			}
		}
		t.Fatalf("no %s content", typ)
		return nil
	}
	n := 0
	imp := func(edit func(map[string]any)) (string, result) {
		n++
		sid := "ses_e29a3ffe0ffeShapeCase" + strings.Repeat("0", 5-len(itoa(n))) + itoa(n)
		f := m.fixtureFile("export-12.json", func(d map[string]any) {
			d["info"].(map[string]any)["id"] = sid
			for i, x := range d["messages"].([]any) {
				x.(map[string]any)["id"] = "msg_1d65c0000001ShapeCase" + itoa(n) + "x" + itoa(i)
			}
			edit(d)
		})
		return sid, m.run("session", "import", f, "--directory", repo, "--standalone")
	}
	for name, edit := range map[string]func(map[string]any){
		"an assistant without agent":    func(d map[string]any) { delete(msg(d, "assistant"), "agent") },
		"a tool call without state":     func(d map[string]any) { delete(content(d, "tool"), "state") },
		"a tool result without content": func(d map[string]any) { content(d, "tool")["state"].(map[string]any)["content"] = []any{} },
		"an unknown message type":       func(d map[string]any) { msg(d, "user")["type"] = "nap" },
		"a prompt without text":         func(d map[string]any) { delete(msg(d, "user"), "text") },
		"a stored file's media ref": func(d map[string]any) {
			msg(d, "user")["files"] = []any{map[string]any{"uri": "media:" + strings.Repeat("0", 64), "mime": "text/plain", "source": map[string]any{"type": "inline"}}}
		},
		"an unknown finish":  func(d map[string]any) { msg(d, "assistant")["finish"] = "done" },
		"a cost in a string": func(d map[string]any) { d["info"].(map[string]any)["cost"] = "0" },
		"a permission of unknown effect": func(d map[string]any) {
			d["info"].(map[string]any)["permissions"] = []any{map[string]any{"action": "read", "resource": "*", "effect": "maybe"}}
		},
		"an error status of 99": func(d map[string]any) {
			msg(d, "assistant")["error"] = map[string]any{"type": "x", "message": "m", "status": 99}
		},
	} {
		if _, r := imp(edit); r.code != 1 || r.stderr != "Error: Failed to import session: Bad Request\n" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	sid, r := imp(func(d map[string]any) {
		d["info"].(map[string]any)["hopsesh"] = map[string]any{"x": 1}
		msg(d, "user")["unknownMember"] = "kept?"
		msg(d, "user")["time"].(map[string]any)["completed"] = 1794598001500
		msg(d, "user")["time"].(map[string]any)["created"] = 1794598001000.5
		content(d, "text")["extra"] = true
		content(d, "tool")["state"].(map[string]any)["extra"] = 1
		msg(d, "user")["metadata"] = map[string]any{"kept": map[string]any{"as": "is"}}
	})
	if r.code != 0 {
		t.Fatalf("unknown members: %+v", r)
	}
	exp := m.must("session", "export", sid, "--standalone")
	for _, gone := range []string{"hopsesh", "unknownMember", "1794598001500", "extra", "1794598001000.5"} {
		if strings.Contains(exp, gone) {
			t.Errorf("the export keeps %s:\n%s", gone, exp)
		}
	}
	if !strings.Contains(exp, `"kept": {`) || !strings.Contains(exp, `"created": 1794598001000`) {
		t.Errorf("the export lost a record or a time:\n%s", exp)
	}

	b, err := os.ReadFile(filepath.Join(fixture, "export-12.json"))
	if err != nil {
		t.Fatal(err)
	}
	if dropped, err := CheckTransfer(b); err != nil || len(dropped) > 0 {
		t.Errorf("OpenCode's own export: dropped %q, %v", dropped, err)
	}
	var d map[string]any
	_ = json.Unmarshal(b, &d)
	d["messages"].([]any)[0].(map[string]any)["extra"] = 1
	b, _ = json.Marshal(d)
	if dropped, err := CheckTransfer(b); err != nil || !slices.Equal(dropped, []string{"messages[0].extra"}) {
		t.Errorf("an unknown member: dropped %q, %v", dropped, err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// sandboxMachine is a machine whose database holds the sandbox's rows.
func sandboxMachine(t *testing.T) *machine {
	m := newMachine(t)
	m.env["OPENCODE_DB"] = fixtureDB(t)
	m.dir = gitRepo(t, "https://example.com/u/demo.git")
	return m
}

type infoPage struct {
	Data struct {
		ID    string  `json:"id"`
		Title *string `json:"title"`
		Fork  struct {
			Boundary struct{ Type, MessageID string }
		} `json:"fork"`
		Metadata map[string]any `json:"metadata"`
		Time     map[string]any `json:"time"`
	} `json:"data"`
}

func (m *machine) info(args ...string) infoPage {
	m.t.Helper()
	var p infoPage
	if err := json.Unmarshal([]byte(m.must(args...)), &p); err != nil {
		m.t.Fatal(err)
	}
	return p
}

// A fork is titled as OpenCode's projector titles it, keeps the parent's seq in its
// message ids, and refuses a boundary that is not there as OpenCode does.
func TestFork(t *testing.T) {
	m := sandboxMachine(t)
	const parent, aFork, partRows = "ses_e29a88c86ffeoU3BQ4jmST24T2", "ses_e29a69ba5ffeH6q8sHMV3Anqbz", "ses_e29a5465fffek5VRD43HVvlx4P"
	// "file tools" has two forks already, both "(fork #1)".
	if f := m.info("api", "--standalone", "POST", "/api/session/"+parent+"/fork", "-d", "{}"); f.Data.Title == nil || *f.Data.Title != "file tools (fork #1)" {
		t.Errorf("a third fork: %+v", f)
	}
	if f := m.info("api", "--standalone", "POST", "/api/session/"+aFork+"/fork", "-d", "{}"); f.Data.Title == nil || *f.Data.Title != "file tools (fork #2)" {
		t.Errorf("a fork of a fork: %+v", f)
	}
	f := m.info("api", "--standalone", "POST", "/api/session/"+partRows+"/fork", "-d", "{}")
	if f.Data.Fork.Boundary.Type != "through" || f.Data.Fork.Boundary.MessageID != "msg_1d65ace33001Hez3PtcUKBo7QL" {
		t.Errorf("the boundary: %+v", f.Data.Fork)
	}
	var exp struct{ Messages []struct{ ID string } }
	if err := json.Unmarshal([]byte(m.must("session", "export", f.Data.ID, "--standalone")), &exp); err != nil {
		t.Fatal(err)
	}
	var seqs []string
	for _, x := range exp.Messages {
		seqs = append(seqs, x.ID[strings.LastIndex(x.ID, "_")+1:])
	}
	// The sandbox's real fork of this session numbered them so.
	if want := []string{"5", "6", "18", "26", "29", "30", "38"}; !slices.Equal(seqs, want) {
		t.Errorf("the fork's message seqs %q, want %q", seqs, want)
	}
	r := m.run("api", "--standalone", "POST", "/api/session/"+parent+"/fork", "-d", `{"before":"msg_1d65c0000001NoSuchMsg0001"}`)
	if r.code != 1 || r.stderr != "HTTP 404 Not Found\n" || !strings.HasPrefix(r.stdout, `{"_tag":"MessageNotFoundError","sessionID":"`+parent+`","messageID":"msg_1d65c0000001NoSuchMsg0001"`) {
		t.Errorf("a missing boundary: %+v", r)
	}
	if r := m.run("api", "--standalone", "POST", "/api/session/"+parent+"/fork", "-d", `{"before":"x"}`); r.code != 1 || !strings.Contains(r.stdout, `"kind":"Payload"`) {
		t.Errorf("a boundary that is not a message id: %+v", r)
	}
	// An untitled session's fork has no title; metadata and permissions come along.
	sid := "ses_e29a40000ffeUntitledCopy00"
	m.must("session", "import", m.fixtureFile("export-13.json", func(d map[string]any) {
		in := d["info"].(map[string]any)
		in["id"] = sid
		for i, x := range d["messages"].([]any) {
			x.(map[string]any)["id"] = "msg_1d65c0000001UntitledCp0" + itoa(i)
		}
		delete(in, "title")
		in["metadata"] = map[string]any{"k": "v"}
	}), "--standalone")
	if f := m.info("api", "--standalone", "POST", "/api/session/"+sid+"/fork", "-d", "{}"); f.Data.Title != nil || f.Data.Metadata["k"] != "v" {
		t.Errorf("the fork of an untitled session: %+v", f)
	}
}

// PATCH archives and restores, sets permissions; the list filters by project and
// subpath; the message pages default to 50, and a cursor keeps its own order.
func TestPatchListAndPages(t *testing.T) {
	m := sandboxMachine(t)
	sub := filepath.Join(m.dir, "sub", "deeper")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	sid := "ses_e29a3ffe0ffeHandMadeViaCli0"
	m.must("session", "delete", sid, "--standalone")
	m.must("session", "import", m.fixtureFile("export-12.json", nil), "--directory", sub, "--standalone")
	m.must("api", "--standalone", "PATCH", "/api/session/"+sid, "-d", `{"archived":true,"permissions":[{"action":"edit","resource":"*","effect":"deny"}]}`)
	got := m.must("api", "--standalone", "GET", "/api/session/"+sid)
	if !strings.Contains(got, `"archived":`) || !strings.Contains(got, `"subpath":"sub/deeper","permissions":[{"action":"edit","resource":"*","effect":"deny"}],"location":`) {
		t.Errorf("after archiving: %s", got)
	}
	m.must("api", "--standalone", "PATCH", "/api/session/"+sid, "-d", `{"archived":false}`)
	if got := m.must("api", "--standalone", "GET", "/api/session/"+sid); strings.Contains(got, `"archived":`) {
		t.Errorf("after restoring: %s", got)
	}
	if r := m.run("api", "--standalone", "PATCH", "/api/session/"+sid, "-d", `{"permissions":[{"action":"edit"}]}`); r.code != 1 ||
		r.stdout != `{"_tag":"InvalidRequestError","message":"Missing key\n  at [\"permissions\"][0][\"resource\"]","kind":"Payload"}`+"\n" {
		t.Errorf("a rule without resource: %+v", r)
	}
	ids := func(path string) []string {
		var p struct{ Data []struct{ ID string } }
		if err := json.Unmarshal([]byte(m.must("api", "--standalone", "GET", path)), &p); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range p.Data {
			out = append(out, s.ID)
		}
		return out
	}
	if got := ids("/api/session?project=" + demoProject + "&subpath=sub/deeper"); !slices.Equal(got, []string{sid}) {
		t.Errorf("by project and subpath: %q", got)
	}
	if got := ids("/api/session?project=0000000000000000000000000000000000000000"); len(got) != 0 {
		t.Errorf("another project: %q", got)
	}
	if got := m.must("api", "--standalone", "GET", "/api/session?project=0000000000000000000000000000000000000000"); got != `{"data":[],"cursor":{"previous":null,"next":null}}`+"\n" {
		t.Errorf("an empty page: %s", got)
	}
	const long = "ses_e29a7bdf0ffefEVrCCay4aaALo"
	if got := ids("/api/session/" + long + "/message"); len(got) != 24 {
		t.Errorf("all of a session's 24 messages: %d", len(got))
	}
	var page struct {
		Cursor struct{ Next string } `json:"cursor"`
	}
	if err := json.Unmarshal([]byte(m.must("api", "--standalone", "GET", "/api/session/"+long+"/message?limit=2")), &page); err != nil {
		t.Fatal(err)
	}
	if r := m.run("api", "--standalone", "GET", "/api/session/"+long+"/message?limit=2&order=asc&cursor="+page.Cursor.Next); r.code != 1 ||
		r.stdout != `{"_tag":"InvalidCursorError","message":"Cursor cannot be combined with order"}`+"\n" {
		t.Errorf("a cursor with an order: %+v", r)
	}
	if r := m.run("api", "--standalone", "GET", "/api/session/"+long+"/message?cursor=abc"); r.code != 1 || !strings.HasPrefix(r.stdout, `{"_tag":"InvalidCursorError"`) {
		t.Errorf("a bad cursor: %+v", r)
	}
	if r := m.run("api", "--standalone", "GET", "/api/session/abc"); r.code != 1 || !strings.Contains(r.stdout, `"kind":"Params"`) {
		t.Errorf("a bad session id: %+v", r)
	}
	// A private server takes a move and exits before making it: the session stays where
	// it was, marked busy with no idle.
	before := m.must("api", "--standalone", "GET", "/api/session/"+sid)
	m.must("api", "--standalone", "POST", "/api/session/"+sid+"/move", "-d", `{"directory":".."}`)
	if got := m.must("api", "--standalone", "GET", "/api/session/"+sid); !strings.Contains(got, `"subpath":"sub/deeper"`) || !strings.Contains(got, `"busy":`) || strings.Contains(before, `"busy":`) {
		t.Errorf("a move on a private server: %s", got)
	}
	// The background service makes it. A relative folder is the session's, a link is not
	// resolved in the subpath.
	m.env["FAKE_OPENCODE_SERVICE"] = "running"
	m.must("api", "POST", "/api/session/"+sid+"/move", "-d", `{"directory":".."}`)
	if got := m.must("api", "GET", "/api/session/"+sid); !strings.Contains(got, `"subpath":"sub","permissions"`) {
		t.Errorf("after a relative move: %s", got)
	}
	link := filepath.Join(m.home, "link")
	if err := os.Symlink(m.dir, link); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{"directory": link})
	m.must("api", "POST", "/api/session/"+sid+"/move", "-d", string(b))
	if want, _ := filepath.Rel(m.dir, link); !strings.Contains(m.must("api", "GET", "/api/session/"+sid), `"subpath":"`+filepath.ToSlash(want)+`"`) {
		t.Errorf("a folder through a link: want subpath %s", want)
	}
}

// Without ?limit a page holds 50 messages.
func TestMessagesDefaultLimit(t *testing.T) {
	m := newMachine(t)
	var msgs []string
	for i := range 60 {
		msgs = append(msgs, `{"id":"msg_1d65c0000001ManyMessages`+itoa(100+i)+`","type":"user","time":{"created":`+itoa(1794598000000+i)+`},"text":"hi"}`)
	}
	doc := `{"info":{"id":"ses_e29a40000ffeManyMessages00","projectID":"global","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1,"updated":1},"location":{"directory":"/"}},"messages":[` + strings.Join(msgs, ",") + `]}`
	f := filepath.Join(m.home, "many.json")
	if err := os.WriteFile(f, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	m.must("session", "import", f, "--standalone")
	var p struct{ Data []any }
	if err := json.Unmarshal([]byte(m.must("api", "--standalone", "GET", "/api/session/ses_e29a40000ffeManyMessages00/message")), &p); err != nil || len(p.Data) != 50 {
		t.Errorf("a page of %d messages, %v", len(p.Data), err)
	}
}

// The TUI talks to the background service: it starts it, or replaces one of another
// version.
func TestTUIStartsTheService(t *testing.T) {
	m := newMachine(t)
	m.must("-s", "ses_e29a40000ffeHandMadeImport")
	if !strings.Contains(m.log(), "opencode: started the background service") {
		t.Error("the TUI did not start the service")
	}
	m = newMachine(t)
	m.env["FAKE_OPENCODE_SERVICE"] = "running"
	m.env["FAKE_OPENCODE_SERVICE_VERSION"] = "2.2.7"
	m.must("-s", "ses_e29a40000ffeHandMadeImport")
	if !strings.Contains(m.log(), "opencode: replaced the background service 2.2.7 with 2.3.0") {
		t.Error("the TUI did not replace a service of another version")
	}
}
