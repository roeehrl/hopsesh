package hosts

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Candidate is a machine found by discovery. Discovery never connects to anything.
type Candidate struct {
	Name        string   `json:"name"`        // short label
	Destination string   `json:"destination"` // what ssh is given
	Via         []string `json:"via"`         // tailscale, ssh-config
	OS          string   `json:"os,omitempty"`
	Online      *bool    `json:"online,omitempty"` // tailscale's view, if known
	DNSName     string   `json:"dnsName,omitempty"`
	IPs         []string `json:"ips,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	OtherOwner  bool     `json:"otherOwner,omitempty"` // shared into the tailnet by someone else
	SSHHostKeys []string `json:"sshHostKeys,omitempty"`
	Self        bool     `json:"self,omitempty"`
	LastSeen    string   `json:"lastSeen,omitempty"`
}

// Discover merges Tailscale peers and ~/.ssh/config aliases.
func Discover(ctx context.Context) ([]Candidate, error) {
	ts, _ := Tailscale(ctx)
	aliases := SSHConfigAliases(filepath.Join(home(), ".ssh", "config"))
	return Merge(ts, aliases), nil
}

// tsStatus is the part of `tailscale status --json` hopsesh uses. Tailscale documents this
// structure as unstable, so every field is optional.
type tsStatus struct {
	Self *tsPeer           `json:"Self"`
	Peer map[string]tsPeer `json:"Peer"`
	User map[string]struct {
		LoginName   string `json:"LoginName"`
		DisplayName string `json:"DisplayName"`
	} `json:"User"`
}

type tsPeer struct {
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	OS           string   `json:"OS"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Online       bool     `json:"Online"`
	UserID       int64    `json:"UserID"`
	SSHHostKeys  []string `json:"sshHostKeys"`
	Expired      bool     `json:"Expired"`
	LastSeen     string   `json:"LastSeen"`
}

// Tailscale lists tailnet peers that could run Claude Code (no phones or tablets, no
// expired nodes), marking the local node and nodes owned by other users.
func Tailscale(ctx context.Context) ([]Candidate, error) {
	bin := tailscaleBinary()
	if bin == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "status", "--json").Output()
	if err != nil {
		return nil, err
	}
	return parseTailscale(out)
}

func parseTailscale(out []byte) ([]Candidate, error) {
	var st tsStatus
	if err := json.Unmarshal(out, &st); err != nil {
		return nil, err
	}
	var selfUser int64
	var res []Candidate
	conv := func(p tsPeer, self bool) Candidate {
		on := p.Online || self
		dns := strings.TrimSuffix(p.DNSName, ".")
		c := Candidate{
			Name: firstLabel(dns), Destination: dns, Via: []string{"tailscale"}, OS: normOS(p.OS),
			Online: &on, DNSName: dns, IPs: p.TailscaleIPs, SSHHostKeys: p.SSHHostKeys, Self: self, LastSeen: p.LastSeen,
		}
		if u, ok := st.User[itoa(p.UserID)]; ok {
			c.Owner = u.LoginName
		}
		return c
	}
	if st.Self != nil {
		selfUser = st.Self.UserID
		res = append(res, conv(*st.Self, true))
	}
	keys := make([]string, 0, len(st.Peer))
	for k := range st.Peer {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := st.Peer[k]
		if p.Expired || mobileOS(p.OS) || p.DNSName == "" {
			continue
		}
		c := conv(p, false)
		c.OtherOwner = selfUser != 0 && p.UserID != selfUser
		res = append(res, c)
	}
	return res, nil
}

// tailscaleBinary finds the Tailscale CLI: HOPSESH_TAILSCALE names it ("off" skips
// Tailscale), else PATH, else where the apps install it.
func tailscaleBinary() string {
	switch v := os.Getenv("HOPSESH_TAILSCALE"); v {
	case "off":
		return ""
	case "":
	default:
		return v
	}
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	for _, p := range []string{
		"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
		`C:\Program Files\Tailscale\tailscale.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Alias is one concrete Host entry from ssh config (wildcard patterns are skipped).
type Alias struct {
	Names    []string // every name on the Host line
	HostName string
	User     string
	Port     string
}

// SSHConfigAliases lists concrete Host aliases from an ssh config file and the files it
// Includes. Real connection settings are always resolved later with `ssh -G`, which
// handles Match, ProxyJump and every other directive exactly like ssh.
func SSHConfigAliases(path string) []Alias {
	var out []Alias
	seen := map[string]bool{}
	var parse func(p string, depth int)
	parse = func(p string, depth int) {
		if depth > 8 || seen[p] {
			return
		}
		seen[p] = true
		f, err := os.Open(p)
		if err != nil {
			return
		}
		defer f.Close()
		var cur *Alias
		inMatch := false
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, val := splitDirective(line)
			switch strings.ToLower(key) {
			case "include":
				for _, pat := range strings.Fields(val) {
					pat = expandTilde(pat)
					if !filepath.IsAbs(pat) {
						pat = filepath.Join(filepath.Dir(path), pat)
					}
					matches, _ := filepath.Glob(pat)
					for _, m := range matches {
						parse(m, depth+1)
					}
				}
			case "host":
				inMatch = false
				cur = nil
				var names []string
				for _, n := range strings.Fields(val) {
					if strings.ContainsAny(n, "*?!") {
						continue
					}
					names = append(names, n)
				}
				if len(names) > 0 {
					out = append(out, Alias{Names: names})
					cur = &out[len(out)-1]
				}
			case "match":
				inMatch = true
				cur = nil
			case "hostname":
				if cur != nil && !inMatch {
					cur.HostName = val
				}
			case "user":
				if cur != nil && !inMatch {
					cur.User = val
				}
			case "port":
				if cur != nil && !inMatch {
					cur.Port = val
				}
			}
		}
	}
	parse(path, 0)
	return out
}

func splitDirective(line string) (string, string) {
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return line, ""
	}
	return line[:i], strings.Trim(strings.TrimSpace(line[i+1:]), `="`)
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func normName(s string) string {
	return nonAlnum.ReplaceAllString(strings.ToLower(firstLabel(s)), "")
}

// Merge joins Tailscale peers with ssh aliases that point at the same machine (by
// Tailscale IP, MagicDNS name or normalised host name). The alias becomes the destination
// because it carries the right user, port and keys.
func Merge(ts []Candidate, aliases []Alias) []Candidate {
	used := make([]bool, len(aliases))
	for i := range ts {
		c := &ts[i]
		if c.Self {
			continue
		}
		for j, a := range aliases {
			if used[j] || !aliasMatches(a, c) {
				continue
			}
			used[j] = true
			c.Destination = a.Names[0]
			c.Name = a.Names[0]
			c.Via = append(c.Via, "ssh-config")
			break
		}
	}
	out := ts
	for j, a := range aliases {
		if used[j] || isGitHost(a) {
			continue
		}
		out = append(out, Candidate{Name: a.Names[0], Destination: a.Names[0], Via: []string{"ssh-config"}})
	}
	return out
}

func aliasMatches(a Alias, c *Candidate) bool {
	targets := append([]string{a.HostName}, a.Names...)
	for _, t := range targets {
		if t == "" {
			continue
		}
		for _, ip := range c.IPs {
			if t == ip {
				return true
			}
		}
		if strings.EqualFold(strings.TrimSuffix(t, "."), c.DNSName) {
			return true
		}
		if n := normName(t); n != "" && (n == normName(c.DNSName) || n == normName(c.Name)) {
			return true
		}
	}
	return false
}

// isGitHost filters well-known git forges from ssh config (not machines with sessions).
func isGitHost(a Alias) bool {
	h := strings.ToLower(a.HostName)
	if h == "" {
		h = strings.ToLower(a.Names[0])
	}
	for _, g := range []string{"github.com", "gitlab.com", "bitbucket.org", "ssh.dev.azure.com", "codeberg.org"} {
		if h == g || strings.HasSuffix(h, "."+g) {
			return true
		}
	}
	return a.User == "git"
}

func firstLabel(s string) string {
	if i := strings.IndexByte(s, '.'); i > 0 {
		return s[:i]
	}
	return s
}

func mobileOS(os string) bool {
	switch strings.ToLower(os) {
	case "ios", "android", "tvos":
		return true
	}
	return false
}

func normOS(os string) string {
	switch strings.ToLower(os) {
	case "macos", "darwin":
		return "darwin"
	case "windows":
		return "windows"
	case "linux":
		return "linux"
	}
	return strings.ToLower(os)
}

func itoa(v int64) string { return strconvItoa(v) }

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func expandTilde(p string) string {
	if strings.HasPrefix(p, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(p, `~\`)) {
		return filepath.Join(home(), p[2:])
	}
	return p
}
