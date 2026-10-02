package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
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
