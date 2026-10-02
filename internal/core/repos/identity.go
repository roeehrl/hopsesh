package repos

import (
	"net/url"
	"regexp"
	"strings"
)

// scpLikeRE matches git's scp-like syntax: [user@]host:path (not a Windows drive letter).
var scpLikeRE = regexp.MustCompile(`^(?:[^@/]+@)?([^:/]{2,}):(.+)$`)

// Identity normalises a git remote URL to host/owner/repo (lower case, no scheme, user,
// port or .git suffix), so ssh and https remotes of the same repository compare equal.
// It returns "" for local paths and unparseable input.
func Identity(remote string) string {
	r := strings.TrimSpace(remote)
	if r == "" {
		return ""
	}
	var host, p string
	if strings.Contains(r, "://") {
		u, err := url.Parse(r)
		if err != nil || u.Host == "" || u.Scheme == "file" {
			return ""
		}
		host, p = u.Hostname(), u.Path
	} else if m := scpLikeRE.FindStringSubmatch(r); m != nil {
		host, p = m[1], m[2]
	} else {
		return ""
	}
	p = strings.Trim(strings.TrimSuffix(strings.TrimSuffix(p, "/"), ".git"), "/")
	if host == "" || p == "" {
		return ""
	}
	return strings.ToLower(host + "/" + p)
}

// Name returns the repository's short name (last path element) from an identity.
func Name(identity string) string {
	if i := strings.LastIndex(identity, "/"); i >= 0 {
		return identity[i+1:]
	}
	return identity
}
