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
