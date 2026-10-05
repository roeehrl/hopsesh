// Package winshim finds the program behind a Windows command shim: the .cmd file npm (and
// pnpm, and older npm versions) writes for a package's command, such as codex.cmd. The app's
// terminal tabs never run a batch file, because cmd.exe would read the tab's arguments
// (internal/core/pty refuses them), so a tab runs what the shim would have run instead:
// node with the package's script, or the package's own .exe.
//
// Only the shims' known shapes are read, never run. A shim's command line is the last line
// that passes the arguments on (%*); before %* it names the program and its fixed
// arguments, where %dp0% and %~dp0 are the shim's folder and "%_prog%" is node.exe beside
// the shim when there is one, else node. When the shim names node.exe beside it on one line
// and node on another (older npm, pnpm), the first that exists wins. Anything else (another
// variable, a second batch file, a shape not recognised) is refused with a message that
// says to run the program itself.
//
// Not carried over: the environment a shim sets for its program (pnpm's NODE_PATH) and its
// PATHEXT change; npm's packages do not need them.
package winshim

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// maxShim is the largest file read as a shim.
const maxShim = 64 << 10

// Env is how Resolve looks at the system: tests give their own.
type Env struct {
	// Read reads a shim.
	Read func(path string) ([]byte, error)
	// Exists reports whether a file is there.
	Exists func(path string) bool
	// LookPath finds a program on PATH ("node").
	LookPath func(name string) (string, error)
}

// System is the running system.
func System() Env {
	return Env{
		Read: func(p string) ([]byte, error) {
			f, err := os.Open(p)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			return io.ReadAll(io.LimitReader(f, maxShim+1))
		},
		Exists: func(p string) bool {
			fi, err := os.Stat(p)
			return err == nil && fi.Mode().IsRegular()
		},
		LookPath: exec.LookPath,
	}
}

// IsShim reports whether a program path is a batch file (.cmd or .bat).
func IsShim(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".cmd") || strings.HasSuffix(p, ".bat")
}

// Program is argv with a batch file in argv[0] (a full path, as PATH lookup found it)
// replaced by what the shim runs; any other argv comes back as it is.
func Program(argv []string, e Env) ([]string, error) {
	if len(argv) == 0 || !IsShim(argv[0]) {
		return argv, nil
	}
	return Resolve(argv[0], argv[1:], e)
}

// ErrUnknownShim means a batch file is not a shim of a known shape.
var ErrUnknownShim = errors.New("not a command shim hopsesh can read")

// Resolve is the argument list that runs what the shim at path (a full Windows path) runs,
// with args after the shim's own.
func Resolve(path string, args []string, e Env) ([]string, error) {
	b, err := e.Read(path)
	if err != nil {
		return nil, err
	}
	if len(b) > maxShim {
		return nil, fmt.Errorf("%s: %w (too large)", path, ErrUnknownShim)
	}
	dir := winDir(path)
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, "%*") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%s: %w (it passes no arguments on)", path, ErrUnknownShim)
	}
	var tried []string
	for _, l := range lines {
		argv, err := commandOf(l, dir, e)
		if err != nil {
			return nil, fmt.Errorf("%s: %w (%v)", path, ErrUnknownShim, err)
		}
		prog, ok := locate(argv[0], e)
		if !ok {
			tried = append(tried, argv[0])
			continue
		}
		if IsShim(prog) {
			return nil, fmt.Errorf("%s: %w (it starts another batch file, %s)", path, ErrUnknownShim, prog)
		}
		argv[0] = prog
		return append(argv, args...), nil
	}
	return nil, fmt.Errorf("%s runs %s, which is not there", path, strings.Join(tried, " or "))
}

// commandOf reads one shim line that passes the arguments on: the program and its fixed
// arguments before %*, with the shim's variables filled in.
func commandOf(line, dir string, e Env) ([]string, error) {
	l := line[:strings.Index(line, "%*")]
	// npm's current shim: endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%" "…" %*
	if i := strings.LastIndex(strings.ToUpper(l), "%COMSPEC% &"); i >= 0 {
		l = l[i+len("%COMSPEC% &"):]
	}
	l = strings.TrimSpace(l)
	l = strings.TrimPrefix(l, "@")
	toks, err := tokens(l)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, errors.New("no program before %*")
	}
	node := dir + `\node.exe`
	for i, t := range toks {
		t = replaceFold(t, "%dp0%", dir+`\`)
		t = replaceFold(t, "%~dp0", dir+`\`)
		if strings.EqualFold(t, "%_prog%") {
			t = "node"
			if e.Exists(node) {
				t = node
			}
		}
		if strings.ContainsAny(t, "%^&|<>") {
			return nil, fmt.Errorf("it uses %q", t)
		}
		if strings.ContainsRune(t, '\\') {
			t = winClean(t)
		}
		toks[i] = t
	}
	return toks, nil
}

// tokens splits a command line's words: quoted words keep their spaces, quotes are
// dropped.
func tokens(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in, has := false, false
	for _, r := range s {
		switch {
		case r == '"':
			in, has = !in, true
		case (r == ' ' || r == '\t') && !in:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if in {
		return nil, errors.New("an unclosed quote")
	}
	if has {
		out = append(out, cur.String())
	}
	return out, nil
}

// locate is the full path of a shim's program: a path that exists, or a bare name found
// on PATH.
func locate(p string, e Env) (string, bool) {
	if strings.ContainsAny(p, `\/:`) {
		return p, e.Exists(p)
	}
	found, err := e.LookPath(p)
	return found, err == nil && found != ""
}

// winDir is the folder of a Windows path, without a trailing backslash.
func winDir(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	if i := strings.LastIndex(p, `\`); i >= 0 {
		return p[:i]
	}
	return "."
}

// winClean tidies a Windows path: forward slashes become backslashes, doubled separators
// one, and "." and ".." are resolved (never above the drive or share).
func winClean(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	prefix := ""
	switch {
	case strings.HasPrefix(p, `\\`):
		prefix, p = `\\`, p[2:]
	case len(p) >= 2 && p[1] == ':':
		prefix, p = p[:2], p[2:]
	}
	abs := strings.HasPrefix(p, `\`)
	var parts []string
	for _, s := range strings.Split(p, `\`) {
		switch s {
		case "", ".":
		case "..":
			if len(parts) > 0 && parts[len(parts)-1] != ".." {
				parts = parts[:len(parts)-1]
			} else if !abs {
				parts = append(parts, "..")
			}
		default:
			parts = append(parts, s)
		}
	}
	out := strings.Join(parts, `\`)
	if abs {
		out = `\` + out
	}
	return prefix + out
}

// replaceFold replaces every old in s, ignoring ASCII case (cmd.exe's variables are not
// case-sensitive).
func replaceFold(s, old, repl string) string {
	lo, lold := strings.ToLower(s), strings.ToLower(old)
	var b strings.Builder
	for {
		i := strings.Index(lo, lold)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(repl)
		s, lo = s[i+len(old):], lo[i+len(old):]
	}
}
