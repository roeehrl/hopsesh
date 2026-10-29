package installs

import (
	"strings"
	"testing"
)

// lines builds Script output.
func lines(ls ...string) []byte { return []byte(strings.Join(ls, "\n") + "\n") }

const appCLI = "/Applications/hopsesh.app/Contents/Resources/bin/hopsesh"

func TestParseMacAppLinkedAndOwnCopy(t *testing.T) {
	// The laptop as found: the app (0.5.0) with its command linked into ~/.local/bin, and
	// the copy another machine set up (0.6.3), which the login service runs.
	l := Parse(lines(
		"at\t/Users/kim/.hopsesh/bin/hopsesh\t/Users/kim/.hopsesh/bin/hopsesh",
		"ver\t/Users/kim/.hopsesh/bin/hopsesh\thopsesh 0.6.3 (commit 4f9e624, built x)",
		"at\t/Users/kim/.local/bin/hopsesh\t"+appCLI,
		"ver\t"+appCLI+"\thopsesh 0.5.0 (commit 362452f)",
		"at\t"+appCLI+"\t"+appCLI,
		"term\t"+appCLI,
		"svc\t/Users/kim/.hopsesh/bin/hopsesh\t1",
		"open\t/Applications/hopsesh.app",
	), "darwin", "arm64", "/Users/kim")
	if len(l.Installs) != 2 {
		t.Fatalf("installs: %+v", l.Installs)
	}
	own, app := l.Installs[0], l.Installs[1]
	if own.Kind != Own || own.Version != "0.6.3" || !own.Service || own.Main {
		t.Fatalf("own copy: %+v", own)
	}
	if app.Kind != App || app.Bundle != "/Applications/hopsesh.app" || app.Version != "0.5.0" || !app.Main || !app.Terminal || !app.Open {
		t.Fatalf("app: %+v", app)
	}
	if len(app.Links) != 1 || app.Links[0] != "/Users/kim/.local/bin/hopsesh" {
		t.Fatalf("links: %v", app.Links)
	}
	if !l.Mixed() || strings.Join(l.Versions(), ",") != "0.5.0,0.6.3" {
		t.Fatalf("versions: %v", l.Versions())
	}

	steps := Plan(l, "0.6.4", "/Users/kim/.hopsesh/bin/hopsesh")
	got := dos(steps)
	if got != "app link service" {
		t.Fatalf("plan: %s\n%+v", got, steps)
	}
	if !strings.Contains(steps[0].Text, "0.5.0 → 0.6.4") || !strings.Contains(steps[0].Text, "reopened") {
		t.Fatalf("app step: %s", steps[0].Text)
	}
	if !strings.Contains(steps[2].Text, "it starts ~/.hopsesh/bin/hopsesh now") {
		t.Fatalf("service step: %s", steps[2].Text)
	}
	if Program(l, steps) != appCLI {
		t.Fatal("the app's hopsesh runs the machine")
	}
}

func dos(steps []Step) string {
	var s []string
	for _, st := range steps {
		s = append(s, st.Do)
	}
	return strings.Join(s, " ")
}

func TestPlanCases(t *testing.T) {
	home := "/home/sam"
	own := home + "/.hopsesh/bin/hopsesh"
	for _, c := range []struct {
		name, os, to string
		out          []byte
		want         string
		main         string
	}{
		{name: "nothing there", os: "linux", to: "0.6.4", out: nil, want: "install service", main: ""},
		{name: "own copy only, older", os: "linux", to: "0.6.4",
			out:  lines("at\t"+own+"\t"+own, "ver\t"+own+"\thopsesh 0.6.3", "svc\t"+own+"\t1"),
			want: "replace", main: own},
		{name: "own copy only, same", os: "linux", to: "0.6.4",
			out:  lines("at\t"+own+"\t"+own, "ver\t"+own+"\thopsesh 0.6.4", "svc\t"+own+"\t1"),
			want: "", main: own},
		{name: "never downgrades", os: "linux", to: "0.6.4",
			out:  lines("at\t"+own+"\t"+own, "ver\t"+own+"\thopsesh 0.7.0", "svc\t"+own+"\t1"),
			want: "", main: own},
		{name: "standalone command and own copy: the command is the machine's", os: "linux", to: "0.6.4",
			out: lines("at\t"+home+"/.local/bin/hopsesh\t"+home+"/.local/bin/hopsesh", "ver\t"+home+"/.local/bin/hopsesh\thopsesh 0.6.2",
				"at\t"+own+"\t"+own, "ver\t"+own+"\thopsesh 0.6.3", "term\t"+home+"/.local/bin/hopsesh", "svc\t"+own+"\t1"),
			want: "replace link service", main: home + "/.local/bin/hopsesh"},
		{name: "a package is the user's to update", os: "linux", to: "0.6.4",
			out:  lines("at\t/usr/bin/hopsesh\t/usr/bin/hopsesh", "ver\t/usr/bin/hopsesh\thopsesh 0.6.1", "pkg\t/usr/bin/hopsesh\tapt", "term\t/usr/bin/hopsesh"),
			want: "manual service", main: "/usr/bin/hopsesh"},
		{name: "Homebrew", os: "darwin", to: "0.6.4",
			out:  lines("at\t/opt/homebrew/bin/hopsesh\t/opt/homebrew/Cellar/hopsesh/0.6.1/bin/hopsesh", "ver\t/opt/homebrew/Cellar/hopsesh/0.6.1/bin/hopsesh\thopsesh 0.6.1"),
			want: "manual service", main: "/opt/homebrew/Cellar/hopsesh/0.6.1/bin/hopsesh"},
		{name: "the service runs a deleted app", os: "darwin", to: "0.6.4",
			out:  lines("at\t"+own+"\t"+own, "ver\t"+own+"\thopsesh 0.6.4", "svc\t"+appCLI+"\t0"),
			want: "service", main: own},
		{name: "a development build replaces only its own copy", os: "darwin", to: "dev",
			out: lines("at\t"+appCLI+"\t"+appCLI, "ver\t"+appCLI+"\thopsesh 0.6.3", "svc\t"+appCLI+"\t1",
				"at\t/usr/local/bin/hopsesh\t/usr/local/bin/hopsesh", "ver\t/usr/local/bin/hopsesh\thopsesh 0.6.3"),
			want: "keep keep", main: appCLI},
		{name: "two apps: the one your shell runs", os: "darwin", to: "0.6.4",
			out: lines("at\t"+appCLI+"\t"+appCLI, "ver\t"+appCLI+"\thopsesh 0.6.4",
				"at\t"+home+"/Applications/hopsesh.app/Contents/Resources/bin/hopsesh\t"+home+"/Applications/hopsesh.app/Contents/Resources/bin/hopsesh",
				"ver\t"+home+"/Applications/hopsesh.app/Contents/Resources/bin/hopsesh\thopsesh 0.6.4",
				"term\t"+home+"/Applications/hopsesh.app/Contents/Resources/bin/hopsesh", "svc\t"+home+"/Applications/hopsesh.app/Contents/Resources/bin/hopsesh\t1"),
			want: "", main: home + "/Applications/hopsesh.app/Contents/Resources/bin/hopsesh"},
	} {
		t.Run(c.name, func(t *testing.T) {
			l := Parse(c.out, c.os, "amd64", home)
			if m := l.Main(); (m == nil) != (c.main == "") || m != nil && m.Path != c.main {
				t.Fatalf("main: %+v", m)
			}
			if got := dos(Plan(l, c.to, own)); got != c.want {
				t.Fatalf("plan %q, want %q: %+v", got, c.want, Plan(l, c.to, own))
			}
		})
	}
}

func TestPackageCommand(t *testing.T) {
	l := Parse(lines("at\t/usr/bin/hopsesh\t/usr/bin/hopsesh", "ver\t/usr/bin/hopsesh\thopsesh 0.6.1", "pkg\t/usr/bin/hopsesh\tapt"), "linux", "arm64", "/home/sam")
	s := Plan(l, "0.6.4", "/home/sam/.hopsesh/bin/hopsesh")[0]
	if s.Command != "sudo apt install ./hopsesh_0.6.4_linux_arm64.deb" {
		t.Fatal(s.Command)
	}
}

func TestParseWindowsApp(t *testing.T) {
	dir := `C:\Users\sam\AppData\Local\Programs\hopsesh`
	l := Parse(lines(
		"at\t"+dir+`\hopsesh.exe`+"\t"+dir+`\hopsesh.exe`,
		"ver\t"+dir+`\hopsesh.exe`+"\thopsesh 0.6.3",
		"appdir\t"+dir+`\hopsesh.exe`+"\t"+dir,
		"at\t"+`C:\Users\sam\.hopsesh\bin\hopsesh.exe`+"\t"+`C:\Users\sam\.hopsesh\bin\hopsesh.exe`,
		"ver\t"+`C:\Users\sam\.hopsesh\bin\hopsesh.exe`+"\thopsesh 0.6.3",
		"term\t"+strings.ToUpper(dir)+`\HOPSESH.EXE`,
		"svc\t"+`C:\Users\sam\.hopsesh\bin\hopsesh.exe`+"\t1",
		"open\t"+dir,
	), "windows", "amd64", `C:\Users\sam`)
	app := l.Main()
	if app == nil || app.Kind != App || app.Bundle != dir || !app.Terminal || !app.Open {
		t.Fatalf("app: %+v", l.Installs)
	}
	// No links on Windows: hopsesh's own copy is brought to the same version instead.
	if got := dos(Plan(l, "0.6.4", `C:\Users\sam\.hopsesh\bin\hopsesh.exe`)); got != "app replace service" {
		t.Fatal(got)
	}
}

func TestManagerOf(t *testing.T) {
	for p, want := range map[string]string{
		"/opt/homebrew/Cellar/hopsesh/0.6.4/bin/hopsesh":         "Homebrew",
		"/home/linuxbrew/.linuxbrew/Cellar/hopsesh/x/hopsesh":    "Homebrew",
		`C:\Users\sam\scoop\apps\hopsesh\current\hopsesh.exe`:    "Scoop",
		`C:\Users\sam\AppData\Local\Microsoft\WinGet\Packages\x`: "winget",
		"/Users/sam/.local/bin/hopsesh":                          "",
		appCLI:                                                   "",
	} {
		if got := ManagerOf(p); got != want {
			t.Errorf("%s: %q, want %q", p, got, want)
		}
	}
}
