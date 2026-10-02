package transport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/roeehrl/hopsesh/internal/core/lnp"
	"github.com/roeehrl/hopsesh/internal/core/repos"
)

// Facts describes a remote machine, learned with one probe.
type Facts struct {
	OS            string `json:"os"` // darwin | linux | windows
	Arch          string `json:"arch,omitempty"`
	Home          string `json:"home"`
	ConfigDir     string `json:"configDir"` // Claude Code's config dir there
	ClaudePath    string `json:"claudePath,omitempty"`
	ClaudeVersion string `json:"claudeVersion,omitempty"`
	HasGit        bool   `json:"hasGit"`
}

const posixFacts = `
printf 'os\t%s\n' "$(uname -s 2>/dev/null)"
printf 'arch\t%s\n' "$(uname -m 2>/dev/null)"
printf 'home\t%s\n' "$HOME"
printf 'claudeconfig\t%s\n' "${CLAUDE_CONFIG_DIR:-}"
for c in "$(command -v claude 2>/dev/null)" "$HOME/.local/bin/claude" "$HOME/.claude/local/claude" /opt/homebrew/bin/claude /usr/local/bin/claude; do
  if [ -n "$c" ] && [ -x "$c" ]; then
    printf 'claude\t%s\n' "$c"
    printf 'claudever\t%s\n' "$("$c" --version 2>/dev/null | head -n1)"
    break
  fi
done
command -v git >/dev/null 2>&1 && printf 'git\t1\n'
exit 0
`

const windowsFacts = `
$h = $env:USERPROFILE
"os` + "`t" + `windows"
"arch` + "`t" + `$env:PROCESSOR_ARCHITECTURE"
"home` + "`t" + `$h"
"claudeconfig` + "`t" + `$env:CLAUDE_CONFIG_DIR"
$c = (Get-Command claude -ErrorAction SilentlyContinue).Source
if (-not $c) { foreach ($p in @("$h\.local\bin\claude.exe", "$env:LOCALAPPDATA\Programs\claude\claude.exe")) { if (Test-Path $p) { $c = $p; break } } }
if ($c) { "claude` + "`t" + `$c"; $v = (& $c --version 2>$null | Select-Object -First 1); "claudever` + "`t" + `$v" }
if (Get-Command git -ErrorAction SilentlyContinue) { "git` + "`t" + `1" }
`

// Probe learns the remote machine's OS, home, Claude config dir and Claude version.
func (c *Conn) Probe(ctx context.Context) (*Facts, error) {
	out, err := c.Run(ctx, "uname -s")
	var re *RemoteError
	if err != nil && !errors.As(err, &re) {
		return nil, err // connection-level failure
	}
	if err == nil && strings.TrimSpace(string(out)) != "" {
		out, err = c.RunSh(ctx, posixFacts)
		if err != nil {
			return nil, err
		}
		f := parseFacts(out)
		f.OS = normUname(f.OS)
		if f.ConfigDir == "" {
			f.ConfigDir = f.Home + "/.claude"
		}
		return f, nil
	}
	out, err = c.RunPowerShell(ctx, windowsFacts)
	if err != nil {
		return nil, fmt.Errorf("could not identify the remote system: %w", err)
	}
	f := parseFacts(out)
	f.OS = "windows"
	if f.ConfigDir == "" {
		f.ConfigDir = f.Home + `\.claude`
	}
	return f, nil
}

// RunPowerShell runs a PowerShell script on a Windows machine (whatever its default ssh
// shell is) via -EncodedCommand, which avoids every cmd.exe quoting pitfall.
func (c *Conn) RunPowerShell(ctx context.Context, script string) ([]byte, error) {
	u := utf16.Encode([]rune("$ProgressPreference='SilentlyContinue';" + script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[2*i], b[2*i+1] = byte(v), byte(v>>8)
	}
	enc := base64.StdEncoding.EncodeToString(b)
	return c.Run(ctx, "powershell -NoProfile -NonInteractive -EncodedCommand "+enc)
}

func parseFacts(out []byte) *Facts {
	f := &Facts{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(strings.TrimRight(sc.Text(), "\r"), "\t")
		switch k {
		case "os":
			f.OS = v
		case "arch":
			f.Arch = strings.ToLower(v)
		case "home":
			f.Home = v
		case "claudeconfig":
			f.ConfigDir = v
		case "claude":
			f.ClaudePath = v
		case "claudever":
			f.ClaudeVersion = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "(Claude Code)"))
		case "git":
			f.HasGit = v == "1"
		}
	}
	return f
}

func normUname(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	}
	if strings.Contains(strings.ToLower(s), "mingw") || strings.Contains(strings.ToLower(s), "msys") || strings.Contains(strings.ToLower(s), "cygwin") {
		return "windows"
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// Alive reports which pids are running on the remote machine.
func (c *Conn) Alive(ctx context.Context, f *Facts, pids []int) map[int]bool {
	out := map[int]bool{}
	if len(pids) == 0 {
		return out
	}
	var res []byte
	var err error
	if f.OS == "windows" {
		ids := make([]string, len(pids))
		for i, p := range pids {
			ids[i] = strconv.Itoa(p)
		}
		res, err = c.RunPowerShell(ctx, "Get-Process -Id "+strings.Join(ids, ",")+" -ErrorAction SilentlyContinue | ForEach-Object { $_.Id }")
	} else {
		args := make([]string, len(pids))
		for i, p := range pids {
			args[i] = strconv.Itoa(p)
		}
		res, err = c.RunSh(ctx, `for p in "$@"; do kill -0 "$p" 2>/dev/null && echo "$p"; done; exit 0`, args...)
	}
	if err != nil {
		return out
	}
	for _, line := range strings.Fields(string(res)) {
		if n, err := strconv.Atoi(line); err == nil {
			out[n] = true
		}
	}
	return out
}

// GitProbe returns the git state of directories on the remote machine.
func (c *Conn) GitProbe(ctx context.Context, f *Facts, dirs []string) ([]repos.GitState, error) {
	if len(dirs) == 0 || !f.HasGit {
		return nil, nil
	}
	if f.OS == "windows" {
		out, err := c.RunPowerShell(ctx, repos.PowerShellProbe(dirs))
		if err != nil {
			return nil, err
		}
		return repos.ParseProbe(out), nil
	}
	script, args := repos.ProbeScript(dirs)
	out, err := c.RunSh(ctx, script, args[1:]...)
	if err != nil {
		return nil, err
	}
	return repos.ParseProbe(out), nil
}

// ResolvedHost is what ssh -G says about a destination.
type ResolvedHost struct {
	HostName     string
	Port         string
	User         string
	HostKeyAlias string
}

// Resolve asks the local ssh client how it would connect to dest.
func (c *Conn) Resolve(ctx context.Context) (*ResolvedHost, error) {
	out, err := exec.CommandContext(ctx, c.sshBinary, "-G", c.Dest).Output()
	if err != nil {
		return nil, err
	}
	r := &ResolvedHost{Port: "22"}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		switch k {
		case "hostname":
			r.HostName = v
		case "port":
			r.Port = v
		case "user":
			r.User = v
		case "hostkeyalias":
			r.HostKeyAlias = v
		}
	}
	return r, nil
}

// HostKey is a scanned host key.
type HostKey struct {
	Line        string `json:"-"` // known_hosts line
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"` // SHA256:...
	Key         string `json:"-"`
}

// ScanHostKeys fetches the machine's host keys with ssh-keyscan (nothing is trusted yet).
func (c *Conn) ScanHostKeys(ctx context.Context) ([]HostKey, *ResolvedHost, error) {
	r, err := c.Resolve(ctx)
	if err != nil {
		return nil, nil, err
	}
	if c.override == "" && len(c.Fallbacks) > 0 {
		if _, err := net.LookupHost(r.HostName); err != nil {
			c.override = c.Fallbacks[0] // the alias's own name does not resolve from here
		}
	}
	if c.override != "" {
		r.HostName = c.override
	}
	bin, err := exec.LookPath("ssh-keyscan")
	if err != nil {
		return nil, r, errors.New("ssh-keyscan not found")
	}
	// ssh-keyscan connects straight to the host name, so local network privacy applies
	// to that name alone.
	port, _ := strconv.Atoi(r.Port)
	var gated []gatedTarget
	if need, _ := lnp.Needs(ctx, r.HostName); need {
		t := gatedTarget{host: r.HostName, port: port}
		if lnp.InApp() && lnp.CanProbe {
			start := time.Now()
			t.state = lnp.Probe(ctx, t.host, t.port, lnp.Wait(c.StateDir))
			lnp.Remember(c.StateDir, t.state, time.Since(start))
		}
		if t.state == lnp.Denied && c.override == "" && len(c.Fallbacks) > 0 {
			c.override = c.Fallbacks[0] // a Tailscale route is not subject to the setting
			r.HostName = c.override
		} else {
			gated = append(gated, t)
		}
	}
	ks := exec.CommandContext(ctx, bin, "-T", "5", "-p", r.Port, r.HostName)
	var ksErr bytes.Buffer
	ks.Stderr = &ksErr
	out, err := ks.Output()
	if len(out) == 0 {
		why := strings.TrimSpace(ksErr.String())
		if why == "" && err != nil {
			why = err.Error()
		}
		return nil, r, c.explainLocalNetwork(fmt.Errorf("%w: ssh-keyscan %s: %s", ErrUnreachable, r.HostName, firstLine(why)), gated)
	}
	name := r.HostName
	if r.HostKeyAlias != "" {
		name = r.HostKeyAlias
	}
	if r.Port != "22" {
		name = "[" + name + "]:" + r.Port
	}
	var keys []HostKey
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || strings.HasPrefix(f[0], "#") {
			continue
		}
		fp, err := Fingerprint(f[2])
		if err != nil {
			continue
		}
		keys = append(keys, HostKey{Line: name + " " + f[1] + " " + f[2], Type: f[1], Fingerprint: fp, Key: f[1] + " " + f[2]})
	}
	if len(keys) == 0 {
		return nil, r, fmt.Errorf("%w: no host keys from %s", ErrUnreachable, r.HostName)
	}
	return keys, r, nil
}

// Fingerprint returns the OpenSSH SHA256 fingerprint of a base64 key blob.
func Fingerprint(b64 string) (string, error) {
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]), nil
}

// Trust appends confirmed host keys to hopsesh's own known_hosts.
func (c *Conn) Trust(keys []HostKey) error {
	if err := os.MkdirAll(filepath.Dir(c.KnownHostsFile()), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(c.KnownHostsFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, k := range keys {
		if _, err := f.WriteString(k.Line + "\n"); err != nil {
			return err
		}
	}
	return nil
}

// MatchesTailscale reports whether any scanned key equals a key Tailscale publishes for
// the node (a strong, out-of-band confirmation).
func MatchesTailscale(keys []HostKey, tsKeys []string) bool {
	for _, k := range keys {
		for _, t := range tsKeys {
			if strings.TrimSpace(t) == k.Key {
				return true
			}
		}
	}
	return false
}

// KnownElsewhere returns the names under which the user's ~/.ssh/known_hosts already
// trusts one of these keys (e.g. the machine's IP), which verifies a key seen under a new
// name. Hashed known_hosts entries are matched by key alone.
func KnownElsewhere(keys []HostKey, userKnownHosts string) []string {
	f, err := os.Open(userKnownHosts)
	if err != nil {
		return nil
	}
	defer f.Close()
	want := map[string]bool{}
	for _, k := range keys {
		want[k.Key] = true
	}
	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || strings.HasPrefix(fields[0], "#") || strings.HasPrefix(fields[0], "@") {
			continue
		}
		if want[fields[1]+" "+fields[2]] {
			names = append(names, fields[0])
		}
	}
	return names
}
