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
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

// Service multiplexes replies and incoming requests through the owner's single
// mailbox listener. Opening another GUI or CLI creates no extra relay poller.
type Service struct {
	Transport Transport
	Processor Processor
	Notify    func(error)
	health    Health
	mu        sync.Mutex
	waiters   map[string]chan struct{}
}

func replyKey(peer, op, method string) string {
	digest := sha256.Sum256([]byte(peer + "\x00" + op + "\x00" + method))
	return hex.EncodeToString(digest[:])
}
func (s *Service) receive(ctx context.Context, e Envelope) error {
	grant, err := s.Processor.Store.Grant(ctx, e.From)
	if err != nil {
		if errors.Is(err, ErrRevoked) {
			err = reject(err)
		}
		return err
	}
	body, err := Open(s.Processor.Identity, grant.Peer, e, s.Processor.Space, time.Now())
	if err != nil {
		return reject(err)
	}
	var reply Reply
	if err = json.Unmarshal(body, &reply); err != nil {
		return reject(err)
	}
	key := replyKey(e.From, e.Operation, reply.Method)
	outgoing, err := localstate.ReadPrivateFile(filepath.Join(s.Processor.Store.Directory, "outgoing-"+key+".json"), 8192)
	if err != nil {
		if os.IsNotExist(err) {
			return reject(errors.New("unsolicited relay reply"))
		}
		return err
	}
	var intent Record
	if err = json.Unmarshal(outgoing, &intent); err != nil {
		return reject(err)
	}
	permission := intent.Authorization
	if permission == "" {
		permission = reply.Method
	}
	if !grant.AllowsSend(permission, time.Now()) {
		return reject(ErrRevoked)
	}
	if err = s.Processor.Store.withLock(ctx, func() error { return writeJSON(filepath.Join(s.Processor.Store.Directory, "reply-"+key+".json"), e) }); err != nil {
		return err
	}
	s.mu.Lock()
	ch := s.waiters[key]
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return nil
}

type Health struct {
	DeliveryMode string    `json:"deliveryMode"`
	Connected    bool      `json:"connected"`
	LastSuccess  time.Time `json:"lastSuccess"`
	Error        string    `json:"error,omitempty"`
	Rejected     uint64    `json:"rejected"`
}

func (s *Service) Health() Health { s.mu.Lock(); defer s.mu.Unlock(); return s.health }
func (s *Service) Run(ctx context.Context) error {
	return (Listener{Transport: s.Transport, Processor: s.Processor, OnResponse: s.receive, OnDeliveryMode: func(mode string) { s.mu.Lock(); s.health.DeliveryMode = mode; s.mu.Unlock() }, OnRejected: func() { s.mu.Lock(); s.health.Rejected++; s.mu.Unlock() }, Notify: func(err error) {
		s.mu.Lock()
		s.health.Connected = err == nil
		s.health.Error = ""
		if err != nil {
			s.health.Error = err.Error()
		} else {
			s.health.LastSuccess = time.Now().UTC()
		}
		s.mu.Unlock()
		if s.Notify != nil {
			s.Notify(err)
		}
	}}).Run(ctx)
}
func (s *Service) callSmall(ctx context.Context, peer, operation, method, permission string, params json.RawMessage) (json.RawMessage, error) {
	grant, err := s.Processor.Store.Grant(ctx, peer)
	if err != nil {
		return nil, err
	}
	if !grant.AllowsSend(permission, time.Now()) {
		return nil, ErrRevoked
	}
	body, err := json.Marshal(Request{Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	ttl := time.Hour
	if grant.Expires != 0 {
		ttl = min(ttl, time.Until(time.Unix(grant.Expires, 0)))
	}
	if ttl < time.Second {
		return nil, ErrRevoked
	}
	e, err := Seal(s.Processor.Identity, grant.Peer, s.Processor.Space, operation, body, time.Now(), ttl)
	if err != nil {
		return nil, err
	}
	expiry := e.Expires
	key := replyKey(peer, operation, method)
	digest := sha256.Sum256(body)
	if err = s.Processor.Store.withLock(ctx, func() error {
		path := filepath.Join(s.Processor.Store.Directory, "outgoing-"+key+".json")
		prior, e := localstate.ReadPrivateFile(path, MaxWireBytes)
		if e == nil {
			var old Record
			if e = json.Unmarshal(prior, &old); e != nil {
				return e
			}
			if !bytes.Equal(old.Request, digest[:]) {
				return errors.New("relay operation ID reused for different request")
			}
			// Renew only the delivery lease. The peer's canonical operation
			// tombstone still prevents replaying any native action.
			if old.Expires <= time.Now().Unix() {
				if err := os.Remove(filepath.Join(s.Processor.Store.Directory, "reply-"+key+".json")); err != nil && !os.IsNotExist(err) {
					return err
				}
				old.Expires = expiry
				return writeJSON(path, old)
			}
			return nil
		}
		if !os.IsNotExist(e) {
			return e
		}
		records, e := filepath.Glob(filepath.Join(s.Processor.Store.Directory, "outgoing-*.json"))
		if e != nil {
			return e
		}
		if len(records) >= MaxOperationRecords {
			return errors.New("relay outgoing operation quota reached")
		}
		return writeJSON(path, Record{Peer: peer, Operation: operation, Method: method, Authorization: permission, Request: digest[:], Phase: "submitted", Expires: expiry})
	}); err != nil {
		return nil, err
	}

	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if s.waiters == nil {
		s.waiters = map[string]chan struct{}{}
	}
	if _, busy := s.waiters[key]; busy {
		s.mu.Unlock()
		return nil, errors.New("relay request is already in flight")
	}
	s.waiters[key] = ch
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.waiters, key); s.mu.Unlock() }()
	if err = s.Transport.Submit(ctx, e); err != nil {
		return nil, err
	}
	for {
		var envelope Envelope
		path := filepath.Join(s.Processor.Store.Directory, "reply-"+key+".json")
		b, err := localstate.ReadPrivateFile(path, MaxWireBytes)
		if err == nil {
			current, checkErr := s.Processor.Store.Grant(ctx, peer)
			if checkErr != nil || !current.AllowsSend(permission, time.Now()) || current.Peer.Fingerprint() != grant.Peer.Fingerprint() {
				return nil, ErrRevoked
			}
			if err = json.Unmarshal(b, &envelope); err != nil {
				return nil, err
			}
			plain, err := Open(s.Processor.Identity, grant.Peer, envelope, s.Processor.Space, time.Now())
			if err != nil {
				return nil, err
			}
			if envelope.Operation != operation || envelope.Kind != "response" {
				return nil, errors.New("relay reply does not match operation")
			}
			var reply Reply
			if err = json.Unmarshal(plain, &reply); err != nil {
				return nil, err
			}
			if reply.Method != method {
				return nil, errors.New("relay reply method mismatch")
			}
			if reply.Outcome.Error != "" {
				return nil, errors.New(reply.Outcome.Error)
			}
			return reply.Outcome.Result, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
		}
	}
}
