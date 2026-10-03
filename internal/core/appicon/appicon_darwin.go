package appicon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// iconOf reads a .app bundle's icon: the .icns its Info.plist names.
func iconOf(app string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "plutil", "-extract", "CFBundleIconFile", "raw", filepath.Join(app, "Contents", "Info.plist")).Output()
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(string(out))
	if filepath.Ext(name) == "" {
		name += ".icns"
	}
	b, err := os.ReadFile(filepath.Join(app, "Contents", "Resources", filepath.Base(name)))
	if err != nil {
		return nil, err
	}
	return PNGFromICNS(b, 128)
}
