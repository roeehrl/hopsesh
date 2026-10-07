package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
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
	fi, err := os.Lstat(c.CAFile)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return nil, errors.New("relay trust root must be a bounded regular PEM file")
	}
	data, err := os.ReadFile(c.CAFile)
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
	if !opaque(c.Device) || c.Expires <= time.Now().Unix() {
		return errors.New("invalid or expired relay credential")
	}
	if _, err := (Transport{Base: c.URL, Space: c.Space, Token: c.Token}).endpoint("/v1/messages"); err != nil {
		return err
	}
	if _, err := c.HTTPClient(); err != nil {
		return err
	}
	return s.withLock(ctx, func() error { return writeJSON(filepath.Join(s.Directory, "connection.json"), c) })
}
func (s Store) Connection(ctx context.Context) (Connection, error) {
	var c Connection
	err := s.withLock(ctx, func() error {
		path := filepath.Join(s.Directory, "connection.json")
		b, err := os.ReadFile(path)
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

type Listener struct {
	Transport  Transport
	Processor  Processor
	Notify     func(error)
	OnResponse func(context.Context, Envelope) error
}

// Run uses one adaptive HTTP poll for all local clients. Delivery acknowledgments
// follow durable native outcomes and encrypted reply publication, not receipt of
// a request alone. Source observation timestamps are never renewed by transport.
func (l Listener) Run(ctx context.Context) error {
	var cursor uint64
	delay := time.Second
	for {
		batch, err := l.Transport.Poll(ctx, cursor)
		if err == nil {
			for _, d := range batch.Messages {
				if d.Envelope.Kind == "response" {
					if l.OnResponse == nil {
						err = errors.New("relay has no reply dispatcher")
						break
					}
					if err = l.OnResponse(ctx, d.Envelope); err != nil {
						break
					}
					cursor = d.Sequence
					continue
				}
				response, e := l.Processor.Process(ctx, d.Envelope, time.Now())
				if e == nil {
					e = l.Transport.Submit(ctx, response)
				}
				if e != nil {
					err = e
					break
				}
				cursor = d.Sequence
			}
			if err == nil && batch.Cursor > cursor {
				cursor = batch.Cursor
			}
			if err == nil && cursor > 0 {
				err = l.Transport.Ack(ctx, cursor)
			}
		}
		if l.Notify != nil {
			l.Notify(err)
		}
		if err == nil && len(batch.Messages) > 0 {
			delay = time.Second
		} else {
			delay = min(60*time.Second, delay*2)
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
