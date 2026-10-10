package update

import (
	"bytes"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Qualifies the real missing-app installer with a locally signed, notarized DMG.
// It never launches the GUI or writes either Applications directory.
func TestInstallSignedMacApp(t *testing.T) {
	directory := os.Getenv("HOPSESH_SIGNED_MAC_APP_DIR")
	version := os.Getenv("HOPSESH_SIGNED_MAC_APP_VERSION")
	if directory == "" || version == "" {
		t.Skip("explicit signed macOS candidate qualification")
	}
	key, err := os.ReadFile("../../packaging/release-key.pub")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(key)
	if block == nil {
		t.Fatal("missing release public key")
	}
	oldKey, oldAPI := PublicKey, API
	PublicKey = base64.StdEncoding.EncodeToString(block.Bytes)
	t.Cleanup(func() { PublicKey, API = oldKey, oldAPI })
	for _, mode := range []string{"new install", "destination appears during download"} {
		t.Run(mode, func(t *testing.T) {
			target := Target{Kind: KindMacApp, Path: filepath.Join(t.TempDir(), "Applications", "hopsesh.app")}
			asset := AssetName(target, version)
			sums, err := os.ReadFile(filepath.Join(directory, "checksums.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = checksumFor(sums, asset); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(target.Path, "independent-install")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := strings.TrimPrefix(r.URL.Path, "/")
				if name != asset && name != "checksums.txt" && name != "checksums.txt.sig" {
					http.NotFound(w, r)
					return
				}
				if name == asset && mode == "destination appears during download" {
					if err := os.MkdirAll(target.Path, 0700); err != nil {
						http.Error(w, "fixture setup", 500)
						return
					}
					if err := os.WriteFile(marker, []byte("keep independent installation"), 0600); err != nil {
						http.Error(w, "fixture setup", 500)
						return
					}
				}
				http.ServeFile(w, r, filepath.Join(directory, name))
			}))
			defer server.Close()
			API = server.URL // Only this loopback fixture bypasses HTTPS, as in other updater tests.
			release := &Release{Tag: "v" + version, Version: version, Assets: map[string]string{}}
			for _, name := range []string{asset, "checksums.txt", "checksums.txt.sig"} {
				release.Assets[name] = server.URL + "/" + name
			}
			err = InstallApp(t.Context(), release, target)
			if mode == "destination appears during download" {
				if err == nil {
					t.Fatal("missing-app installation replaced an independently appearing destination")
				}
				body, readErr := os.ReadFile(marker)
				if readErr != nil || string(body) != "keep independent installation" {
					t.Fatal("refusal did not preserve the independent app", readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cli := filepath.Join(target.Path, "Contents", "Resources", "bin", "hopsesh")
			out, err := exec.CommandContext(t.Context(), cli, "version").CombinedOutput()
			if err != nil || !bytes.Contains(out, []byte(version)) {
				t.Fatal("installed signed CLI did not report the candidate version", err)
			}
			if err = InstallApp(t.Context(), release, target); err == nil {
				t.Fatal("install accepted an existing signed app")
			}
		})
	}
}
