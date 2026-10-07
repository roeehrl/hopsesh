package runtime

import (
	"encoding/xml"
	"path/filepath"
	"strings"
	"testing"
)

func TestServicePlansAreNamespaceScopedAndCredentialFree(t *testing.T) {
	root := t.TempDir()
	n := Namespace{ID: "abc123", Config: filepath.Join(root, "settings & review"), State: filepath.Join(root, "state"), Directory: filepath.Join(root, "runtime")}
	exe := filepath.Join(root, `app "%"`, "hopsesh")
	for _, platform := range []string{"darwin", "linux", "windows"} {
		p, err := PlanService(n, exe, platform, root, "1000")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(p.Name, n.ID) || !strings.Contains(p.Definition, "runtime") || strings.Contains(p.Definition, "password") {
			t.Fatalf("bad plan: %+v", p)
		}
		if platform != "linux" {
			d := xml.NewDecoder(strings.NewReader(p.Definition))
			for {
				_, err := d.Token()
				if err != nil {
					if err.Error() != "EOF" {
						t.Fatal(err)
					}
					break
				}
			}
		}
		if platform == "linux" && !strings.Contains(p.Definition, "%%") {
			t.Fatal("systemd specifier not escaped")
		}
	}
	if _, err := PlanService(n, "relative", "linux", root, "1000"); err == nil {
		t.Fatal("relative executable accepted")
	}
	if _, err := PlanService(n, exe, "plan9", root, "1000"); err == nil {
		t.Fatal("unsupported platform accepted")
	}
	if _, err := PlanService(n, exe+"\ncommand", "linux", root, "1000"); err == nil {
		t.Fatal("injected directive accepted")
	}
}
