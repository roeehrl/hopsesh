package remote

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFindMachine(t *testing.T) {
	ls := []byte(`{"machines": [{"name": "here", "local": true, "status": "ok"},
	  {"name": "box", "status": "ok", "hopsesh": "0.4.5", "presence": "older"}], "groups": []}`)
	m, err := FindMachine(ls, "box")
	if err != nil || m.Presence != "older" || m.Hopsesh != "0.4.5" || m.Local {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := FindMachine(ls, "gone"); err == nil {
		t.Fatal("a missing machine was found")
	}
	if _, err := FindMachine([]byte("hopsesh: failed"), "box"); err == nil {
		t.Fatal("text was read as JSON")
	}
}

func TestFindCheck(t *testing.T) {
	doc := []byte(`[{"name": "box: hopsesh", "ok": true, "detail": "0.5.0 in the login session"}]`)
	c, err := FindCheck(doc, "box: hopsesh")
	if err != nil || !c.OK || !strings.Contains(c.Detail, "login session") {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := FindCheck(doc, "off: ssh + sftp"); err == nil {
		t.Fatal("a missing check was found")
	}
}

func TestLookup(t *testing.T) {
	doc := []byte(`{"plan": {"unprotected": true, "source": {"location": "box"}}}`)
	if v, ok := Lookup(doc, "plan.unprotected"); !ok || v != true {
		t.Fatalf("%v %v", v, ok)
	}
	if v, ok := Lookup(doc, "plan.source.location"); !ok || v != "box" {
		t.Fatalf("%v %v", v, ok)
	}
	for _, p := range []string{"plan.missing", "plan.unprotected.deeper", "result"} {
		if _, ok := Lookup(doc, p); ok {
			t.Errorf("%s was found", p)
		}
	}
}

func TestKeptEndpointPath(t *testing.T) {
	// sha256("box")[:8], as internal/app/endpoint_keeper.go names the folder.
	want := filepath.Join("/state", "machines", "26f8567f25691822", "endpoint-id")
	if got := KeptEndpointPath("/state", "box"); got != want {
		t.Fatalf("%s, want %s", got, want)
	}
	if !ValidEndpoint(strings.Repeat("ab", 32)) || ValidEndpoint("abc") || ValidEndpoint(strings.Repeat("AB", 32)) {
		t.Fatal("endpoint validation")
	}
}

func TestClaudeSession(t *testing.T) {
	if got := ClaudeSlug("/home/hsremote/proj"); got != "-home-hsremote-proj" {
		t.Fatal(got)
	}
	b := string(ClaudeSession("id-1", "/home/u/p", "a \"quoted\" title"))
	if strings.Count(b, "\n") != 3 || !strings.Contains(b, `"customTitle":"a \"quoted\" title"`) || !strings.Contains(b, "/home/u/p/main.go") {
		t.Fatal(b)
	}
}

func TestShQuoteAndCrashed(t *testing.T) {
	if got := ShQuote("it's"); got != `'it'\''s'` {
		t.Fatal(got)
	}
	if !Crashed("panic: runtime error\n\ngoroutine 1 [running]:") || Crashed("hopsesh: off is unreachable") {
		t.Fatal("crash detection")
	}
}
