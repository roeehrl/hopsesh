package installs

import "fmt"

// What a Step does.
const (
	DoInstall = "install" // put this hopsesh in ~/.hopsesh/bin (nothing else is there)
	DoReplace = "replace" // write this hopsesh over an older copy hopsesh may replace
	DoApp     = "app"     // update the app with its own verified updater
	DoLink    = "link"    // point hopsesh's own copy at the machine's own hopsesh
	DoManual  = "manual"  // a package manager owns it: the user updates it
	DoKeep    = "keep"    // left as it is, with why (a development build can't update an app)
	DoService = "service" // start the machine's own hopsesh at login instead of another
)

// Step is one thing an update does on a machine.
type Step struct {
	Do      string `json:"do"`
	Path    string `json:"path"`
	Bundle  string `json:"bundle,omitempty"` // DoApp: the app (Path is its hopsesh)
	From    string `json:"from,omitempty"`   // its version now
	To      string `json:"to,omitempty"`     // the version it gets
	Text    string `json:"text"`
	Command string `json:"command,omitempty"` // DoManual: what the user runs there
}

// Plan is what updating hopsesh on a machine (l) to version to does: every copy hopsesh
// may replace that is older is brought to it (the app by its own updater, the others with
// this program), hopsesh's own copy runs the machine's own hopsesh when there is another
// (so the hooks that name it keep working), a package is left to its manager, and the
// login service runs the machine's own hopsesh. Nothing newer than to is touched.
// ownPath is where hopsesh's own copy goes, and links are possible there (not Windows).
func Plan(l List, to, ownPath string) []Step {
	main := l.Main()
	if main == nil {
		return []Step{
			{Do: DoInstall, Path: ownPath, To: to, Text: "Copy hopsesh " + to + " to " + Tilde(ownPath, l.Home)},
			{Do: DoService, Path: ownPath, Text: "Start it at login"},
		}
	}
	var steps, links []Step
	dev := IsDev(to)
	for _, in := range l.Installs {
		older := in.Version != to && (dev || Newer(to, in.Version))
		where := Tilde(in.Path, l.Home)
		switch {
		case in.Kind == Own && !in.Main && l.OS != "windows":
			links = append(links, Step{Do: DoLink, Path: in.Path, From: in.Version, To: main.Version,
				Text: fmt.Sprintf("Point hopsesh's own copy (%s) at %s, so the hooks that run it run that", where, describe(*main, l.Home))})
		case !older, dev && in.Kind == Package:
		case in.Kind == App && dev:
			steps = append(steps, Step{Do: DoKeep, Path: in.Bundle, From: in.Version,
				Text: fmt.Sprintf("Leave the app (%s) at %s: this hopsesh is a development build, which can't update it", Tilde(in.Bundle, l.Home), orUnknown(in.Version))})
		case in.Kind == App:
			t := fmt.Sprintf("Update the app (%s) %s → %s with its own updater (it checks the release's signature%s)", Tilde(in.Bundle, l.Home), orUnknown(in.Version), to, map[bool]string{true: ", Apple's notarization", false: ""}[l.OS == "darwin"])
			if in.Open {
				t += "; it is open there and uses " + to + " once it is reopened"
			}
			steps = append(steps, Step{Do: DoApp, Path: in.Path, Bundle: in.Bundle, From: in.Version, To: to, Text: t})
		case in.Kind == Package:
			cmd := UpdateCommand(in.Manager, to, l.Arch)
			steps = append(steps, Step{Do: DoManual, Path: in.Path, From: in.Version, To: to, Command: cmd,
				Text: fmt.Sprintf("hopsesh %s at %s comes from %s; update it there with %s", orUnknown(in.Version), where, in.Manager, cmd)})
		case dev && in.Kind == Command:
			steps = append(steps, Step{Do: DoKeep, Path: in.Path, From: in.Version,
				Text: fmt.Sprintf("Leave hopsesh %s at %s: this hopsesh is a development build", orUnknown(in.Version), where)})
		default:
			steps = append(steps, Step{Do: DoReplace, Path: in.Path, From: in.Version, To: to,
				Text: fmt.Sprintf("Update hopsesh at %s %s → %s", where, orUnknown(in.Version), to)})
		}
	}
	// Linked last: once the machine's own hopsesh is up to date.
	steps = append(steps, links...)
	if !main.Service || l.ServiceGone {
		t := "Start " + describe(*main, l.Home) + " at login"
		if l.Service != "" {
			was := Tilde(l.Service, l.Home)
			if l.ServiceGone {
				was += ", which isn't there any more"
			}
			t += " (it starts " + was + " now)"
		}
		steps = append(steps, Step{Do: DoService, Path: main.Path, Text: t})
	}
	return steps
}

// Program is the hopsesh the machine runs once the steps are done: its own one, or the
// copy they install.
func Program(l List, steps []Step) string {
	if m := l.Main(); m != nil {
		return m.Path
	}
	for _, s := range steps {
		if s.Do == DoInstall {
			return s.Path
		}
	}
	return ""
}

// describe names an install for a sentence.
func describe(in Install, home string) string {
	switch in.Kind {
	case App:
		return "the app's hopsesh"
	case Own:
		return "hopsesh's own copy"
	}
	return Tilde(in.Path, home)
}

func orUnknown(v string) string {
	if v == "" {
		return "(version unknown)"
	}
	return v
}

// Tilde shortens a path in home to ~/….
func Tilde(p, home string) string {
	if home != "" && len(p) > len(home) && p[:len(home)] == home && (p[len(home)] == '/' || p[len(home)] == '\\') {
		return "~" + p[len(home):]
	}
	return p
}
