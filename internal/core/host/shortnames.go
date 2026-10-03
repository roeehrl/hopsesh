package host

import (
	"context"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/transport"
)

// ShortNames returns the 8.3 short form of each path on a Windows machine
// ("C:\Users\LIHUA~1\proj"), for the paths that have one that differs. A session can name
// the same folder either way; moves rewrite both. Other systems, snapshots, and drives
// with short names turned off give none.
func (m *Machine) ShortNames(ctx context.Context, paths []string) map[string]string {
	out := map[string]string{}
	if m.Facts.OS != "windows" || m.snap != nil || len(paths) == 0 {
		return out
	}
	var shorts []string
	if m.Local {
		for _, p := range paths {
			shorts = append(shorts, shortPath(p))
		}
	} else {
		quoted := make([]string, len(paths))
		for i, p := range paths {
			quoted[i] = transport.PSQuote(p)
		}
		script := `[Console]::OutputEncoding = [Text.Encoding]::UTF8
$fso = New-Object -ComObject Scripting.FileSystemObject
foreach ($p in @(` + strings.Join(quoted, ",") + `)) {
  if (Test-Path -LiteralPath $p -PathType Container) { $fso.GetFolder($p).ShortPath }
  elseif (Test-Path -LiteralPath $p) { $fso.GetFile($p).ShortPath }
  else { '-' }
}`
		res, err := m.Conn.RunPowerShell(ctx, script)
		if err != nil {
			return out
		}
		shorts = strings.Split(strings.ReplaceAll(strings.TrimSpace(string(res)), "\r", ""), "\n")
	}
	if len(shorts) != len(paths) {
		return out
	}
	for i, p := range paths {
		if s := strings.TrimSpace(shorts[i]); s != "" && s != "-" && !strings.EqualFold(s, p) {
			out[p] = s
		}
	}
	return out
}
