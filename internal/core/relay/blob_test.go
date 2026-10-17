package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChunkedNativeRequestRestartRetryAndLargeReply(t *testing.T) {
	a, b := identities(t)
	store := Store{Directory: privateTemp(t)}
	if err := store.Approve(t.Context(), Grant{Peer: a.Public, Kind: "device", Methods: []string{"apply"}}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"transcript": strings.Repeat("private-native-history", ChunkBytes/10)})
	d := descriptor("chunk-native-operation-1234", "apply", "request", body, time.Now().Add(time.Hour).Unix())
	if d.Parts < 2 {
		t.Fatal("fixture should require multiple chunks")
	}
	calls := 0
	handler := func(_ context.Context, _ Grant, _, method string, p json.RawMessage) (any, error) {
		calls++
		if method != "apply" || !bytes.Equal(p, body) {
			t.Fatal("reassembly changed native request")
		}
		return json.RawMessage(body), nil
	}
	processor := Processor{Identity: b, Space: "chunk-test-space-1234", Store: store, Handle: handler, Recover: handler}
	process := func(method, op string, params any) Reply {
		raw, _ := json.Marshal(params)
		req, _ := json.Marshal(Request{Method: method, Params: raw})
		env, err := Seal(a, b.Public, processor.Space, op, req, time.Now(), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		res, err := processor.Process(t.Context(), env, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		plain, err := Open(a, b.Public, res, processor.Space, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var reply Reply
		if err = json.Unmarshal(plain, &reply); err != nil {
			t.Fatal(err)
		}
		return reply
	}
	// An incomplete upload cannot invoke an action. Publication resumes using the
	// same immutable chunks after a receiver restart.
	process("blob.put", chunkOperation(d, "put", 0), blobRequest{Descriptor: d, Data: body[:ChunkBytes]})
	params, _ := json.Marshal(blobRequest{Descriptor: d})
	incomplete, _ := json.Marshal(Request{Method: "blob.invoke", Params: params})
	env, err := Seal(a, b.Public, processor.Space, d.Operation, incomplete, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = processor.Process(t.Context(), env, time.Now()); err == nil {
		t.Fatal("incomplete upload invoked native action")
	}
	if calls != 0 {
		t.Fatal("native action ran before all chunks were available")
	}
	// Completing the missing upload preserves the original operation ID.
	for i := 0; i < d.Parts; i++ {
		process("blob.put", chunkOperation(d, "put", i), blobRequest{Descriptor: d, Index: i, Data: body[i*ChunkBytes : i*ChunkBytes+d.chunkSize(i)]})
	}
	processor = Processor{Identity: b, Space: processor.Space, Store: Store{Directory: store.Directory}, Handle: handler, Recover: handler}
	var ref blobResult
	for range 2 {
		reply := process("blob.invoke", d.Operation, blobRequest{Descriptor: d})
		if reply.Outcome.Error != "" {
			t.Fatal(reply.Outcome.Error)
		}
		if err := json.Unmarshal(reply.Outcome.Result, &ref); err != nil || ref.Descriptor == nil {
			t.Fatal("large reply not chunked", err)
		}
	}
	if calls != 1 {
		t.Fatal("native action repeated after restart/retry", calls)
	}
	var rebuilt []byte
	for i := 0; i < ref.Descriptor.Parts; i++ {
		reply := process("blob.read", chunkOperation(*ref.Descriptor, "read", i), blobRequest{Descriptor: *ref.Descriptor, Index: i})
		var chunk struct {
			Data []byte `json:"data"`
		}
		if err := json.Unmarshal(reply.Outcome.Result, &chunk); err != nil {
			t.Fatal(err)
		}
		rebuilt = append(rebuilt, chunk.Data...)
	}
	if !bytes.Equal(rebuilt, body) {
		t.Fatal("large response changed")
	}
	other, _ := GenerateIdentity()
	if _, err := store.readChunk(other.Public.ID, blobRequest{Descriptor: *ref.Descriptor}); err == nil {
		t.Fatal("another peer read the response")
	}
}

func TestChunksRefuseChangedDataInvalidScopesAndQuotaBypass(t *testing.T) {
	store := Store{Directory: privateTemp(t)}
	body := json.RawMessage(`{"bound":"session"}`)
	d := descriptor("chunk-scope-operation-1234", "export", "request", body, time.Now().Add(time.Hour).Unix())
	b := blobRequest{Descriptor: d, Data: body}
	if err := store.putChunk("approved-peer-1234", b); err != nil {
		t.Fatal(err)
	}
	b.Data = bytes.Repeat([]byte("x"), len(body))
	if err := store.putChunk("approved-peer-1234", b); err == nil {
		t.Fatal("changed chunk accepted")
	}
	if _, err := store.assemble("approved-peer-1234", d); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.blobPath("approved-peer-1234", d), "000.json")
	corrupt, _ := json.Marshal(b.Data)
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.assemble("approved-peer-1234", d); err == nil {
		t.Fatal("corrupted object accepted")
	}
	params, _ := json.Marshal(blobRequest{Descriptor: d, Data: body})
	if _, _, err := blobPermission(Request{Method: "blob.put", Params: params}, Grant{Kind: "cloud-session"}, time.Now()); err == nil {
		t.Fatal("cloud session uploaded a request")
	}
	d.Bytes = MaxObjectBytes + 1
	d.Parts = (d.Bytes + ChunkBytes - 1) / ChunkBytes
	d.ID = d.identity()
	if err := d.check(time.Now()); err == nil {
		t.Fatal("object allocation limit bypassed")
	}
	d = descriptor("chunk-expired-operation-1234", "export", "response", body, time.Now().Add(-time.Second).Unix())
	if err := d.check(time.Now()); err == nil {
		t.Fatal("expired response scope accepted")
	}
}
