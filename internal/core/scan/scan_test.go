package scan

import (
	"strings"
	"testing"
)

func TestFindsAndMasks(t *testing.T) {
	in := strings.Join([]string{
		`{"stdout":"ANTHROPIC_API_KEY=sk-ant-api03-` + strings.Repeat("a", 40) + `"}`,
		`aws AKIAIOSFODNN7EXAMPLE and ghp_` + strings.Repeat("b", 36),
		`curl -H "Authorization: Bearer ` + strings.Repeat("c", 30) + `"`,
		`-----BEGIN OPENSSH PRIVATE KEY-----`,
		`nothing to see /Users/alice/git/proj sk-not-a-key`,
	}, "\n")
	s, err := Reader(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"anthropic-api-key", "aws-access-key-id", "github-token", "bearer-token", "private-key"} {
		if s.ByRule[rule] != 1 {
			t.Errorf("%s: %d (%v)", rule, s.ByRule[rule], s.ByRule)
		}
	}
	if s.Total != 5 {
		t.Errorf("total %d", s.Total)
	}
	for _, f := range s.Findings {
		if strings.Contains(f.Preview, strings.Repeat("a", 10)) || strings.Contains(f.Preview, "IOSFODNN7") {
			t.Errorf("preview leaks the secret: %q", f.Preview)
		}
	}
}

func TestRedact(t *testing.T) {
	out, n := Redact([]byte(`key=sk-ant-` + strings.Repeat("x", 30) + ` ok`))
	if n != 1 || string(out) != "key=[REDACTED:anthropic-api-key] ok" {
		t.Errorf("%d %q", n, out)
	}
}
