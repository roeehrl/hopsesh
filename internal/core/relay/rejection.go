package relay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

type rejectedDelivery struct{ cause error }

func (r rejectedDelivery) Error() string { return r.cause.Error() }
func (r rejectedDelivery) Unwrap() error { return r.cause }
func reject(err error) error             { return rejectedDelivery{cause: err} }
func permanent(err error) bool           { var r rejectedDelivery; return errors.As(err, &r) }

// Quarantine contains routing, a ciphertext digest and the local rejection reason. An invalid or
// revoked message must not permanently block unrelated later deliveries.
func (s Store) Quarantine(ctx context.Context, e Envelope, cause error) error {
	return s.withLock(ctx, func() error {
		paths, err := filepath.Glob(filepath.Join(s.Directory, "rejected-*.json"))
		if err != nil {
			return err
		}
		for _, path := range paths {
			body, err := localstate.ReadPrivateFile(path, 8192)
			if err != nil {
				return err
			}
			var old struct {
				Envelope Envelope `json:"envelope"`
			}
			if err = json.Unmarshal(body, &old); err != nil {
				return err
			}
			if old.Envelope.Expires <= time.Now().Unix() {
				if err = os.Remove(path); err != nil {
					return err
				}
			}
		}
		paths, err = filepath.Glob(filepath.Join(s.Directory, "rejected-*.json"))
		if err != nil {
			return err
		}
		if len(paths) >= 1000 {
			return errors.New("relay rejection quota reached; inspect rejected delivery receipts")
		}
		key := replyKey(e.From, e.ID, e.Operation)
		digest := sha256.Sum256(e.Ciphertext)
		e.Ciphertext = nil // rejection inspection never needs a full untrusted blob
		return writeJSON(filepath.Join(s.Directory, "rejected-"+key+".json"), struct {
			Envelope         Envelope `json:"envelope"`
			CiphertextDigest string   `json:"ciphertextDigest"`
			Reason           string   `json:"reason"`
		}{e, hex.EncodeToString(digest[:]), cause.Error()})
	})
}
