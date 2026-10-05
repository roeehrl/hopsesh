package termapp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The encoded line reaches PowerShell unchanged: quotes, ampersands and non-ASCII.
func TestEncodePSRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), `Lǐ Huá's & co`)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `[Console]::OutputEncoding = [Text.Encoding]::UTF8; Set-Location -LiteralPath '` + strings.ReplaceAll(dir, "'", "''") +
		`'; (Get-Location).Path; Write-Output 'a&b "c"'`
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePS(line)).Output()
	if err != nil {
		t.Fatalf("powershell: %v\n%s", err, out)
	}
	// PowerShell may spell the temp folder in full where Go has its short (8.3) name.
	got := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")), "\n")
	if len(got) != 2 || !strings.HasSuffix(got[0], `\Lǐ Huá's & co`) || got[1] != `a&b "c"` {
		t.Fatalf("got %q", got)
	}
}
