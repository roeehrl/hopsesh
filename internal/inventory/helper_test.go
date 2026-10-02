package inventory

import (
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/transport"
)

func TestHelperBinary(t *testing.T) {
	if _, err := HelperBinary("", &transport.Facts{OS: "windows", Arch: "amd64"}); err == nil {
		t.Fatal("Windows machines must not get a helper")
	}
	other := "linux"
	if runtime.GOOS == "linux" {
		other = "darwin"
	}
	if _, err := HelperBinary("", &transport.Facts{OS: other, Arch: "x86_64"}); err == nil || !strings.Contains(err.Error(), "--binary") {
		t.Fatalf("a different OS needs --binary: %v", err)
	}
	if b, err := HelperBinary("/x/hopsesh", &transport.Facts{OS: other}); err != nil || b != "/x/hopsesh" {
		t.Fatal("an explicit binary is used as given")
	}
	for in, want := range map[string]string{"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"} {
		if goArch(in) != want {
			t.Errorf("goArch(%s) = %s", in, goArch(in))
		}
	}
}
