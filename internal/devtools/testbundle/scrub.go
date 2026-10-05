package main

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Contributions hold made-up data only: the people are alice, bob and carol, the
// addresses are documentation ones. scrub reports what looks real. It cannot prove a
// payload is clean (a reviewer still reads it), but it catches what capturing leaves
// behind most often: the capturer's home folder, an e-mail address, a token, a real
// address or machine name.

// madeUpUsers may own a home folder in a payload.
var madeUpUsers = []string{"alice", "bob", "carol", "u", "demo", "user"}

var (
	homeDir = regexp.MustCompile(`(?i)(?:/Users/|/home/|[A-Z]:[\\/]+Users[\\/]+)([^/\\\s"'` + "`" + `]+)`)
	email   = regexp.MustCompile(`[A-Za-z0-9._%+-]+@([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+)`)
	ipv4    = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	secrets = []struct {
		what string
		re   *regexp.Regexp
	}{
		{"an API key", regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{16,}`)},
		{"a GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`)},
		{"a Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
		{"an AWS key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
		{"a Google key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
		{"a JSON web token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.`)},
		{"a private key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY`)},
		{"a bearer token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{16,}`)},
		{"a tailnet name", regexp.MustCompile(`(?i)\b[a-z0-9-]+\.ts\.net\b`)},
	}
	// The documentation and loopback ranges, and 100.64.0.0/24 (CONTRIBUTING's made-up peers).
	madeUpNets = []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("0.0.0.0/32"),
		netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("100.64.0.0/24"),
	}
)

// scrub returns what in a JSON document does not look made up.
func scrub(doc []byte) []string {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return []string{err.Error()}
	}
	found := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			for _, f := range scrubString(x) {
				found[f] = true
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for k, e := range x {
				walk(k)
				walk(e)
			}
		}
	}
	walk(v)
	out := make([]string, 0, len(found))
	for f := range found {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func scrubString(s string) []string {
	var out []string
	for _, m := range homeDir.FindAllStringSubmatch(s, -1) {
		if !slices.Contains(madeUpUsers, strings.ToLower(m[1])) {
			out = append(out, fmt.Sprintf("a home folder of %q: use one of %s", m[1], strings.Join(madeUpUsers, ", ")))
		}
	}
	for _, m := range email.FindAllStringSubmatch(s, -1) {
		d := strings.ToLower(m[1])
		if !slices.Contains([]string{"example.com", "example.org", "example.net"}, d) && !strings.HasSuffix(d, ".example.com") {
			out = append(out, fmt.Sprintf("an e-mail address at %s: use example.com", d))
		}
	}
	for _, m := range ipv4.FindAllString(s, -1) {
		a, err := netip.ParseAddr(m)
		if err != nil {
			continue // not an address ("999.1.1.1")
		}
		if !slices.ContainsFunc(madeUpNets, func(p netip.Prefix) bool { return p.Contains(a) }) {
			out = append(out, fmt.Sprintf("the address %s: use 192.0.2.x or 100.64.0.x", m))
		}
	}
	for _, sc := range secrets {
		if sc.re.MatchString(s) {
			out = append(out, "what looks like "+sc.what)
		}
	}
	return out
}
