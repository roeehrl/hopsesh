package agent

import (
	"net/url"
	"strings"
)

// LinksIn returns the https links in a command's output whose host is exactly one of hosts:
// no other host before or after it, no user info, no port, and not inside another link (a
// link starts its word, after at most an opening bracket or quote). A module reads a cloud
// session's link from what its driver printed with it this way, never with a regular
// expression that could match a link to another host.
func LinksIn(text string, hosts ...string) []*url.URL {
	var out []*url.URL
	for _, f := range strings.Fields(text) {
		f = strings.TrimLeft(f, "([{<\"'")
		if !strings.HasPrefix(f, "https://") {
			continue
		}
		f = strings.TrimRight(f, ")]}>\"'.,;:!?")
		u, err := url.Parse(f)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Opaque != "" {
			continue
		}
		for _, h := range hosts {
			if strings.EqualFold(u.Hostname(), h) && u.Host == u.Hostname() {
				out = append(out, u)
				break
			}
		}
	}
	return out
}

// LinkOn reads one link the user gave (a session's link, pasted): https on exactly one of
// hosts, with no user info and no port. A link without a scheme ("claude.ai/code/…") is read
// as https; plain http, another host, or a host before or after the named one is not a
// link here. A module reads a pasted link this way, never by cutting a prefix off a string.
func LinkOn(s string, hosts ...string) (*url.URL, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return nil, false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Opaque != "" || u.Host != u.Hostname() {
		return nil, false
	}
	for _, h := range hosts {
		if strings.EqualFold(u.Hostname(), h) {
			return u, true
		}
	}
	return nil, false
}

// PathParts are a link's path segments, without empty ones ("/a/b/" → a, b).
func PathParts(u *url.URL) []string {
	var out []string
	for _, p := range strings.Split(u.Path, "/") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
