package cloudintegration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/packaging"
)

func TestBootstrapSurfacesDoNotInventConnectedStateOrGuaranteedStartSkills(t *testing.T) {
	for _, provider := range []string{"claude-hosted", "codex-current", "codex-legacy", "work-cloud"} {
		p, err := Plan(provider, "0.5.0-alpha.1", DownloadOrigin)
		if err != nil {
			t.Fatal(err)
		}
		if p.Connected || p.CallbackSupported != (provider == "claude-hosted") || !strings.Contains(p.InstallScript, "openssl dgst -sha256 -verify") || !strings.Contains(p.InstallScript, packaging.ReleasePublicKey) || strings.Contains(p.InstallScript, "curl -L") {
			t.Fatal("unsafe or invented bootstrap capability", provider, p)
		}
	}
	for _, value := range []struct{ provider, version, origin string }{{"unknown", "0.5.0", DownloadOrigin}, {"claude-hosted", "latest", DownloadOrigin}, {"claude-hosted", "0.4.0", DownloadOrigin}, {"claude-hosted", "0.5.0", "http://example.com"}, {"claude-hosted", "0.5.0", "https://user:password@example.com"}, {"claude-hosted", "0.5.0", "https://example.com?secret=x"}, {"claude-hosted", "0.5.0", "https://example.com/redirect"}, {"claude-hosted", "0.5.0", "https://host$(touch_marker).example"}} {
		if _, err := Plan(value.provider, value.version, value.origin); err == nil {
			t.Fatal("unsafe bootstrap input accepted", value)
		}
	}
}

func TestGeneratedBootstrapVerifiesSignatureAndArchiveBeforeInstalling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("provider environment installation is POSIX; surface contracts are cross-platform")
	}
	for _, program := range []string{"curl", "openssl", "tar", "awk", "sh"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skip("POSIX bootstrap prerequisites missing", program)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
	var archive bytes.Buffer
	z := gzip.NewWriter(&archive)
	tw := tar.NewWriter(z)
	binary := []byte("#!/bin/sh\nprintf '%s\\n' 'verified fixture'\n")
	if err = tw.WriteHeader(&tar.Header{Name: "hopsesh", Mode: 0755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err = tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = z.Close()
	name := fmt.Sprintf("hopsesh_0.5.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive.Bytes())
	manifest := []byte(fmt.Sprintf("%x  %s\n", sum, name))
	mh := sha256.Sum256(manifest)
	signature, err := ecdsa.SignASN1(rand.Reader, key, mh[:])
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"valid", "bad-signature", "bad-archive"} {
		t.Run(scenario, func(t *testing.T) {
			sig, data := bytes.Clone(signature), bytes.Clone(archive.Bytes())
			if scenario == "bad-signature" {
				sig[0] ^= 1
			}
			if scenario == "bad-archive" {
				data[len(data)-1] ^= 1
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/releases/v0.5.0/checksums.txt":
					_, _ = w.Write(manifest)
				case "/releases/v0.5.0/checksums.txt.sig":
					_, _ = w.Write(sig)
				case "/releases/v0.5.0/" + name:
					_, _ = w.Write(data)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			home := t.TempDir()
			ca := filepath.Join(home, "fixture-ca.pem")
			if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := Plan("claude-hosted", "0.5.0", server.URL)
			if err != nil {
				t.Fatal(err)
			}
			// Ephemeral trust root is substituted only inside the test.
			script := strings.Replace(p.InstallScript, packaging.ReleasePublicKey, publicPEM, 1)
			cmd := exec.Command("sh")
			cmd.Stdin = strings.NewReader(script)
			cmd.Env = append(os.Environ(), "HOME="+home, "CURL_CA_BUNDLE="+ca)
			output, runErr := cmd.CombinedOutput()
			path := filepath.Join(home, ".local", "share", "hopsesh", "cloud", "v0.5.0", "hopsesh")
			installed, readErr := os.ReadFile(path)
			if scenario == "valid" {
				if runErr != nil || readErr != nil || !bytes.Equal(installed, binary) {
					t.Fatalf("verified bootstrap failed: %v %v\n%s", runErr, readErr, output)
				}
			} else if runErr == nil || !os.IsNotExist(readErr) {
				t.Fatalf("unverified archive installed: %v %v\n%s", runErr, readErr, output)
			}
		})
	}
}
