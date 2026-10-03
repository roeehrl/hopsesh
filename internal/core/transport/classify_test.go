package transport

import (
	"errors"
	"os/exec"
	"runtime"
	"testing"
)

// ssh's Tailscale check message gives its link; a link inside another URL does not count.
func TestTailscaleCheckLink(t *testing.T) {
	exit := exec.Command("sh", "-c", "exit 255")
	if runtime.GOOS == "windows" {
		exit = exec.Command("cmd", "/c", "exit 255")
	}
	err := exit.Run()
	got := classify(err, "# Tailscale SSH requires an additional check.\n# To authenticate, visit: https://login.tailscale.com/a/abc123\n")
	var tc *TailscaleCheckError
	if !errors.As(got, &tc) || tc.URL != "https://login.tailscale.com/a/abc123" {
		t.Fatalf("got %v", got)
	}
	got = classify(err, "see https://evil.example/?next=https://login.tailscale.com/a/x")
	if errors.As(got, &tc) {
		t.Fatalf("a link inside another URL must not count: %v", got)
	}
}
