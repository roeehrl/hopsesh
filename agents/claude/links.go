package claude

import (
	"net/url"
	"strings"
)

// sessionOf is the session a claude.ai link names: https://claude.ai/code/<id>[?…], with
// nothing after the id in the path ("" otherwise).
func sessionOf(u *url.URL) string {
	rest, ok := strings.CutPrefix(u.Path, "/code/")
	if !ok || strings.Contains(rest, "/") {
		return ""
	}
	return canonical(rest)
}
