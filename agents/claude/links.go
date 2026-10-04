package claude

import (
	"net/url"
	"strings"
)

// linksIn returns the https links in text whose host is exactly one of hosts: no other
// host before or after it, no user info, no port, and not inside another link (the link
// starts its word, after at most an opening bracket or quote). A session's link is read
// from what claude printed with it, never with a regular expression that could match a
// link to another host.
//
// It is the same as the SDK's agent.LinksIn (roeehrl/hopsesh#47), stricter about a link
// that does not start its word; once that helper is in the SDK, this one goes.
func linksIn(text string, hosts ...string) []*url.URL {
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

// sessionOf is the session a claude.ai link names: https://claude.ai/code/<id>[?…], with
// nothing after the id in the path ("" otherwise).
func sessionOf(u *url.URL) string {
	rest, ok := strings.CutPrefix(u.Path, "/code/")
	if !ok || strings.Contains(rest, "/") {
		return ""
	}
	return canonical(rest)
}
