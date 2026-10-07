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
	"time"
)

const MaxOperationRecords = 10000

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
		return Envelope{}, errors.New("a relay reply is not a new native action")
	}
	grant, err := p.Store.Grant(ctx, e.From)
	if err != nil {
		return Envelope{}, err
	}
	plain, err := Open(p.Identity, grant.Peer, e, p.Space, now)
	if err != nil {
		return Envelope{}, err
	}
	var req Request
	if err = json.Unmarshal(plain, &req); err != nil {
		return Envelope{}, err
	}
	if !grant.Allows(req.Method, now) {
		return Envelope{}, ErrRevoked
	}
	if p.Handle == nil {
		return Envelope{}, errors.New("relay receiver unavailable")
	}
	var response Envelope
	handle := p.Handle
	err = p.Store.withLock(ctx, func() error {
		// Re-read authorization under the same lock as durable intent. Revocation
		// before action cannot race an earlier authorization read.
		b, err := os.ReadFile(filepath.Join(p.Store.Directory, "peer-"+e.From+".json"))
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
		if !current.Allows(req.Method, now) {
			return ErrRevoked
		}
		digest := sha256.Sum256(plain)
		key := sha256.Sum256([]byte(e.From + "\x00" + e.Operation + "\x00" + req.Method))
		path := filepath.Join(p.Store.Directory, "operation-"+hex.EncodeToString(key[:])+".json")
		var record Record
		b, err = os.ReadFile(path)
		if err == nil {
			if err = json.Unmarshal(b, &record); err != nil {
				return err
			}
			if !bytes.Equal(record.Request, digest[:]) {
				return errors.New("relay operation ID reused for different input")
			}
			if record.Phase == "completed" {
				if record.Response.Expires <= now.Unix() {
					return errors.New("operation completed; reply retention expired; inspect its native journal")
				}
				response = record.Response
				return nil
			}
			if p.Recover == nil {
				return ErrUncertain
			}
			handle = p.Recover
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		entries, err := filepath.Glob(filepath.Join(p.Store.Directory, "operation-*.json"))
		if err != nil {
			return err
		}
		if len(entries) >= MaxOperationRecords {
			return errors.New("relay operation quota reached; archive receipts before accepting new operations")
		}
		record = Record{Peer: e.From, Operation: e.Operation, Method: req.Method, Request: digest[:], Phase: "started", Expires: e.Expires}
		if err = writeJSON(path, record); err != nil {
			return err
		}
		result, actionErr := handle(ctx, current, e.Operation, req.Method, req.Params)
		out := Outcome{}
		if actionErr != nil {
			out.Error = actionErr.Error()
		} else {
			out.Result, err = json.Marshal(result)
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
		record.Phase, record.Response = "completed", response
		return writeJSON(path, record)
	})
	return response, err
}
