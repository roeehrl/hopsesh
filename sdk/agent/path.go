package agent

import (
	"path"
	"strings"
)

// PathFor returns the path rules of an operating system.
func PathFor(goos string) Path {
	if goos == "windows" {
		return WindowsPath{}
	}
	return PosixPath{}
}

// PosixPath is the path form of macOS and Linux.
type PosixPath struct{}

func (PosixPath) Join(elem ...string) string { return path.Join(elem...) }
func (PosixPath) Base(p string) string       { return path.Base(p) }
func (PosixPath) Dir(p string) string        { return path.Dir(p) }
func (PosixPath) Clean(p string) string      { return path.Clean(p) }
func (PosixPath) IsAbs(p string) bool        { return path.IsAbs(p) }
func (PosixPath) Sep() string                { return "/" }

func (PosixPath) Rel(base, target string) (string, bool) {
	base, target = path.Clean(base), path.Clean(target)
	if target == base {
		return ".", true
	}
	prefix := strings.TrimSuffix(base, "/") + "/"
	if !strings.HasPrefix(target, prefix) {
		return "", false
	}
	return strings.TrimPrefix(target, prefix), true
}

// WindowsPath is the path form of Windows: drive letters, backslashes, and names compared
// without regard to case.
type WindowsPath struct{}

func (WindowsPath) slash(p string) string { return strings.ReplaceAll(p, `\`, "/") }
func (WindowsPath) back(p string) string  { return strings.ReplaceAll(p, "/", `\`) }

func (w WindowsPath) Join(elem ...string) string {
	var parts []string
	for _, e := range elem {
		if e != "" {
			parts = append(parts, w.slash(e))
		}
	}
	return w.Clean(strings.Join(parts, "/"))
}

func (w WindowsPath) Clean(p string) string {
	s := w.slash(p)
	vol := ""
	if len(s) >= 2 && s[1] == ':' {
		vol, s = strings.ToUpper(s[:1])+":", s[2:]
	}
	c := path.Clean(s)
	if c == "." && vol != "" {
		c = "/"
	}
	return w.back(vol + c)
}

func (w WindowsPath) Base(p string) string { return path.Base(w.slash(p)) }
func (w WindowsPath) Dir(p string) string  { return w.Clean(path.Dir(w.slash(p))) }
func (WindowsPath) IsAbs(p string) bool {
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}
func (WindowsPath) Sep() string { return `\` }

func (w WindowsPath) Rel(base, target string) (string, bool) {
	b, t := w.Clean(base), w.Clean(target)
	if strings.EqualFold(b, t) {
		return ".", true
	}
	prefix := strings.TrimSuffix(b, `\`) + `\`
	if len(t) <= len(prefix) || !strings.EqualFold(t[:len(prefix)], prefix) {
		return "", false
	}
	return t[len(prefix):], true
}
