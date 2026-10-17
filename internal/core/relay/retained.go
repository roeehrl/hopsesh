package relay

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"time"

	"filippo.io/age"
)

// retainedOutcome is local ciphertext, never a routable relay frame. Signing
// binds it to the exact canonical operation, preventing result substitution.
type retainedOutcome struct {
	Peer       string `json:"peer"`
	Operation  string `json:"operation"`
	Request    []byte `json:"request"`
	Expires    int64  `json:"expires"`
	Ciphertext []byte `json:"ciphertext"`
	Signature  []byte `json:"signature,omitempty"`
}

func (r retainedOutcome) signedBytes() []byte {
	r.Signature = nil
	b, _ := json.Marshal(r)
	return append([]byte("hopsesh-private-outcome-v1\x00"), b...)
}
func retainOutcome(id Identity, peer, op string, request, body []byte, now time.Time) (*retainedOutcome, error) {
	k, err := age.ParseX25519Recipient(id.Public.Recipient)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, k)
	if err != nil {
		return nil, err
	}
	if _, err = w.Write(body); err != nil {
		return nil, err
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	r := &retainedOutcome{Peer: peer, Operation: op, Request: bytes.Clone(request), Expires: now.Add(MaxLifetime).Unix(), Ciphertext: buf.Bytes()}
	r.Signature = ed25519.Sign(id.Signing, r.signedBytes())
	return r, nil
}
func (r retainedOutcome) open(id Identity, peer, op string, request []byte, now time.Time) ([]byte, error) {
	if r.Peer != peer || r.Operation != op || !bytes.Equal(r.Request, request) || r.Expires <= now.Unix() || !ed25519.Verify(id.Public.Signing, r.signedBytes(), r.Signature) {
		return nil, errors.New("completed outcome expired, damaged or outside this operation")
	}
	k, err := age.ParseX25519Identity(id.Encryption)
	if err != nil {
		return nil, err
	}
	reader, err := age.Decrypt(bytes.NewReader(r.Ciphertext), k)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(reader, ChunkBytes+8193))
	if err != nil {
		return nil, err
	}
	if len(b) > ChunkBytes+8192 {
		return nil, errors.New("retained outcome exceeds bound")
	}
	return b, nil
}
