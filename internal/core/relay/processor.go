package relay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

const MaxOperationRecords = 10000

func grantScope(g Grant) []byte {
	roots := slices.Clone(g.Roots)
	slices.Sort(roots)
	body, _ := json.Marshal(struct {
		Kind, Endpoint, Fingerprint string
		Roots                       []string
	}{g.Kind, g.Endpoint, g.Peer.Fingerprint(), roots})
	digest := sha256.Sum256(body)
	return digest[:]
}

type Handler func(context.Context, Grant, string, string, json.RawMessage) (any, error)
type Processor struct {
	Identity Identity
	Space    string
	Store    Store
	Handle   Handler
	Recover  Handler // explicit native-journal recovery, never generic re-execution
}

func (p Processor) Process(ctx context.Context, e Envelope, now time.Time) (Envelope, error) {
	if e.Kind != "request" {
		return Envelope{}, reject(errors.New("a relay reply is not a new native action"))
	}
	grant, err := p.Store.Grant(ctx, e.From)
	if err != nil {
		if errors.Is(err, ErrRevoked) {
			err = reject(err)
		}
		return Envelope{}, err
	}
	plain, err := Open(p.Identity, grant.Peer, e, p.Space, now)
	if err != nil {
		return Envelope{}, reject(err)
	}
	var req Request
	if err = json.Unmarshal(plain, &req); err != nil {
		return Envelope{}, reject(err)
	}
	permission, chunk, err := blobPermission(req, grant, now)
	if err != nil {
		return Envelope{}, reject(err)
	}
	if !grant.Allows(permission, now) {
		return Envelope{}, reject(ErrRevoked)
	}
	if p.Handle == nil {
		return Envelope{}, errors.New("relay receiver unavailable")
	}
	var response Envelope
	handle := p.Handle
	err = p.Store.withLock(ctx, func() error {
		// Re-read authorization under the same lock as durable intent. Revocation
		// before action cannot race an earlier authorization read.
		b, err := localstate.ReadPrivateFile(filepath.Join(p.Store.Directory, "peer-"+e.From+".json"), 8192)
		if err != nil {
			return err
		}
		var current Grant
		if err = json.Unmarshal(b, &current); err != nil {
			return err
		}
		if current.Peer.ID != grant.Peer.ID || current.Peer.Recipient != grant.Peer.Recipient || !bytes.Equal(current.Peer.Signing, grant.Peer.Signing) {
			return errors.New("relay peer keys changed during authorization")
		}
		if !current.Allows(permission, now) {
			return reject(ErrRevoked)
		}
		scope := grantScope(current)
		canonical := req
		if req.Method == "blob.invoke" {
			// Missing or corrupt uploads have not started a native action. Do
			// not create its replay tombstone until the complete object verifies.
			assembled, err := p.Store.assemble(e.From, chunk.Descriptor)
			if err != nil {
				return reject(err)
			}
			canonical = Request{Method: permission, Params: assembled}
		}
		canonicalBytes, err := json.Marshal(canonical)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(canonicalBytes)
		key := sha256.Sum256([]byte(e.From + "\x00" + e.Operation + "\x00" + canonical.Method))
		path := filepath.Join(p.Store.Directory, "operation-"+hex.EncodeToString(key[:])+".json")
		var record Record
		b, err = localstate.ReadPrivateFile(path, MaxWireBytes)
		newRecord := os.IsNotExist(err)
		if err == nil {
			if err = json.Unmarshal(b, &record); err != nil {
				return err
			}
			if !bytes.Equal(record.Request, digest[:]) {
				return reject(errors.New("relay operation ID reused for different input"))
			}
			if !bytes.Equal(record.Scope, scope) {
				return reject(errors.New("relay operation authorization scope changed; use a new operation ID"))
			}
			if record.Phase == "completed" {
				if record.Response.Expires > now.Unix() && record.Method == req.Method {
					response = record.Response
					return nil
				}
				if record.Outcome == nil {
					return errors.New("completed operation has no retained outcome; inspect its native journal")
				}
				retained, err := record.Outcome.open(p.Identity, e.From, e.Operation, digest[:], now)
				if err != nil {
					return errors.New("completed outcome retention expired or damaged; inspect its native journal; native action was not repeated")
				}
				var out Outcome
				if err = json.Unmarshal(retained, &out); err != nil {
					return err
				}
				var ref blobResult
				if json.Unmarshal(out.Result, &ref) == nil && ref.Descriptor != nil && ref.Descriptor.Expires <= now.Unix() {
					return errors.New("completed large reply retention expired; inspect its native journal; native action was not repeated")
				}
				body, err := json.Marshal(Reply{Method: req.Method, Outcome: out})
				if err != nil {
					return err
				}
				response, err = sealKind(p.Identity, grant.Peer, p.Space, e.Operation, "response", body, now, time.Duration(e.Expires-now.Unix())*time.Second)
				if err != nil {
					return err
				}
				record.Method, record.Response, record.Expires = req.Method, response, e.Expires
				if err = p.Store.recoverySpace(path, MaxWireBytes, now); err != nil {
					return err
				}
				return writeRecoveryRecord(path, record)
			}
			if p.Recover == nil && req.Method != "blob.put" && req.Method != "blob.read" {
				return ErrUncertain
			}
			if p.Recover != nil {
				handle = p.Recover
			}
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		entries, err := filepath.Glob(filepath.Join(p.Store.Directory, "operation-*.json"))
		if err != nil {
			return err
		}
		if newRecord && len(entries) >= MaxOperationRecords {
			return errors.New("relay operation quota reached; archive receipts before accepting new operations")
		}
		if err = p.Store.recoverySpace(path, MaxWireBytes, now); err != nil {
			return err
		}
		record = Record{Peer: e.From, Operation: e.Operation, Method: req.Method, Request: digest[:], Scope: scope, Phase: "started", Expires: e.Expires}
		if err = writeRecoveryRecord(path, record); err != nil {
			return err
		}
		result, actionErr := p.chunkHandle(ctx, current, e.Operation, req, chunk, handle)
		out := Outcome{}
		if actionErr != nil {
			out.Error = actionErr.Error()
		} else {
			out.Result, err = json.Marshal(result)
			if err != nil {
				return err
			}
			resultMethod := permission
			if req.Method == "blob.read" {
				resultMethod = req.Method
			}
			out.Result, err = p.chunkResult(e.From, e.Operation, resultMethod, out.Result, e.Expires)
			if err != nil {
				return err
			}
		}
		body, err := json.Marshal(Reply{Method: req.Method, Outcome: out})
		if err != nil {
			return err
		}
		response, err = sealKind(p.Identity, grant.Peer, p.Space, e.Operation, "response", body, now, time.Duration(e.Expires-now.Unix())*time.Second)
		if err != nil {
			return err
		}
		retained, err := json.Marshal(out)
		if err != nil {
			return err
		}
		var sealed *retainedOutcome
		if canonical.Method != "blob.read" {
			sealed, err = retainOutcome(p.Identity, e.From, e.Operation, digest[:], retained, now)
			if err != nil {
				return err
			}
		}
		record.Phase, record.Response, record.Outcome = "completed", response, sealed
		return writeRecoveryRecord(path, record)
	})
	return response, err
}
