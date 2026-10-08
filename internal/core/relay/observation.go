package relay

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/observe"
)

// ObservationLease bounds source-issued remote inventory. Local presence keeps its
// shorter lease. Transport activity never renews either kind of evidence.
const ObservationLease = 6 * time.Minute
const ObservationRenew = 5 * time.Minute
const MaxObservationBytes = 1 << 20

func checkObservation(s observe.Snapshot, now time.Time) error {
	if !opaque(s.Epoch) || s.Sequence == 0 || s.AttemptedAt.IsZero() || s.AttemptedAt.After(now.Add(time.Minute)) || s.ObservedAt.After(now.Add(time.Minute)) || s.ExpiresAt.After(s.ObservedAt.Add(ObservationLease)) || len(s.Data) > MaxObservationBytes || !json.Valid(s.Data) {
		return errors.New("invalid source observation")
	}
	return nil
}

// PublishObservation sends a replaceable inventory snapshot, never an operation.
// There is no per-update recovery record or native action to replay. The caller
// must have projected Data using this exact current incoming observe grant.
func (s *Service) PublishObservation(ctx context.Context, expected Grant, snapshot observe.Snapshot) error {
	if err := checkObservation(snapshot, time.Now()); err != nil {
		return err
	}
	body, err := json.Marshal(snapshot)
	if err != nil || len(body) > MaxObservationBytes {
		return errors.New("source observation exceeds delivery bound")
	}
	op, err := NewOperationID()
	if err != nil {
		return err
	}
	var envelope Envelope
	err = s.Processor.Store.withLock(ctx, func() error {
		current, err := s.Processor.Store.grantLocked(expected.Peer.ID)
		if err != nil {
			return err
		}
		if current.Kind != "device" || !current.Allows("observe", time.Now()) || !reflect.DeepEqual(current, expected) {
			return ErrRevoked
		}
		ttl := ObservationLease
		if current.Expires != 0 {
			ttl = min(ttl, time.Until(time.Unix(current.Expires, 0)))
		}
		if ttl < time.Second {
			return ErrRevoked
		}
		envelope, err = sealKind(s.Processor.Identity, current.Peer, s.Processor.Space, op, "observation", body, time.Now(), ttl)
		return err
	})
	if err != nil {
		return err
	}
	return s.Transport.Submit(ctx, envelope)
}

func (s *Service) receiveObservation(ctx context.Context, envelope Envelope) error {
	if envelope.Kind != "observation" || len(envelope.Ciphertext) > MaxObservationBytes+4096 {
		return reject(errors.New("invalid observation envelope"))
	}
	grant, err := s.Processor.Store.Grant(ctx, envelope.From)
	if err != nil {
		if errors.Is(err, ErrRevoked) {
			return reject(err)
		}
		return err
	}
	if grant.Kind != "device" || !grant.AllowsSend("observe", time.Now()) {
		return reject(ErrRevoked)
	}
	body, err := Open(s.Processor.Identity, grant.Peer, envelope, s.Processor.Space, time.Now())
	if err != nil {
		return reject(err)
	}
	var snapshot observe.Snapshot
	if len(body) > MaxObservationBytes || json.Unmarshal(body, &snapshot) != nil {
		return reject(errors.New("invalid observation payload"))
	}
	if err = checkObservation(snapshot, time.Now()); err != nil {
		return reject(err)
	}
	return s.Processor.Store.withLock(ctx, func() error {
		current, err := s.Processor.Store.grantLocked(envelope.From)
		if err != nil {
			if errors.Is(err, ErrRevoked) {
				return reject(err)
			}
			return err
		}
		if !reflect.DeepEqual(current, grant) || !current.AllowsSend("observe", time.Now()) || current.Expires != 0 && envelope.Expires > current.Expires {
			return reject(ErrRevoked)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.observations == nil {
			s.observations = map[string]observe.Snapshot{}
		}
		for key, old := range s.observations {
			if old.AttemptedAt.Add(ObservationLease).Before(time.Now()) {
				delete(s.observations, key)
			}
		}
		old, exists := s.observations[envelope.From]
		// Duplicate, reordered and expired delivery can be acknowledged without
		// publishing it. Retain original source times across owner restarts too.
		if snapshot.AttemptedAt.Add(ObservationLease).Before(time.Now()) || exists && (snapshot.AttemptedAt.Before(old.AttemptedAt) || snapshot.Epoch == old.Epoch && snapshot.Sequence <= old.Sequence || snapshot.Epoch != old.Epoch && !snapshot.AttemptedAt.After(old.AttemptedAt)) {
			return nil
		}
		if !exists && len(s.observations) >= 4096 {
			return reject(errors.New("observation peer limit reached"))
		}
		if s.OnObservation != nil {
			if err = s.OnObservation(ctx, current, snapshot); err != nil {
				return err
			}
		}
		snapshot.Data = nil // replay protection retains no inventory or transcript
		s.observations[envelope.From] = snapshot
		s.health.ObservationReceived++
		return nil
	})
}
