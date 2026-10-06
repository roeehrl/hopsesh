package app

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/peer"
)

// hopsesh peer starts directly in the form the Windows machine's ssh shell takes.
func TestPeerCommandWindows(t *testing.T) {
	exe := `C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe`
	for _, tc := range []struct{ shell, want string }{
		{"", `"C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe" peer --stdio`},
		{`C:\Windows\System32\cmd.exe`, `"C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe" peer --stdio`},
		{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, `& 'C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe' peer --stdio`},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, `& 'C:\Users\Sam Doe\AppData\Local\Programs\hopsesh\hopsesh.exe' peer --stdio`},
		{`C:\Program Files\Git\bin\bash.exe`, `'C:/Users/Sam Doe/AppData/Local/Programs/hopsesh/hopsesh.exe' peer --stdio`},
	} {
		if got := peerCommandWindows(exe, tc.shell); got != tc.want {
			t.Errorf("shell %q:\n got %s\nwant %s", tc.shell, got, tc.want)
		}
	}
}

// A push to a machine with hopsesh 0.3 (peer protocol 1) stops at hello and names the
// machine to update, instead of sending it a package it would read without its lineage.
func TestPushToOlderPeer(t *testing.T) {
	a := &App{PeerDial: func(context.Context, config.Host) (*PeerConn, error) {
		cr, sw := io.Pipe()
		sr, cw := io.Pipe()
		go func() {
			defer sw.Close()
			line, _ := bufio.NewReader(sr).ReadString('\n')
			if !strings.Contains(line, `"method":"hello"`) {
				return
			}
			// hopsesh 0.3's refusal, byte for byte.
			_, _ = io.WriteString(sw, `{"id":1,"error":{"code":"protocol","message":"hopsesh here speaks protocol 1, the other machine 2: update hopsesh so both are the same version"}}`+"\n")
		}()
		return &PeerConn{Out: cr, In: cw, Close: func() { cw.Close(); sr.Close() }}, nil
	}}
	_, _, _, err := a.dialPeer(context.Background(), config.Host{Name: "old-box"})
	if !errors.Is(err, peer.ErrProtocol) {
		t.Fatalf("hello to an older peer: %v", err)
	}
	if want := "old-box runs an older hopsesh (peer protocol 1)"; !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "update hopsesh on old-box to "+peer.FirstVersion+" or later") {
		t.Fatalf("%q must name old-box as the one to update", err)
	}
}

func TestReceiveOptionsRetainsTransferIdentityAndSelectedReplica(t *testing.T) {
	a := &App{Cfg: config.Defaults()}
	got := a.receiveOptions(move.Options{OperationID: "same-transfer", TargetSession: "claude/selected"})
	if got.OperationID != "same-transfer" || got.TargetSession != "claude/selected" {
		t.Fatalf("peer lost identity or destination: %+v", got)
	}
}
