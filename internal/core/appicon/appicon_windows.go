package appicon

import (
	"context"
	"encoding/base64"
	"os/exec"
	"strings"
	"time"
)

// iconOf reads a Windows program's icon through the .NET drawing library.
func iconOf(exe string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := `Add-Type -AssemblyName System.Drawing
$i = [System.Drawing.Icon]::ExtractAssociatedIcon($env:HOPSESH_ICON_EXE)
$m = New-Object System.IO.MemoryStream
$i.ToBitmap().Save($m, [System.Drawing.Imaging.ImageFormat]::Png)
[Convert]::ToBase64String($m.ToArray())`
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(cmd.Environ(), "HOPSESH_ICON_EXE="+exe)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
}
