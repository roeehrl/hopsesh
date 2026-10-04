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
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/roeehrl/hopsesh/internal/core/lnp"
	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// RunPowerShell runs a PowerShell script on a Windows machine (whatever its default ssh
// shell is) via -EncodedCommand, which avoids every cmd.exe quoting pitfall. A script too
// long for a command line goes through standard input instead.
func (c *Conn) RunPowerShell(ctx context.Context, script string) ([]byte, error) {
	cmd, stdin := PowerShellInvocation(script)
	return c.RunInput(ctx, cmd, stdin)
}

// maxCommandLine keeps a command line inside cmd.exe's limit (8191 characters), the shell
// Windows' OpenSSH server uses unless told otherwise.
const maxCommandLine = 8000

// psFromStdin reads a base64 UTF-8 script from standard input and runs it. It reads one
// line, not to the end: Windows' OpenSSH server does not always pass the end of input on
// to the command, and waiting for it hung until the timeout.
const psFromStdin = `$s = [Console]::In.ReadLine(); Invoke-Expression ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($s.Trim())))`

// PowerShellInvocation is the command line for a script and what to send on its standard
// input (nil when the script fits on the command line). -EncodedCommand triples a
// script's length, so a script that grows with the number of sessions or folders soon
// outgrows the command line: then a short fixed command reads it from standard input.
func PowerShellInvocation(script string) (string, []byte) {
	if cmd := PowerShellCommand(script); len(cmd) <= maxCommandLine {
		return cmd, nil
	}
	return PowerShellCommand(psFromStdin), []byte(base64.StdEncoding.EncodeToString([]byte(script)) + "\n")
}

// PowerShellCommand is the command line that runs a PowerShell script on a Windows
// machine through -EncodedCommand (valid whether its ssh shell is cmd.exe or PowerShell).
func PowerShellCommand(script string) string {
	u := utf16.Encode([]rune("$ProgressPreference='SilentlyContinue';" + script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[2*i], b[2*i+1] = byte(v), byte(v>>8)
	}
	return "powershell -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(b)
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
	out, err := proc.CommandContext(ctx, c.sshBinary, "-G", c.Dest).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(bytes.TrimSpace(ee.Stderr)) > 0 {
			return nil, fmt.Errorf("ssh cannot read the settings for %s: %s", c.Dest, firstLine(strings.TrimSpace(string(ee.Stderr))))
		}
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
	ks := proc.CommandContext(ctx, bin, "-T", "5", "-p", r.Port, r.HostName)
	var ksErr bytes.Buffer
	ks.Stderr = &ksErr
	out, err := ks.Output()
	name := r.HostName
	if r.HostKeyAlias != "" {
		name = r.HostKeyAlias
	}
	if r.Port != "22" {
		name = "[" + name + "]:" + r.Port
	}
	keys := parseHostKeys(out, name)
	if len(keys) == 0 {
		// Windows' ssh-keyscan returns only the banner from some OpenSSH servers (Ubuntu's
		// 9.6, for one); ssh itself still reads the key.
		keys = parseHostKeys(c.hostKeyViaSSH(ctx), name)
	}
	if len(keys) == 0 {
		why := strings.TrimSpace(ksErr.String())
		if why == "" && err != nil {
			why = err.Error()
		}
		if why == "" {
			why = "no host keys"
		}
		return nil, r, c.explainLocalNetwork(fmt.Errorf("%w: ssh-keyscan %s: %s", ErrUnreachable, r.HostName, firstLine(why)), gated)
	}
	return keys, r, nil
}

// parseHostKeys reads known_hosts-style lines ("host type key"; comments skipped) as the
// keys of name.
func parseHostKeys(out []byte, name string) []HostKey {
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
	return keys
}

// hostKeyViaSSH reads the machine's host key the way ssh records it: one connection, as
// hopsesh would make it (the destination's user, keys and settings), with a throwaway
// known_hosts file that accepts the new key. Nothing is trusted by this; the caller shows
// the key for confirmation. Returns that file's lines.
func (c *Conn) hostKeyViaSSH(ctx context.Context) []byte {
	dir, err := os.MkdirTemp("", "hopsesh-hostkey-")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(dir)
	kh := filepath.Join(dir, "known_hosts")
	null := "/dev/null"
	if runtime.GOOS == "windows" {
		null = "NUL"
	}
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + quoteList(kh), "-o", "GlobalKnownHostsFile=" + null,
		"-o", "HashKnownHosts=no", "-o", "ControlPath=none", "-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes", "-o", "LogLevel=ERROR"}
	if c.override != "" {
		args = append(args, "-o", "HostName="+c.override)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_ = proc.CommandContext(ctx, c.sshBinary, append(args, c.Dest, "exit")...).Run() // the login may fail; the key is recorded first
	b, _ := os.ReadFile(kh)
	return b
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
