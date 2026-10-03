package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.9", true}, {"v1.0.0", "0.9.9", true}, {"0.1.0", "0.1.0", false},
		{"0.1.0", "0.1.0-rc1", true}, {"0.1.0-rc2", "0.1.0", false}, {"0.1.0", "dev", true},
		{"dev", "0.1.0", false}, {"0.10.0", "0.9.0", true},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestVerifySignature(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	pub := base64.StdEncoding.EncodeToString(der)
	data := []byte("abc  hopsesh_0.1.0_darwin_arm64.tar.gz\n")
	h := sha256.Sum256(data)
	sig, _ := ecdsa.SignASN1(rand.Reader, k, h[:])
	if err := VerifySignature(data, sig, pub); err != nil {
		t.Fatal(err)
	}
	if VerifySignature(append(data, 'x'), sig, pub) == nil {
		t.Fatal("tampered data must fail")
	}
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	od, _ := x509.MarshalPKIXPublicKey(&other.PublicKey)
	if VerifySignature(data, sig, base64.StdEncoding.EncodeToString(od)) == nil {
		t.Fatal("another key must fail")
	}
}

// The release job signs with openssl; make sure that format verifies.
func TestOpenSSLSignatureInterop(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("no openssl")
	}
	dir := t.TempDir()
	keyFile, dataFile, sigFile := filepath.Join(dir, "k.pem"), filepath.Join(dir, "checksums.txt"), filepath.Join(dir, "s")
	if out, err := exec.Command("openssl", "ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", keyFile).CombinedOutput(); err != nil {
		t.Skip(string(out))
	}
	os.WriteFile(dataFile, []byte("deadbeef  x.tar.gz\n"), 0o600)
	if out, err := exec.Command("openssl", "dgst", "-sha256", "-sign", keyFile, "-out", sigFile, dataFile).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	pubPEM, err := exec.Command("openssl", "ec", "-in", keyFile, "-pubout").Output()
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(pubPEM)
	data, _ := os.ReadFile(dataFile)
	sig, _ := os.ReadFile(sigFile)
	if err := VerifySignature(data, sig, base64.StdEncoding.EncodeToString(blk.Bytes)); err != nil {
		t.Fatal(err)
	}
}

func TestChecksumAndExtract(t *testing.T) {
	sums := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  hopsesh_0.1.0_linux_amd64.tar.gz\n")
	if s, err := checksumFor(sums, "hopsesh_0.1.0_linux_amd64.tar.gz"); err != nil || len(s) != 64 {
		t.Fatal(s, err)
	}
	if _, err := checksumFor(sums, "other"); err == nil {
		t.Fatal("missing entry must fail")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"README.md": "r", "hopsesh": "BIN", "hopsesh.exe": "WIN"} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	b, err := extract(buf.Bytes(), "x.tar.gz")
	if err != nil || (string(b) != "BIN" && string(b) != "WIN") {
		t.Fatal(string(b), err)
	}
}

func TestInstallRefusesWithoutKey(t *testing.T) {
	PublicKey = ""
	if _, err := Install(t.Context(), &Release{}); err != ErrNoKey {
		t.Fatalf("got %v", err)
	}
}

func TestTargetFor(t *testing.T) {
	cases := []struct {
		exe, goos string
		want      Target
	}{
		{"/Applications/hopsesh.app/Contents/MacOS/hopsesh-app", "darwin", Target{KindMacApp, "/Applications/hopsesh.app"}},
		{"/Applications/hopsesh.app/Contents/Resources/bin/hopsesh", "darwin", Target{KindMacApp, "/Applications/hopsesh.app"}},
		{"/usr/local/bin/hopsesh", "darwin", Target{KindCLI, "/usr/local/bin/hopsesh"}},
		{"/usr/local/bin/hopsesh", "linux", Target{KindCLI, "/usr/local/bin/hopsesh"}},
	}
	for _, c := range cases {
		if got := targetFor(c.exe, c.goos); got != c.want {
			t.Errorf("%s on %s: %+v, want %+v", c.exe, c.goos, got, c.want)
		}
	}
	// A Windows folder with both programs is the app; a lone hopsesh.exe is the CLI.
	dir := t.TempDir()
	cli := filepath.Join(dir, "hopsesh.exe")
	os.WriteFile(cli, []byte("MZ"), 0o755)
	if got := targetFor(cli, "windows"); got.Kind != KindCLI {
		t.Errorf("lone hopsesh.exe: %+v", got)
	}
	os.WriteFile(filepath.Join(dir, "hopsesh-app.exe"), []byte("MZ"), 0o755)
	if got := targetFor(cli, "windows"); got != (Target{KindWindowsApp, dir}) {
		t.Errorf("app folder: %+v", got)
	}
	if n := AssetName(Target{Kind: KindMacApp}, "0.3.0"); n != "hopsesh-0.3.0-macos-universal.dmg" {
		t.Error(n)
	}
	if n := AssetName(Target{Kind: KindWindowsApp}, "0.3.0"); !strings.HasPrefix(n, "hopsesh-0.3.0-windows-") || !strings.HasSuffix(n, "-app.zip") {
		t.Error(n)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

// Both programs of the Windows app are replaced together, or neither.
func TestInstallWindowsApp(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"hopsesh-app.exe", "hopsesh.exe"} {
		os.WriteFile(filepath.Join(dir, n), []byte("old "+n), 0o755)
	}
	if err := installWindowsApp(dir, zipOf(t, map[string]string{"hopsesh-app.exe": "new app"}), "0.3.0"); err == nil {
		t.Fatal("a zip without hopsesh.exe must be refused")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "hopsesh-app.exe")); string(b) != "old hopsesh-app.exe" {
		t.Fatalf("a refused update changes nothing: %q", b)
	}
	if err := installWindowsApp(dir, zipOf(t, map[string]string{"hopsesh-app.exe": "new app", "hopsesh.exe": "new cli", "LICENSE": "MIT"}), "0.3.0"); err != nil {
		t.Fatal(err)
	}
	for n, want := range map[string]string{"hopsesh-app.exe": "new app", "hopsesh.exe": "new cli"} {
		if b, _ := os.ReadFile(filepath.Join(dir, n)); string(b) != want {
			t.Errorf("%s: %q", n, b)
		}
		if _, err := os.Stat(filepath.Join(dir, n+".new")); err == nil {
			t.Errorf("%s.new left behind", n)
		}
	}
}

// A release file is installed only when it matches checksums.txt and checksums.txt
// carries the release key's signature.
func TestDownloadVerifies(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	PublicKey = base64.StdEncoding.EncodeToString(der)
	defer func() { PublicKey = "" }()
	body := []byte("the app zip")
	sum := sha256.Sum256(body)
	sums := []byte(hex.EncodeToString(sum[:]) + "  hopsesh-0.3.0-windows-amd64-app.zip\n")
	h := sha256.Sum256(sums)
	sig, _ := ecdsa.SignASN1(rand.Reader, key, h[:])
	served := map[string][]byte{"/sums": sums, "/sig": sig, "/file": body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(served[r.URL.Path]) }))
	defer srv.Close()
	oldAPI := API
	API = srv.URL // a local test server may serve plain HTTP
	defer func() { API = oldAPI }()
	rel := &Release{Tag: "v0.3.0", Assets: map[string]string{"checksums.txt": srv.URL + "/sums", "checksums.txt.sig": srv.URL + "/sig",
		"hopsesh-0.3.0-windows-amd64-app.zip": srv.URL + "/file"}}
	got, err := download(t.Context(), rel, "hopsesh-0.3.0-windows-amd64-app.zip")
	if err != nil || string(got) != string(body) {
		t.Fatalf("%q %v", got, err)
	}
	served["/file"] = []byte("tampered")
	if _, err := download(t.Context(), rel, "hopsesh-0.3.0-windows-amd64-app.zip"); err == nil {
		t.Fatal("a file that does not match checksums.txt must be refused")
	}
	served["/file"], served["/sig"] = body, []byte("not a signature")
	if _, err := download(t.Context(), rel, "hopsesh-0.3.0-windows-amd64-app.zip"); err == nil {
		t.Fatal("checksums.txt without a valid signature must be refused")
	}
}
