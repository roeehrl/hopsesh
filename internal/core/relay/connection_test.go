package relay

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitTrustRootUsesBoundedOwnedFileAndVerifiedTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "relay.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	client, err := (Connection{CAFile: path}).HTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal("explicit certificate did not verify TLS", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal(response.StatusCode)
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err == nil {
		if _, err := (Connection{CAFile: link}).HTTPClient(); err == nil {
			t.Fatal("trust root link followed")
		}
	}
	if err := os.WriteFile(path, make([]byte, (1<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Connection{CAFile: path}).HTTPClient(); err == nil {
		t.Fatal("oversized trust root read")
	}
}
