package host

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/proc"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// probeWants is what every Spec asks a probe for.
type probeWants struct {
	env  []string
	bins []agent.Binary
}

// Hopsesh is hopsesh itself on a machine: the probe reports whether it is there (to work
// with it as a peer) and its version.
var Hopsesh = agent.Binary{
	Name: "hopsesh",
	Candidates: map[string][]string{
		"*":       {"~/.local/bin/hopsesh", "/opt/homebrew/bin/hopsesh", "/usr/local/bin/hopsesh", "/Applications/hopsesh.app/Contents/Resources/bin/hopsesh"},
		"windows": {"~/AppData/Local/Programs/hopsesh/hopsesh.exe"},
	},
	VersionArgs: []string{"version"},
}

func wants(specs []agent.Spec) probeWants {
	w := probeWants{bins: []agent.Binary{Hopsesh}, env: []string{"HOPSESH_CONFIG_DIR", "HOPSESH_STATE_DIR", "XDG_STATE_HOME", "LOCALAPPDATA"}}
	seen := map[string]bool{"HOPSESH_CONFIG_DIR": true, "HOPSESH_STATE_DIR": true, "XDG_STATE_HOME": true, "LOCALAPPDATA": true}
	for _, s := range specs {
		for _, e := range SpecEnv(s) {
			if !seen[e] && envName.MatchString(e) {
				seen[e] = true
				w.env = append(w.env, e)
			}
		}
		w.bins = append(w.bins, s.Binaries...)
	}
	sort.Strings(w.env)
	return w
}

// ProbeLocal learns this machine's facts for the given modules.
func ProbeLocal(ctx context.Context, specs []agent.Spec) Facts {
	return probeLocal(ctx, specs, false)
}

// ObserveLocal resolves roots and executable paths without starting vendor programs,
// checking login, or launching desktop protocol helpers. Observation must be safe on
// an uninitialized installation and on a machine without a graphical session.
func ObserveLocal(ctx context.Context, specs []agent.Spec) Facts {
	return probeLocal(ctx, specs, true)
}

func probeLocal(ctx context.Context, specs []agent.Spec, passive bool) Facts {
	w := wants(specs)
	home, _ := os.UserHomeDir()
	f := Facts{OS: runtime.GOOS, Arch: runtime.GOARCH, Home: home, Env: map[string]string{}, Binaries: map[string]agent.BinaryFact{}}
	for _, e := range w.env {
		f.Env[e] = os.Getenv(e)
	}
	for _, b := range w.bins {
		p := lookLocal(b, home)
		if p == "" {
			continue
		}
		bf := agent.BinaryFact{Path: p}
		if !passive && len(b.VersionArgs) > 0 {
			vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			out, _ := proc.CommandContext(vctx, p, b.VersionArgs...).Output()
			cancel()
			bf.Version = firstLine(string(out))
		}
		f.Binaries[b.Name] = bf
	}
	_, err := exec.LookPath("git")
	f.HasGit = err == nil
	f.DesktopProtocols = map[string]bool{}
	for _, s := range specs {
		if !passive && s.DesktopScheme != "" {
			f.DesktopProtocols[s.DesktopScheme] = registeredDesktopProtocol(ctx, s.DesktopScheme)
		}
	}
	return f
}

func lookLocal(b agent.Binary, home string) string {
	if p, err := exec.LookPath(b.Name); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	for _, c := range candidates(b, runtime.GOOS) {
		if strings.HasPrefix(c, "~/") {
			c = filepath.Join(home, c[2:])
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && (runtime.GOOS == "windows" || fi.Mode()&0o111 != 0) {
			return c
		}
	}
	return ""
}

func candidates(b agent.Binary, goos string) []string {
	if c, ok := b.Candidates[goos]; ok {
		return c
	}
	return b.Candidates["*"]
}

// ProbeRemote learns a machine's facts in one round trip, with a script generated from
// the modules' Specs (no module code runs on the remote machine).
func ProbeRemote(ctx context.Context, c *transport.Conn, specs []agent.Spec) (Facts, error) {
	w := wants(specs)
	out, err := c.Run(ctx, "uname -s")
	var re *transport.RemoteError
	if err != nil && !errors.As(err, &re) {
		return Facts{}, err // the connection itself failed
	}
	if err == nil && strings.TrimSpace(string(out)) != "" && !windowsUname(string(out)) {
		out, err = c.RunSh(ctx, posixProbe(w))
		if err != nil {
			return Facts{}, err
		}
		f := parseProbe(out)
		f.OS = normUname(f.OS)
		return f, nil
	}
	out, err = c.RunPowerShell(ctx, windowsProbe(w))
	if err != nil {
		return Facts{}, fmt.Errorf("could not identify the remote system: %w", err)
	}
	f := parseProbe(out)
	f.OS = "windows"
	return f, nil
}

// posixProbe prints tab-separated facts: os, arch, home, env NAME value, bin NAME path,
// ver NAME line, git 1.
func posixProbe(w probeWants) string {
	var b strings.Builder
	b.WriteString("printf 'os\\t%s\\n' \"$(uname -s 2>/dev/null)\"\n")
	b.WriteString("printf 'arch\\t%s\\n' \"$(uname -m 2>/dev/null)\"\n")
	b.WriteString("printf 'home\\t%s\\n' \"$HOME\"\n")
	for _, e := range w.env {
		fmt.Fprintf(&b, "printf 'env\\t%s\\t%%s\\n' \"${%s:-}\"\n", e, e)
	}
	for _, bin := range w.bins {
		name := transport.ShQuote(bin.Name)
		fmt.Fprintf(&b, "c=\"$(command -v %s 2>/dev/null)\"\n", name)
		var cands []string
		for _, c := range candidates(bin, "*") {
			if strings.HasPrefix(c, "~/") {
				cands = append(cands, "\"$HOME\"/"+transport.ShQuote(c[2:]))
			} else {
				cands = append(cands, transport.ShQuote(c))
			}
		}
		if len(cands) > 0 {
			fmt.Fprintf(&b, "[ -n \"$c\" ] || for p in %s; do [ -x \"$p\" ] && c=\"$p\" && break; done\n", strings.Join(cands, " "))
		}
		fmt.Fprintf(&b, "if [ -n \"$c\" ]; then printf 'bin\\t%%s\\t%%s\\n' %s \"$c\"", name)
		if len(bin.VersionArgs) > 0 {
			args := make([]string, len(bin.VersionArgs))
			for i, a := range bin.VersionArgs {
				args[i] = transport.ShQuote(a)
			}
			fmt.Fprintf(&b, "; printf 'ver\\t%%s\\t%%s\\n' %s \"$(\"$c\" %s 2>/dev/null | head -n1)\"", name, strings.Join(args, " "))
		}
		b.WriteString("; fi\n")
	}
	b.WriteString("command -v git >/dev/null 2>&1 && printf 'git\\t1\\n'\nexit 0\n")
	return b.String()
}

func windowsProbe(w probeWants) string {
	var b strings.Builder
	b.WriteString("$h = $env:USERPROFILE\n\"os`twindows\"\n\"arch`t$env:PROCESSOR_ARCHITECTURE\"\n\"home`t$h\"\n")
	for _, e := range w.env {
		fmt.Fprintf(&b, "\"env`t%s`t$env:%s\"\n", e, e)
	}
	for _, bin := range w.bins {
		name := transport.PSQuote(bin.Name)
		fmt.Fprintf(&b, "$c = (Get-Command %s -ErrorAction SilentlyContinue).Source\n", name)
		var cands []string
		for _, c := range candidates(bin, "windows") {
			if strings.HasPrefix(c, "~/") {
				cands = append(cands, "(Join-Path $h "+transport.PSQuote(strings.ReplaceAll(c[2:], "/", `\`))+")")
			} else {
				cands = append(cands, transport.PSQuote(c))
			}
		}
		if len(cands) > 0 {
			fmt.Fprintf(&b, "if (-not $c) { foreach ($p in @(%s)) { if (Test-Path $p) { $c = $p; break } } }\n", strings.Join(cands, ", "))
		}
		fmt.Fprintf(&b, "if ($c) { \"bin`t%s`t$c\"", bin.Name)
		if len(bin.VersionArgs) > 0 {
			args := make([]string, len(bin.VersionArgs))
			for i, a := range bin.VersionArgs {
				args[i] = transport.PSQuote(a)
			}
			fmt.Fprintf(&b, "; $v = (& $c %s 2>$null | Select-Object -First 1); \"ver`t%s`t$v\"", strings.Join(args, " "), bin.Name)
		}
		b.WriteString(" }\n")
	}
	b.WriteString("if (Get-Command git -ErrorAction SilentlyContinue) { \"git`t1\" }\n")
	return b.String()
}

func parseProbe(out []byte) Facts {
	f := Facts{Env: map[string]string{}, Binaries: map[string]agent.BinaryFact{}}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		parts := strings.SplitN(strings.TrimRight(sc.Text(), "\r"), "\t", 3)
		switch {
		case len(parts) == 2 && parts[0] == "os":
			f.OS = parts[1]
		case len(parts) == 2 && parts[0] == "arch":
			f.Arch = strings.ToLower(parts[1])
		case len(parts) == 2 && parts[0] == "home":
			f.Home = parts[1]
		case len(parts) == 2 && parts[0] == "git":
			f.HasGit = parts[1] == "1"
		case len(parts) == 3 && parts[0] == "env":
			f.Env[parts[1]] = parts[2]
		case len(parts) == 3 && parts[0] == "bin":
			bf := f.Binaries[parts[1]]
			bf.Path = parts[2]
			f.Binaries[parts[1]] = bf
		case len(parts) == 3 && parts[0] == "ver":
			bf := f.Binaries[parts[1]]
			bf.Version = strings.TrimSpace(parts[2])
			f.Binaries[parts[1]] = bf
		}
	}
	return f
}

// windowsUname reports a uname from a Unix layer on Windows (Git for Windows, MSYS2,
// Cygwin): Windows OpenSSH runs commands in cmd or PowerShell, and the paths are Windows'.
func windowsUname(s string) bool {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.HasPrefix(s, "MINGW") || strings.HasPrefix(s, "MSYS") || strings.HasPrefix(s, "CYGWIN")
}

func normUname(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
