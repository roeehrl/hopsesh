package moved

import (
	"encoding/json"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	for _, c := range []struct{ host, title string }{{"laptop", "fix flaky tests"}, {"mini", ""}, {"studio", "a · b"}} {
		h, ti, ok := Parse(Title(c.host, c.title))
		if !ok || h != c.host || ti != c.title {
			t.Errorf("%+v → %q %q %v", c, h, ti, ok)
		}
	}
	for _, s := range []string{"fix flaky tests", "↪ moved to ", "↪ moved to two words · x", ""} {
		if _, _, ok := Parse(s); ok {
			t.Errorf("%q must not parse as moved", s)
		}
	}
	var r map[string]string
	if err := json.Unmarshal(Record("sid", "laptop", "x"), &r); err != nil || r["type"] != "custom-title" || r["customTitle"] != "↪ moved to laptop · x" || r["sessionId"] != "sid" {
		t.Fatalf("record: %v %v", r, err)
	}
}
