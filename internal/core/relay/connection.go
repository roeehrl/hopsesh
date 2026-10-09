package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

// Connection keeps its credential only in the private state namespace. It is
// never returned through settings, status, logs or provider bootstrap scripts.
type Connection struct {
	URL     string `json:"url"`
	Space   string `json:"space"`
	Device  string `json:"device"`
	Token   string `json:"token"`
	Expires int64  `json:"expires"`
	CAFile  string `json:"caFile,omitempty"` // explicit local trust root for private relays; hostname verification remains enabled
}

func (c Connection) HTTPClient() (*http.Client, error) {
	if c.CAFile == "" {
		return nil, nil
	}
	if !filepath.IsAbs(c.CAFile) {
		return nil, errors.New("relay trust root must be an absolute local path")
	}
	data, err := localstate.ReadOwnedFile(c.CAFile, 1<<20)
	if err != nil {
		return nil, err
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}
	if !roots.AppendCertsFromPEM(data) {
		return nil, errors.New("relay trust root contains no certificates")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}, nil
}

func (s Store) SetConnection(ctx context.Context, c Connection) error {
	if !opaque(c.Device) || c.Expires <= time.Now().Unix() || c.Expires > time.Now().Add(MaxLifetime+time.Minute).Unix() || len(c.Token) > 4096 {
		return errors.New("invalid or expired relay credential")
	}
	if _, err := (Transport{Base: c.URL, Space: c.Space, Token: c.Token}).endpoint("/v1/messages"); err != nil {
		return err
	}
	if _, err := c.HTTPClient(); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil || len(data) > 8192 {
		return errors.New("relay credential metadata exceeds limit")
	}
	return s.withLock(ctx, func() error {
		identity, err := s.Public()
		if err != nil || identity.ID != c.Device {
			return errors.New("relay credential does not match this endpoint's current identity")
		}
		return writeJSON(filepath.Join(s.Directory, "connection.json"), c)
	})
}
func (s Store) Connection(ctx context.Context) (Connection, error) {
	var c Connection
	err := s.withLock(ctx, func() error {
		path := filepath.Join(s.Directory, "connection.json")
		b, err := localstate.ReadPrivateFile(path, 8192)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(b, &c); err != nil {
			return err
		}
		if c.Expires <= time.Now().Unix() {
			return errors.New("relay credential expired; enroll again")
		}
		return nil
	})
	return c, err
}

// MaxHTTPReconcileInterval bounds healthy idle reconciliation without a socket.
const MaxHTTPReconcileInterval = time.Minute

type Listener struct {
	Transport       Transport
	Processor       Processor
	Notify          func(error)
	OnResponse      func(context.Context, Envelope) error
	OnObservation   func(context.Context, Envelope) error
	OnRejected      func()
	OnDeliveryMode  func(string)
	RequestActivity <-chan struct{}
	ActiveRequests  func() bool
}

// Run shares one notification stream and HTTP reconciliation across all clients.
// Delivery acknowledgments
// follow durable native outcomes and encrypted reply publication, not receipt of
// a request alone. Source observation timestamps are never renewed by transport.
func (l Listener) Run(ctx context.Context) error {
	child, cancel := context.WithCancel(ctx)
	wake, state, done := make(chan struct{}, 1), make(chan bool, 1), make(chan struct{})
	go func() { defer close(done); l.Transport.notifications(child, wake, state) }()
	defer func() { cancel(); <-done }()
	connected := false
	reportState := func(value bool) {
		connected = value
		if l.OnDeliveryMode != nil {
			mode := "http-fallback"
			if connected {
				mode = "notifications"
			}
			l.OnDeliveryMode(mode)
		}
	}
	var cursor, acknowledged uint64
	idleDelay, failureDelay := time.Second, time.Second
	for {
		// Draining committed batches bypasses the idle wait below. Consume a
		// pending stream transition here too so continuous traffic cannot hide
		// the completed handshake or retain a disconnected stream's mode.
		select {
		case value := <-state:
			reportState(value)
		default:
		}
		batch, err := l.Transport.Poll(ctx, cursor)
		if err == nil {
			for _, d := range batch.Messages {
				if d.Envelope.Kind == "response" || d.Envelope.Kind == "observation" {
					dispatch := l.OnResponse
					if d.Envelope.Kind == "observation" {
						dispatch = l.OnObservation
					}
					if dispatch == nil {
						err = errors.New("relay has no reply dispatcher")
						break
					}
					if err = dispatch(ctx, d.Envelope); err != nil {
						if !permanent(err) {
							break
						}
						// Rejected replaceable inventory has no operation to recover.
						// Count and acknowledge it without letting a revoked sender
						// consume durable native-action quarantine slots forever.
						if d.Envelope.Kind != "observation" {
							if err = l.Processor.Store.Quarantine(ctx, d.Envelope, err); err != nil {
								break
							}
						}
						err = nil
						if l.OnRejected != nil {
							l.OnRejected()
						}
					}
					cursor = d.Sequence
					continue
				}
				response, e := l.Processor.Process(ctx, d.Envelope, time.Now())
				if e == nil {
					e = l.Transport.Submit(ctx, response)
				}
				if e != nil {
					if permanent(e) {
						if err = l.Processor.Store.Quarantine(ctx, d.Envelope, e); err != nil {
							break
						}
						if l.OnRejected != nil {
							l.OnRejected()
						}
						cursor = d.Sequence
						continue
					}
					err = e
					break
				}
				cursor = d.Sequence
			}
			if err == nil && batch.Cursor > cursor {
				cursor = batch.Cursor
			}
			if err == nil && cursor > acknowledged {
				err = l.Transport.Ack(ctx, cursor)
				if err == nil {
					acknowledged = cursor
				}
			}
		}
		if l.Notify != nil {
			l.Notify(err)
		}
		var wait time.Duration
		if err == nil {
			// Successful empty polls are healthy, not failed attempts. Keep
			// their energy-saving cadence separate from error recovery so one
			// transient failure after idle does not inherit a minute of backoff.
			failureDelay = time.Second
			if len(batch.Messages) > 0 {
				idleDelay = time.Second
				continue // Drain committed batches without adding per-message latency.
			}
			idleDelay = min(MaxHTTPReconcileInterval, idleDelay*2)
			wait = idleDelay
		} else {
			failureDelay = min(MaxHTTPReconcileInterval, failureDelay*2)
			wait = retryDelay(failureDelay, err)
		}
		if connected && err == nil {
			wait = 5 * time.Minute
		} else if err == nil && l.ActiveRequests != nil && l.ActiveRequests() {
			// The peer can still be idle, but a sender actively waiting for its
			// reply must not add a second minute of local reconciliation delay.
			wait = min(wait, time.Second)
		}
		t := time.NewTimer(wait)
	waiting:
		for {
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
				break waiting
			case value := <-state:
				reportState(value)
			case <-wake:
			case <-l.RequestActivity:
				if connected {
					continue // Healthy notification streams already signal replies.
				}
			}
			// Notifications are hints, not permission to bypass error backoff or a
			// server Retry-After. Keep consuming them without restarting the timer.
			if err == nil {
				t.Stop()
				break
			}
		}
	}
}
