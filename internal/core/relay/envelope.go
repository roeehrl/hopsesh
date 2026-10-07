// Package relay carries authenticated, encrypted messages between explicitly
// approved endpoints. Mailbox delivery is not permission to apply a transfer.
package relay

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"filippo.io/age"
)

const Protocol = 1
const MaxPlaintext = 16 << 20
const MaxEnvelope = MaxPlaintext + 1<<20
const MaxLifetime = 24 * time.Hour

// PublicIdentity is approved through an independent fingerprint comparison.
// A relay response can never replace these locally pinned keys.
type PublicIdentity struct {
	Endpoint  string `json:"endpoint,omitempty"`
	ID        string `json:"id"`
	Signing   []byte `json:"signing"`
	Recipient string `json:"recipient"`
}
type Identity struct {
	Public     PublicIdentity     `json:"public"`
	Signing    ed25519.PrivateKey `json:"signing"`
	Encryption string             `json:"encryption"`
}

func GenerateIdentity(endpoints ...string) (Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, err
	}
	k, err := age.GenerateX25519Identity()
	if err != nil {
		return Identity{}, err
	}
	p := PublicIdentity{Signing: pub, Recipient: k.Recipient().String()}
	if len(endpoints) > 0 {
		p.Endpoint = endpoints[0]
	}
	p.ID = p.Fingerprint()
	return Identity{Public: p, Signing: priv, Encryption: k.String()}, nil
}

func NewOperationID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil { return "", err }
	return hex.EncodeToString(id[:]), nil
}
func (p PublicIdentity) Fingerprint() string {
	h := sha256.New()
	h.Write([]byte("hopsesh-relay-identity-v1\x00"))
	h.Write(p.Signing)
	h.Write([]byte(p.Recipient))
	h.Write([]byte("\x00" + p.Endpoint))
	return hex.EncodeToString(h.Sum(nil))
}
func (p PublicIdentity) Check() error {
	if len(p.Signing) != ed25519.PublicKeySize || p.ID != p.Fingerprint() {
		return errors.New("invalid relay identity")
	}
	_, err := age.ParseX25519Recipient(p.Recipient)
	return err
}
func (i Identity) Check() error {
	if err := i.Public.Check(); err != nil {
		return err
	}
	if len(i.Signing) != ed25519.PrivateKeySize || !bytes.Equal(i.Signing.Public().(ed25519.PublicKey), i.Public.Signing) {
		return errors.New("relay signing identity does not match public key")
	}
	k, err := age.ParseX25519Identity(i.Encryption)
	if err != nil {
		return err
	}
	if k.Recipient().String() != i.Public.Recipient {
		return errors.New("relay decryption identity does not match public key")
	}
	return nil
}

// Envelope routing, expiry, operation and ciphertext are all signed together.
// age supplies the reviewed offline recipient encryption construction. Fresh
// encryption randomness is supplied by age for every envelope and retry.
type Envelope struct {
	Kind       string `json:"kind"`
	Protocol   int    `json:"protocol"`
	ID         string `json:"id"`
	Space      string `json:"space"`
	From       string `json:"from"`
	To         string `json:"to"`
	Operation  string `json:"operation"`
	Created    int64  `json:"created"`
	Expires    int64  `json:"expires"`
	Ciphertext []byte `json:"ciphertext"`
	Signature  []byte `json:"signature,omitempty"`
}

func (e Envelope) signedBytes() []byte {
	e.Signature = nil
	b, _ := json.Marshal(e)
	return append([]byte("hopsesh-relay-envelope-v1\x00"), b...)
}
func opaque(s string) bool {
	if len(s) < 16 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func (e Envelope) Check(now time.Time) error {
	if (e.Kind != "request" && e.Kind != "response") || e.Protocol != Protocol || !opaque(e.ID) || !opaque(e.Space) || !opaque(e.Operation) || !opaque(e.From) || !opaque(e.To) || e.From == e.To {
		return errors.New("invalid relay envelope routing")
	}
	if e.Expires <= e.Created || e.Expires-e.Created > int64(MaxLifetime/time.Second) || e.Created > now.Unix()+60 || e.Expires <= now.Unix() {
		return errors.New("expired or invalid relay envelope lifetime")
	}
	if len(e.Ciphertext) == 0 || len(e.Ciphertext) > MaxEnvelope || len(e.Signature) != ed25519.SignatureSize {
		return errors.New("invalid relay envelope size")
	}
	return nil
}
func Seal(sender Identity, recipient PublicIdentity, space, operation string, plaintext []byte, now time.Time, ttl time.Duration) (Envelope, error) {
	return sealKind(sender, recipient, space, operation, "request", plaintext, now, ttl)
}
func sealKind(sender Identity, recipient PublicIdentity, space, operation, kind string, plaintext []byte, now time.Time, ttl time.Duration) (Envelope, error) {
	if err := sender.Check(); err != nil {
		return Envelope{}, err
	}
	if err := recipient.Check(); err != nil {
		return Envelope{}, err
	}
	if len(plaintext) > MaxPlaintext || ttl < time.Second || ttl > MaxLifetime {
		return Envelope{}, errors.New("relay payload or lifetime exceeds limit")
	}
	k, _ := age.ParseX25519Recipient(recipient.Recipient)
	var ciphertext bytes.Buffer
	w, err := age.Encrypt(&ciphertext, k)
	if err != nil {
		return Envelope{}, err
	}
	if _, err = w.Write(plaintext); err != nil {
		return Envelope{}, err
	}
	if err = w.Close(); err != nil {
		return Envelope{}, err
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return Envelope{}, err
	}
	e := Envelope{Kind: kind, Protocol: Protocol, ID: hex.EncodeToString(id[:]), Space: space, From: sender.Public.ID, To: recipient.ID, Operation: operation, Created: now.Unix(), Expires: now.Add(ttl).Unix(), Ciphertext: ciphertext.Bytes()}
	e.Signature = ed25519.Sign(sender.Signing, e.signedBytes())
	if err = e.Check(now); err != nil {
		return Envelope{}, err
	}
	return e, nil
}
func Open(recipient Identity, approved PublicIdentity, e Envelope, space string, now time.Time) ([]byte, error) {
	if err := recipient.Check(); err != nil {
		return nil, err
	}
	if err := approved.Check(); err != nil {
		return nil, err
	}
	if err := e.Check(now); err != nil {
		return nil, err
	}
	if e.Space != space || e.To != recipient.Public.ID || e.From != approved.ID {
		return nil, errors.New("relay envelope is outside the approved peer namespace")
	}
	if !ed25519.Verify(approved.Signing, e.signedBytes(), e.Signature) {
		return nil, errors.New("relay signature verification failed")
	}
	k, _ := age.ParseX25519Identity(recipient.Encryption)
	r, err := age.Decrypt(bytes.NewReader(e.Ciphertext), k)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(r, MaxPlaintext+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxPlaintext {
		return nil, errors.New("decrypted relay payload exceeds limit")
	}
	return b, nil
}
