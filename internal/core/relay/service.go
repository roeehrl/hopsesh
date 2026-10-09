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

	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

// Service multiplexes replies and incoming requests through the owner's single
// mailbox listener. Opening another GUI or CLI creates no extra relay poller.
type Service struct {
	Transport     Transport
	Processor     Processor
	Notify        func(error)
	OnObservation func(context.Context, Grant, observe.Snapshot) error
	health        Health
	mu            sync.Mutex
	waiters       map[string]chan struct{}
	activity      chan struct{}
	observations  map[string]observe.Snapshot
}

func replyKey(peer, op, method string) string {
	digest := sha256.Sum256([]byte(peer + "\x00" + op + "\x00" + method))
	return hex.EncodeToString(digest[:])
}

func wireMethodAuthorized(method, permission string) bool {
	return permission != "" && (method == permission || method == "blob.put" || method == "blob.invoke" || method == "blob.read")
}

func (s *Service) receive(ctx context.Context, e Envelope) error {
	if e.Kind != "response" {
		return reject(errors.New("relay request is not a reply"))
	}
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
	if !wireMethodAuthorized(reply.Method, permission) {
		return reject(errors.New("relay reply has no request authorization"))
	}
	if !grant.AllowsSend(permission, time.Now()) {
		return reject(ErrRevoked)
	}
	if err = s.Processor.Store.withLock(ctx, func() error {
		// Approval can change while decryption waits for the durable writer.
		// Re-read both the current scope and intent under that same lock.
		current, err := s.Processor.Store.grantLocked(e.From)
		if err != nil {
			if errors.Is(err, ErrRevoked) {
				return reject(err)
			}
			return err
		}
		if !current.AllowsSend(permission, time.Now()) || current.Peer.Fingerprint() != grant.Peer.Fingerprint() {
			return reject(ErrRevoked)
		}
		body, err := localstate.ReadPrivateFile(filepath.Join(s.Processor.Store.Directory, "outgoing-"+key+".json"), 8192)
		if err != nil {
			return err
		}
		var currentIntent Record
		if err = json.Unmarshal(body, &currentIntent); err != nil {
			return err
		}
		if currentIntent.Peer != e.From || currentIntent.Operation != e.Operation || currentIntent.Method != reply.Method || currentIntent.Authorization != permission || !bytes.Equal(currentIntent.Scope, grantScope(current)) || currentIntent.Expires <= time.Now().Unix() || e.Expires > currentIntent.Expires || current.Expires != 0 && e.Expires > current.Expires {
			return reject(errors.New("relay reply is outside its current request authorization"))
		}
		path := filepath.Join(s.Processor.Store.Directory, "reply-"+key+".json")
		encoded, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err = s.Processor.Store.recoverySpace(path, int64(len(encoded)), time.Now()); err != nil {
			return err
		}
		return writeJSON(path, e)
	}); err != nil {
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
	ObservationReceived uint64    `json:"observationReceived"`
	ObservationSent     uint64    `json:"observationSent"`
	ObservationFailed   uint64    `json:"observationFailed"`
	ObservationError    string    `json:"observationError,omitempty"`
	DeliveryMode        string    `json:"deliveryMode"`
	Connected           bool      `json:"connected"`
	LastSuccess         time.Time `json:"lastSuccess"`
	Error               string    `json:"error,omitempty"`
	Rejected            uint64    `json:"rejected"`
}

func (s *Service) ObservationDelivery(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.health.ObservationFailed++
	} else {
		s.health.ObservationSent++
	}
}

func (s *Service) ObservationProblem(message string) {
	s.mu.Lock()
	s.health.ObservationError = message
	s.mu.Unlock()
}

func (s *Service) Health() Health { s.mu.Lock(); defer s.mu.Unlock(); return s.health }

func (s *Service) requestActivity() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activity == nil {
		s.activity = make(chan struct{}, 1)
	}
	return s.activity
}

func (s *Service) Run(ctx context.Context) error {
	return (Listener{Transport: s.Transport, Processor: s.Processor, OnResponse: s.receive, OnObservation: s.receiveObservation, RequestActivity: s.requestActivity(), ActiveRequests: func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.waiters) > 0
	}, OnDeliveryMode: func(mode string) { s.mu.Lock(); s.health.DeliveryMode = mode; s.mu.Unlock() }, OnRejected: func() { s.mu.Lock(); s.health.Rejected++; s.mu.Unlock() }, Notify: func(err error) {
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
	if !wireMethodAuthorized(method, permission) {
		return nil, errors.New("relay wire method does not match request authorization")
	}
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
	if transientOperation(method) {
		ttl = 90 * time.Second
	}
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
		current, e := s.Processor.Store.grantLocked(peer)
		if e != nil {
			return e
		}
		if !current.AllowsSend(permission, time.Now()) || !bytes.Equal(grantScope(current), grantScope(grant)) || current.Expires != 0 && expiry > current.Expires {
			return ErrRevoked
		}
		scope := grantScope(current)
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
			if old.Peer != peer || old.Operation != operation || old.Method != method || old.Authorization != permission || !bytes.Equal(old.Scope, scope) {
				return errors.New("relay request authorization changed; use a new operation ID")
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
			expiry = old.Expires
			return nil
		}
		if !os.IsNotExist(e) {
			return e
		}
		if e := s.Processor.Store.recoveryRecordSlot("outgoing-", time.Now()); e != nil {
			return e
		}
		out := Record{Transient: transientOperation(method), Peer: peer, Operation: operation, Method: method, Authorization: permission, Scope: scope, Request: digest[:], Phase: "submitted", Expires: expiry}
		encoded, e := json.Marshal(out)
		if e != nil {
			return e
		}
		if e = s.Processor.Store.recoverySpace(path, int64(len(encoded)), time.Now()); e != nil {
			return e
		}
		return writeRecoveryRecord(path, out)
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
	select {
	case s.requestActivity() <- struct{}{}:
	default:
	}
	lease := time.NewTimer(time.Until(time.Unix(expiry, 0)))
	defer lease.Stop()
	for {
		var envelope Envelope
		path := filepath.Join(s.Processor.Store.Directory, "reply-"+key+".json")
		b, err := localstate.ReadPrivateFile(path, MaxWireBytes)
		if err == nil {
			current, checkErr := s.Processor.Store.Grant(ctx, peer)
			if checkErr != nil || !current.AllowsSend(permission, time.Now()) || current.Peer.Fingerprint() != grant.Peer.Fingerprint() || !bytes.Equal(grantScope(current), grantScope(grant)) {
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
		case <-lease.C:
			return nil, errors.New("relay request lease expired; retry the operation to recover its outcome")
		case <-ch:
		}
	}
}
