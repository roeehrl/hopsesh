// Package installs finds every hopsesh on a machine: the app, a command you installed, one
// a package manager owns, and the copy hopsesh puts in ~/.hopsesh/bin when it sets a
// machine up. It says who owns each, which one is the machine's own (Main: the one its
// login service, other machines and its hooks should run), and plans an update that
// leaves every copy hopsesh may replace at one version, so the app, the command in your
// terminal and the service that receives sessions never drift apart.
//
// The facts are read by a script (Script) run on the machine, locally or over SSH, so any
// version of hopsesh there, or none, can be listed; Parse and Plan are plain functions.
package installs

import (
	"path"
	"sort"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Kind is what an install is.
type Kind string

const (
	App     Kind = "app"     // hopsesh.app, or the Windows app's folder (with the command inside)
	Command Kind = "command" // a hopsesh you installed: install.sh, a release archive, go install
	Own     Kind = "own"     // the copy hopsesh puts in ~/.hopsesh/bin when it sets a machine up
	Package Kind = "package" // owned by a package manager (Homebrew, Scoop, winget, apt, dnf, apk)
)

// Install is one hopsesh program on a machine.
type Install struct {
	Path    string   `json:"path"`            // the program, links followed
	Links   []string `json:"links,omitempty"` // other paths that run it (~/.local/bin/hopsesh → the app's)
	Version string   `json:"version,omitempty"`
	Kind    Kind     `json:"kind"`
	// Bundle is the app (hopsesh.app, or the Windows app's folder); Open: it is running.
	Bundle string `json:"bundle,omitempty"`
	Open   bool   `json:"open,omitempty"`
	// Manager owns a Package; UpdateWith is the command that updates it there.
	Manager    string `json:"manager,omitempty"`
	UpdateWith string `json:"updateWith,omitempty"`
	Main       bool   `json:"main,omitempty"`
	Terminal   bool   `json:"terminal,omitempty"` // the user's shell runs this one
	Service    bool   `json:"service,omitempty"`  // hopsesh's login service runs this one
}

// Runs reports whether p is this install's program or one of its links.
func (i Install) Runs(p string) bool {
	if p == "" {
		return false
	}
	if same(i.Path, p) {
		return true
	}
	for _, l := range i.Links {
		if same(l, p) {
			return true
		}
	}
	return false
}

// List is every hopsesh on one machine.
type List struct {
	OS       string    `json:"os"`
	Arch     string    `json:"arch,omitempty"`
	Home     string    `json:"home,omitempty"`
	Installs []Install `json:"installs"`
	// Service is the program hopsesh's login service runs ("" when there is none);
	// ServiceGone: that program is not there any more (the app was deleted or moved).
	Service     string `json:"service,omitempty"`
	ServiceGone bool   `json:"serviceGone,omitempty"`
	// Terminal is where the user's shell finds hopsesh ("" when it does not).
	Terminal string `json:"terminal,omitempty"`
}

// Main is the machine's own hopsesh, or nil when there is none.
func (l List) Main() *Install {
	for i := range l.Installs {
		if l.Installs[i].Main {
			return &l.Installs[i]
		}
	}
	return nil
}

// Versions are the distinct versions installed, oldest first ("" left out).
func (l List) Versions() []string {
	seen := map[string]bool{}
	var out []string
	for _, in := range l.Installs {
		if in.Version != "" && !seen[in.Version] {
			seen[in.Version] = true
			out = append(out, in.Version)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return Newer(out[j], out[i]) })
	return out
}

// Mixed reports whether the installs there are not all one version.
func (l List) Mixed() bool { return len(l.Versions()) > 1 }

// Newer reports whether hopsesh version a is newer than b. Development builds are never
// newer (nobody can update to one) and anything is newer than an unknown version.
func Newer(a, b string) bool {
	if a == "" || strings.HasPrefix(a, "dev") {
		return false
	}
	if b == "" || strings.HasPrefix(b, "dev") {
		return true
	}
	c, ok := agent.CompareVersions(a, b)
	return ok && c > 0
}

// IsDev reports a development build's version (not a release anyone can download).
func IsDev(v string) bool { return v == "" || strings.HasPrefix(v, "dev") }

// classify says what the program at p is, from where it is (links followed) and, on
// Linux, the package that owns it (manager, when the script found one).
func classify(p, goos, home, manager, appDir string) (Kind, string, string) {
	low := strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	lowHome := strings.ToLower(strings.ReplaceAll(home, `\`, "/"))
	switch {
	case goos == "darwin" && strings.Contains(p, ".app/Contents/"):
		return App, p[:strings.Index(p, ".app/Contents/")+len(".app")], ""
	case appDir != "":
		return App, appDir, ""
	case strings.Contains(low, "/cellar/") || strings.Contains(low, "/homebrew/") || strings.Contains(low, "/linuxbrew/"):
		return Package, "", "Homebrew"
	case strings.Contains(low, "/scoop/"):
		return Package, "", "Scoop"
	case strings.Contains(low, "/winget/"):
		return Package, "", "winget"
	case manager != "":
		return Package, "", manager
	case lowHome != "" && strings.HasPrefix(low, strings.TrimRight(lowHome, "/")+"/.hopsesh/bin/"):
		return Own, "", ""
	}
	return Command, "", ""
}

// ManagerOf names the package manager that owns the program at p by where it is
// (Homebrew, Scoop, winget), or "" (a Linux package is found by asking dpkg, rpm or apk).
func ManagerOf(p string) string {
	if k, _, m := classify(p, "", "", "", ""); k == Package {
		return m
	}
	return ""
}

// UpdateCommand is how the user updates hopsesh from a package manager to version v.
func UpdateCommand(manager, v, arch string) string {
	switch manager {
	case "Homebrew":
		return "brew upgrade hopsesh"
	case "Scoop":
		return "scoop update hopsesh"
	case "winget":
		return "winget upgrade hopsesh"
	case "apt":
		return "sudo apt install ./" + pkgFile(v, arch, "deb")
	case "dnf":
		return "sudo dnf install ./" + pkgFile(v, arch, "rpm")
	case "apk":
		return "sudo apk add --allow-untrusted ./" + pkgFile(v, arch, "apk")
	}
	return ""
}

func pkgFile(v, arch, ext string) string {
	if v == "" || IsDev(v) {
		v = "<version>"
	}
	if arch == "" {
		arch = "<arch>"
	}
	return "hopsesh_" + v + "_linux_" + arch + "." + ext
}

// pickMain chooses the machine's own hopsesh: an app (the one your shell or the login
// service runs, else the one in the system's Applications), else the one your shell runs,
// else the one the login service runs, else a command you installed, a package, and
// last hopsesh's own copy.
func pickMain(l *List) {
	best, score := -1, -1
	for i, in := range l.Installs {
		s := 0
		switch in.Kind {
		case App:
			s = 100
			if !strings.HasPrefix(in.Bundle, l.Home+"/") {
				s += 5
			}
		case Command:
			s = 30
		case Package:
			s = 20
		case Own:
			s = 10
		}
		if in.Terminal {
			s += 40
		}
		if in.Service && !l.ServiceGone {
			s += 20
		}
		if s > score {
			best, score = i, s
		}
	}
	if best >= 0 {
		l.Installs[best].Main = true
	}
}

// same compares two paths of one machine (Windows' are not case-sensitive).
func same(a, b string) bool {
	if a == b {
		return true
	}
	if strings.Contains(a, `\`) || strings.Contains(b, `\`) {
		return strings.EqualFold(a, b)
	}
	return path.Clean(a) == path.Clean(b)
}
