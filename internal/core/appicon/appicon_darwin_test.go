package appicon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An installed .app's icon, found by its Info.plist, comes back as a PNG data URL.
func TestFindAppIcon(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, "Applications", "Agent.app")
	os.MkdirAll(filepath.Join(app, "Contents", "Resources"), 0o700)
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleIconFile</key><string>agent</string></dict></plist>`), 0o600)
	os.WriteFile(filepath.Join(app, "Contents", "Resources", "agent.icns"), icns(map[string][]byte{"ic07": fakePNG(128)}, "ic07"), 0o600)
	apps := map[string][]string{"darwin": {"/Applications/Not There.app", "~/Applications/Agent.app"}}
	url, err := Find(apps, home)
	if err != nil || !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("icon: %q %v", url, err)
	}
	if _, err := Find(map[string][]string{"darwin": {"~/Applications/None.app"}}, home); err != ErrNone {
		t.Fatalf("no app: %v", err)
	}
}
