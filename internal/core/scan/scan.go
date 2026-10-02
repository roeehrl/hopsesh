package scan

import (
	"bufio"
	"io"
	"regexp"
	"sort"
)

// Rule is one secret pattern. The patterns follow the high-confidence rules popularised by
// gitleaks (MIT); they are written independently here and kept deliberately strict to
// avoid flooding users with false positives.
type Rule struct {
	ID string
	re *regexp.Regexp
}

var Rules = []Rule{
	{"anthropic-api-key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`)},
	{"openai-api-key", regexp.MustCompile(`sk-(?:proj|svcacct|admin)-[A-Za-z0-9_\-]{30,}|sk-[A-Za-z0-9]{20}T3BlbkFJ[A-Za-z0-9]{20}`)},
	{"github-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,}`)},
	{"aws-access-key-id", regexp.MustCompile(`\b(?:AKIA|ASIA|ABIA|ACCA)[0-9A-Z]{16}\b`)},
	{"google-api-key", regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`)},
	{"slack-token", regexp.MustCompile(`xox[baprs]-[0-9A-Za-z\-]{10,}`)},
	{"stripe-secret-key", regexp.MustCompile(`(?:sk|rk)_live_[0-9A-Za-z]{24,}`)},
	{"huggingface-token", regexp.MustCompile(`\bhf_[A-Za-z0-9]{34,}\b`)},
	{"private-key", regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )*PRIVATE KEY-----`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`)},
	{"bearer-token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/\-]{24,}=*`)},
}

// Finding is one likely secret. Preview never contains the secret itself.
type Finding struct {
	Rule    string `json:"rule"`
	Line    int    `json:"line"`
	Preview string `json:"preview"`
}

// Summary counts findings by rule.
type Summary struct {
	Total  int            `json:"total"`
	ByRule map[string]int `json:"byRule"`
	// Findings is capped; Total is exact.
	Findings []Finding `json:"findings,omitempty"`
}

const maxFindings = 200

// Reader scans line-oriented content.
func Reader(r io.Reader) (Summary, error) {
	s := Summary{ByRule: map[string]int{}}
	br := bufio.NewReaderSize(r, 1<<20)
	line := 0
	for {
		b, err := br.ReadBytes('\n')
		if len(b) > 0 {
			line++
			for _, f := range scanBytes(b, line) {
				s.Total++
				s.ByRule[f.Rule]++
				if len(s.Findings) < maxFindings {
					s.Findings = append(s.Findings, f)
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return s, err
		}
	}
	return s, nil
}

// Merge adds another summary into s.
func (s *Summary) Merge(o Summary) {
	if s.ByRule == nil {
		s.ByRule = map[string]int{}
	}
	s.Total += o.Total
	for k, v := range o.ByRule {
		s.ByRule[k] += v
	}
	for _, f := range o.Findings {
		if len(s.Findings) < maxFindings {
			s.Findings = append(s.Findings, f)
		}
	}
}

// RuleIDs returns the rule ids with findings, sorted.
func (s Summary) RuleIDs() []string {
	var ids []string
	for k := range s.ByRule {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids
}

func scanBytes(b []byte, line int) []Finding {
	var out []Finding
	for _, r := range Rules {
		for _, m := range r.re.FindAll(b, -1) {
			out = append(out, Finding{Rule: r.ID, Line: line, Preview: mask(m)})
		}
	}
	return out
}

func mask(m []byte) string {
	if len(m) <= 8 {
		return "****"
	}
	return string(m[:4]) + "…" + string(m[len(m)-2:])
}

// Redact replaces every match with [REDACTED:<rule>] and reports how many it replaced.
// It is meant as rewrite.Options.Redact, which only passes it rewritable string tokens.
func Redact(raw []byte) ([]byte, int) {
	n := 0
	for _, r := range Rules {
		raw = r.re.ReplaceAllFunc(raw, func([]byte) []byte {
			n++
			return []byte("[REDACTED:" + r.ID + "]")
		})
	}
	return raw, n
}
