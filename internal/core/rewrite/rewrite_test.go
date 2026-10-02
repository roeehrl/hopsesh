package rewrite

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// protect is a Claude-like policy (the core knows no agent; this is test data).
var protect = agent.RewritePolicy{Protect: []string{"thinking", "signature", "data", "uuid", "parentUuid", "sessionId"}}

func run(t *testing.T, in string, opt Options) (string, Stats) {
	t.Helper()
	var out bytes.Buffer
	st, err := JSONL(strings.NewReader(in), &out, opt)
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), st
}

var macToMac = []Mapping{
	{From: "/Users/alice/git/proj", To: "/Users/bob/src/proj"},
	{From: "/Users/alice/.claude", To: "/Users/bob/.claude"},
	{From: "/Users/alice", To: "/Users/bob"},
}

func TestRewritesCwdToolInputsAndKeys(t *testing.T) {
	in := `{"type":"user","cwd":"/Users/alice/git/proj","uuid":"u1","message":{"content":[{"type":"tool_use","input":{"file_path":"/Users/alice/git/proj/a.go","command":"cd /Users/alice/git/proj && ls ~/x"}}]}}` + "\n" +
		`{"type":"file-history-snapshot","snapshot":{"trackedFileBackups":{"/Users/alice/git/proj/b.go":{"realParentDir":"/Users/alice/git/proj"}}}}` + "\n" +
		`{"type":"user","toolUseResult":{"persistedOutputPath":"/Users/alice/.claude/projects/-Users-alice-git-proj/s/tool-results/x.txt"}}` + "\n"
	out, st := run(t, in, Options{Mappings: macToMac, Policy: protect})
	for _, want := range []string{
		`"cwd":"/Users/bob/src/proj"`,
		`"file_path":"/Users/bob/src/proj/a.go"`,
		`cd /Users/bob/src/proj && ls ~/x`,
		`"/Users/bob/src/proj/b.go":{"realParentDir":"/Users/bob/src/proj"}`, // keys are paths too
		`"/Users/bob/.claude/projects/-Users-alice-git-proj/s/tool-results/x.txt"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if st.Replacements["/Users/alice/git/proj"] != 5 || st.Lines != 3 || st.LinesChanged != 3 {
		t.Errorf("stats %+v", st)
	}
}

func TestBoundariesAndNoDoubleReplace(t *testing.T) {
	in := `{"a":"/Users/alice/git/proj2/x","b":"/data/Users/alice/x","c":"/Users/alicex","d":"file:///Users/alice/y","e":"/Users/alice"}` + "\n"
	maps := []Mapping{{From: "/Users/alice", To: "/Users/alice/nested"}, {From: "/Users/alice/git/proj", To: "/p"}}
	out, _ := run(t, in, Options{Mappings: maps})
	want := `{"a":"/Users/alice/nested/git/proj2/x","b":"/data/Users/alice/x","c":"/Users/alicex","d":"file:///Users/alice/nested/y","e":"/Users/alice/nested"}` + "\n"
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

func TestThinkingSignatureAndIDsUntouched(t *testing.T) {
	in := `{"type":"assistant","uuid":"/Users/alice","message":{"content":[{"type":"thinking","thinking":"look in /Users/alice/git/proj","signature":"sig/Users/alice"},{"type":"redacted_thinking","data":{"x":"/Users/alice"}},{"type":"text","text":"see /Users/alice/git/proj"}]}}` + "\n"
	out, _ := run(t, in, Options{Mappings: macToMac, Policy: protect})
	if !strings.Contains(out, `"thinking":"look in /Users/alice/git/proj"`) || !strings.Contains(out, `"signature":"sig/Users/alice"`) ||
		!strings.Contains(out, `"data":{"x":"/Users/alice"}`) || !strings.Contains(out, `"uuid":"/Users/alice"`) {
		t.Errorf("protected values changed:\n%s", out)
	}
	if !strings.Contains(out, `"text":"see /Users/bob/src/proj"`) {
		t.Errorf("text not rewritten:\n%s", out)
	}
}

func TestUnchangedLinesAreByteIdentical(t *testing.T) {
	in := `{"type":"x",  "spaced" : "keep   this",  "n": 1.50, "u":"\u00e9\/"}` + "\r\n" + `not json` + "\n" + `{"last":"no newline"}`
	out, st := run(t, in, Options{Mappings: macToMac, Policy: protect})
	if out != `{"type":"x",  "spaced" : "keep   this",  "n": 1.50, "u":"\u00e9\/"}`+"\n"+`not json`+"\n"+`{"last":"no newline"}` {
		t.Errorf("bytes changed:\n%q", out)
	}
	if st.LinesChanged != 0 {
		t.Errorf("changed %d", st.LinesChanged)
	}
}

func TestWindowsEscapedPaths(t *testing.T) {
	maps := []Mapping{{From: `C:\Users\alice\proj`, To: "/home/bob/proj"}, {From: "/Users/alice/proj", To: `D:\work\proj`}}
	in := `{"cwd":"C:\\Users\\alice\\proj","f":"C:\\Users\\alice\\proj\\src\\a.go","g":"/Users/alice/proj/b"}` + "\n"
	out, _ := run(t, in, Options{Mappings: maps})
	want := `{"cwd":"/home/bob/proj","f":"/home/bob/proj\\src\\a.go","g":"D:\\work\\proj/b"}` + "\n"
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(out), &v); err != nil || v["g"] != `D:\work\proj/b` {
		t.Errorf("result must stay valid JSON: %v %v", err, v)
	}
}

func TestWindowsSeparatorTranslation(t *testing.T) {
	// Windows → Linux: separators below the mapped folder become slashes; a JSON escape
	// (\n) or a space ends the path.
	toUnix := []Mapping{{From: `C:\Users\alice\proj`, To: "/home/bob/proj", ToSep: "/"}}
	in := `{"f":"C:\\Users\\alice\\proj\\src\\a.go","o":"see C:\\Users\\alice\\proj\\x y\nC:\\Users\\alice\\proj\\z"}` + "\n"
	out, _ := run(t, in, Options{Mappings: toUnix})
	want := `{"f":"/home/bob/proj/src/a.go","o":"see /home/bob/proj/x y\n/home/bob/proj/z"}` + "\n"
	if out != want {
		t.Errorf("to unix:\ngot  %s\nwant %s", out, want)
	}
	// macOS → Windows.
	toWin := []Mapping{{From: "/Users/alice/proj", To: `D:\work\proj`, ToSep: `\`}}
	out, _ = run(t, `{"f":"/Users/alice/proj/src/a.go","u":"/Users/alice/projects/x"}`+"\n", Options{Mappings: toWin})
	want = `{"f":"D:\\work\\proj\\src\\a.go","u":"/Users/alice/projects/x"}` + "\n"
	if out != want {
		t.Errorf("to windows:\ngot  %s\nwant %s", out, want)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(out), &v); err != nil || v["f"] != `D:\work\proj\src\a.go` {
		t.Errorf("must stay valid JSON: %v %v", err, v)
	}
	// Plain text (tool results).
	var b strings.Builder
	if _, err := Text(strings.NewReader(`C:\Users\alice\proj\a\b.txt done`), &b, toUnix); err != nil || b.String() != "/home/bob/proj/a/b.txt done" {
		t.Errorf("text: %q %v", b.String(), err)
	}
}

func TestDropMovedMarks(t *testing.T) {
	in := `{"type":"custom-title","customTitle":"fix tests","sessionId":"s"}` + "\n" +
		`{"type":"custom-title","customTitle":"↪ moved to laptop · fix tests","sessionId":"s"}` + "\n"
	out, st := run(t, in, Options{Policy: agent.RewritePolicy{DropRecords: []agent.FieldMatch{{Field: "customTitle", Prefixes: agent.MarkPrefixes()}}}})
	if st.DroppedRecords != 1 || strings.Contains(out, "moved to") || !strings.Contains(out, `"fix tests"`) {
		t.Fatalf("got %q %+v", out, st)
	}
	out, _ = run(t, in, Options{})
	if !strings.Contains(out, "moved to") {
		t.Fatal("marks are kept unless asked")
	}
}

func TestRenameSession(t *testing.T) {
	old, nw := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	in := `{"type":"user","sessionId":"` + old + `","uuid":"` + old + `","toolUseResult":{"persistedOutputPath":"/Users/alice/.claude/projects/p/` + old + `/tool-results/x.txt"},"message":{"content":[{"type":"thinking","thinking":"see ` + old + `","signature":"s"}]}}` + "\n"
	pol := protect
	pol.Rename, pol.RenameKeys = [2]string{old, nw}, []string{"sessionId"}
	out, st := run(t, in, Options{Policy: pol})
	if !strings.Contains(out, `"sessionId":"`+nw+`"`) || !strings.Contains(out, "/"+nw+"/tool-results") {
		t.Fatalf("not renamed: %s", out)
	}
	if !strings.Contains(out, `"uuid":"`+old+`"`) || !strings.Contains(out, `"thinking":"see `+old+`"`) {
		t.Fatalf("message ids and signed thinking must not change: %s", out)
	}
	if st.RenamedIDs != 2 {
		t.Fatalf("renamed=%d\n%s", st.RenamedIDs, out)
	}
}

func TestDropRecordsAndElements(t *testing.T) {
	in := `{"type":"bridge-session","bridgeSessionId":"b1"}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"s"},{"type":"text","text":"hi"}]},"z":1}` + "\n"
	pol := agent.RewritePolicy{
		DropRecords: []agent.FieldMatch{{Field: "type", Values: []string{"bridge-session"}}},
		DropElems:   []agent.ElemMatch{{Array: "message.content", Field: "type", Values: []string{"thinking", "redacted_thinking"}}},
	}
	out, st := run(t, in, Options{Policy: pol})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 || st.DroppedRecords != 1 || st.DroppedElems != 1 {
		t.Fatalf("stats %+v\n%s", st, out)
	}
	if lines[0] != `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]},"z":1}` {
		t.Errorf("thinking drop must keep key order: %s", lines[0])
	}
}

func TestRedactHook(t *testing.T) {
	in := `{"t":"key sk-ant-123","thinking":"sk-ant-123"}` + "\n"
	red := func(b []byte) ([]byte, int) {
		n := bytes.Count(b, []byte("sk-ant-123"))
		return bytes.ReplaceAll(b, []byte("sk-ant-123"), []byte("[REDACTED]")), n
	}
	out, st := run(t, in, Options{Redact: red, Policy: protect})
	if out != `{"t":"key [REDACTED]","thinking":"sk-ant-123"}`+"\n" || st.Redactions != 1 {
		t.Errorf("redact: %s %+v", out, st)
	}
}

func TestTextFiles(t *testing.T) {
	var out bytes.Buffer
	n, err := Text(strings.NewReader("ran in /Users/alice/git/proj/x and C:\\other"), &out, macToMac)
	if err != nil || n != 1 || out.String() != "ran in /Users/bob/src/proj/x and C:\\other" {
		t.Errorf("%d %v %q", n, err, out.String())
	}
}

func FuzzRewriteKeepsValidJSON(f *testing.F) {
	f.Add(`{"a":"/Users/alice/git/proj","b":[1,{"thinking":"x"}]}`)
	f.Add(`{"k\"ey":"\\Users\\alice","n":null}`)
	f.Add(`{"p":"C:\\Users\\alice\\x\\y\nz","q":"/Users/alice/a/b c"}`)
	f.Fuzz(func(t *testing.T, s string) {
		if !json.Valid([]byte(s)) || strings.ContainsAny(s, "\r\n") {
			return
		}
		cross := []Mapping{{From: `C:\Users\alice`, To: "/home/bob", ToSep: "/"}, {From: "/Users/alice", To: `D:\bob`, ToSep: `\`}}
		for _, maps := range [][]Mapping{macToMac, cross} {
			var out bytes.Buffer
			if _, err := JSONL(strings.NewReader(s+"\n"), &out, Options{Mappings: maps}); err != nil {
				t.Fatal(err)
			}
			if !json.Valid(bytes.TrimSpace(out.Bytes())) {
				t.Fatalf("invalid JSON after rewrite: %q -> %q", s, out.String())
			}
		}
	})
}

func TestPathsAfterJSONEscapes(t *testing.T) {
	in := `{"o":"line\n/Users/alice/git/proj/x\t/Users/alice/y\u0027/Users/alice/z","w":"C:\\nope/Users/alice/q"}` + "\n"
	out, _ := run(t, in, Options{Mappings: macToMac, Policy: protect})
	want := `{"o":"line\n/Users/bob/src/proj/x\t/Users/bob/y\u0027/Users/bob/z","w":"C:\\nope/Users/alice/q"}` + "\n"
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

func TestDropRecordsByNestedField(t *testing.T) {
	in := `{"type":"response_item","payload":{"type":"message","role":"user"}}
{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"gAAA"}}
{"type":"compacted","payload":{"message":""}}
`
	out, st := run(t, in, Options{Policy: agent.RewritePolicy{DropRecords: []agent.FieldMatch{
		{Field: "payload.type", Values: []string{"reasoning"}}, {Field: "type", Values: []string{"compacted"}}}}})
	if st.DroppedRecords != 2 || strings.Contains(out, "reasoning") || strings.Contains(out, "compacted") || !strings.Contains(out, `"role":"user"`) {
		t.Fatalf("dropped %d:\n%s", st.DroppedRecords, out)
	}
}
