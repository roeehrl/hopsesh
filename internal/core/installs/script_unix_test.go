//go:build !windows

package installs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fake writes a program at p that says it is hopsesh v.
func fake(t *testing.T, p, v string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho 'hopsesh "+v+" (commit test)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestScriptFindsInstalls runs the script on a made-up home: an app with its command
// linked into ~/.local/bin, hopsesh's own copy that the login service runs, and a
// command in ~/go/bin. Installs outside the made-up home (this machine's own) are left out.
func TestScriptFindsInstalls(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	appCLI := filepath.Join(home, "Applications", "hopsesh.app", "Contents", "Resources", "bin", "hopsesh")
	own := filepath.Join(home, ".hopsesh", "bin", "hopsesh")
	gobin := filepath.Join(home, "go", "bin", "hopsesh")
	fake(t, appCLI, "0.5.0")
	fake(t, own, "0.6.3")
	fake(t, gobin, "0.6.1")
	link := filepath.Join(home, ".local", "bin", "hopsesh")
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(appCLI, link); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		la := filepath.Join(home, "Library", "LaunchAgents")
		os.MkdirAll(la, 0o755)
		os.WriteFile(filepath.Join(la, serviceLabel+".plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>`+serviceLabel+`</string>
<key>ProgramArguments</key><array><string>`+own+`</string><string>serve</string></array></dict></plist>
`), 0o644)
	} else {
		u := filepath.Join(home, ".config", "systemd", "user")
		os.MkdirAll(u, 0o755)
		os.WriteFile(filepath.Join(u, "hopsesh.service"), []byte("[Service]\nExecStart="+own+" serve\n"), 0o644)
	}
	cmd := exec.Command("sh", "-c", Script(runtime.GOOS), "sh", link)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	l := Parse(out, runtime.GOOS, runtime.GOARCH, home)
	var mine []Install
	for _, in := range l.Installs {
		if strings.HasPrefix(in.Path, home+"/") {
			mine = append(mine, in)
		}
	}
	if len(mine) != 3 {
		t.Fatalf("installs: %+v\n%s", l.Installs, out)
	}
	by := map[string]Install{}
	for _, in := range mine {
		by[in.Path] = in
	}
	if in := by[own]; in.Kind != Own || in.Version != "0.6.3" || !in.Service {
		t.Fatalf("own: %+v\n%s", in, out)
	}
	if in := by[gobin]; in.Kind != Command || in.Version != "0.6.1" {
		t.Fatalf("go bin: %+v", in)
	}
	in := by[appCLI]
	if in.Version != "0.5.0" || !in.Terminal || len(in.Links) != 1 || in.Links[0] != link {
		t.Fatalf("app's command: %+v\n%s", in, out)
	}
	if runtime.GOOS == "darwin" && (in.Kind != App || !in.Main) {
		t.Fatalf("the app is the machine's own: %+v", in)
	}
	if l.Service != own || l.ServiceGone {
		t.Fatalf("service %q gone=%v", l.Service, l.ServiceGone)
	}
}

// install.sh leaves the app's command alone, and says so before downloading anything.
func TestInstallScriptLeavesTheAppsCommand(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "hopsesh.app", "Contents", "Resources", "bin", "hopsesh")
	fake(t, app, "0.6.4")
	if err := os.Symlink(app, filepath.Join(dir, "hopsesh")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "../../../scripts/install.sh")
	cmd.Env = append(os.Environ(), "HOPSESH_INSTALL_DIR="+dir, "HOPSESH_VERSION=0.0.0-never")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "comes from the hopsesh app") || strings.Contains(string(out), "Downloading") {
		t.Fatalf("%v\n%s", err, out)
	}
	if target, _ := os.Readlink(filepath.Join(dir, "hopsesh")); target != app {
		t.Fatalf("the link was changed: %s", target)
	}
}
