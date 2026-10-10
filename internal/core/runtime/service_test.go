package runtime

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/unicode"
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

func TestWindowsServiceFilePreservesUnicodeAndVerifiedOwnership(t *testing.T) {
	root := t.TempDir()
	n := Namespace{ID: "unicode", Config: filepath.Join(root, "設定 & é 📁"), State: filepath.Join(root, "state"), Directory: root}
	exe := filepath.Join(root, "יישום 📁", "hopsesh.exe")
	p, err := PlanService(n, exe, "windows", root, "S-1-5-21-1000")
	if err != nil {
		t.Fatal(err)
	}
	encoded := p.definitionBytes()
	if !bytes.HasPrefix(encoded, []byte{0xff, 0xfe}) {
		t.Fatal("scheduled task file has no UTF-16LE byte-order mark")
	}
	var task struct {
		Command  string `xml:"Actions>Exec>Command"`
		Args     string `xml:"Actions>Exec>Arguments"`
		Interval string `xml:"Settings>RestartOnFailure>Interval"`
	}
	reader := unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder().Reader(bytes.NewReader(encoded))
	if err = xml.NewDecoder(reader).Decode(&task); err != nil {
		t.Fatal(err)
	}
	if task.Command != exe || !strings.Contains(task.Args, n.Config) || task.Interval != "PT1M" {
		t.Fatalf("task import changed Unicode paths or has an unsupported restart interval: %+v", task)
	}
	if err = os.WriteFile(p.Path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if present, err := p.definitionPresent(); err != nil || !present {
		t.Fatal("published task cannot be verified for repeated enable/disable", present, err)
	}
	encoded[len(encoded)-2] ^= 1
	if err = os.WriteFile(p.Path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = p.definitionPresent(); err == nil {
		t.Fatal("modified scheduled task accepted as owned")
	}
}
