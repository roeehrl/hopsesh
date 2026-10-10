package cloudintegration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/relay"
)

func TestCloudExportSealsCompleteRecordsAndReportsPartialFinalWrite(t *testing.T) {
	parent, scope := sessionFixture(t)
	s, err := Begin(t.Context(), parent, scope, "manual", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	peer, _ := relay.GenerateIdentity("approved-device")
	grant := relay.Grant{Peer: peer.Public, Kind: "cloud-session", Methods: []string{"export"}, Expires: time.Now().Add(time.Hour).Unix()}
	prefix := []byte("{\"sessionId\":\"abc123\",\"message\":\"complete\"}\n")
	partial := []byte("{\"sessionId\":\"abc123\",\"message\":")
	if err = os.WriteFile(scope.Transcript, append(bytes.Clone(prefix), partial...), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := s.Handler(t.Context(), grant, "checkpoint-operation", "export", nil)
	if err != nil {
		t.Fatal(err)
	}
	exported := value.(Export)
	sum := sha256.Sum256(prefix)
	if !bytes.Equal(exported.Data, prefix) || exported.SHA256 != hex.EncodeToString(sum[:]) || exported.Checkpoint.Records != 1 || exported.Checkpoint.OmittedTail != int64(len(partial)) || exported.Checkpoint.ReadBytes != int64(len(prefix)+len(partial)) {
		t.Fatal("partial record was exported or not disclosed")
	}
	completed := append(bytes.Clone(prefix), []byte("{\"sessionId\":\"abc123\",\"message\":\"later\"}\n")...)
	if err = os.WriteFile(scope.Transcript, completed, 0600); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(exported.Data, prefix) {
		t.Fatal("sealed checkpoint changed after vendor continuation")
	}
	value, err = s.Handler(t.Context(), grant, "next-checkpoint", "export", nil)
	if err != nil || !bytes.Equal(value.(Export).Data, completed) || value.(Export).Checkpoint.Records != 2 || value.(Export).Checkpoint.OmittedTail != 0 {
		t.Fatal("completed continuation was not available on next export", err)
	}
}

func TestCloudCheckpointRejectsMalformedCompletedRecordsAndEmptyPrefixes(t *testing.T) {
	for _, body := range [][]byte{nil, []byte(" \n"), []byte(`{"message":`), []byte("{bad}\n{\"ok\":true}\n"), []byte("[]\n"), []byte("null\n")} {
		if _, _, err := sealCheckpoint(body); err == nil {
			t.Fatal("invalid or empty native checkpoint accepted")
		}
	}
	for _, body := range [][]byte{[]byte("{\"ok\":true}\n\n"), []byte(`{"ok":true}`), []byte("{\"ok\":true}\r\n")} {
		data, checkpoint, err := sealCheckpoint(body)
		if err != nil || !bytes.Equal(data, body) || checkpoint.OmittedTail != 0 || checkpoint.Records != 1 {
			t.Fatal("valid native records were altered", err)
		}
	}
}
