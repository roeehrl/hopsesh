package app

import "testing"

// hopsesh peer starts directly in the form the Windows machine's ssh shell takes.
func TestPeerCommandWindows(t *testing.T) {
	exe := `C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe`
	for _, tc := range []struct{ shell, want string }{
		{"", `""C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe" peer --stdio"`},
		{`C:\Windows\System32\cmd.exe`, `""C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe" peer --stdio"`},
		{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, `& 'C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe' peer --stdio`},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, `& 'C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe' peer --stdio`},
	} {
		if got := peerCommandWindows(exe, tc.shell); got != tc.want {
			t.Errorf("shell %q:\n got %s\nwant %s", tc.shell, got, tc.want)
		}
	}
}
