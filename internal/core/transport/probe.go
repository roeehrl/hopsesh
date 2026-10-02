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
)

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
