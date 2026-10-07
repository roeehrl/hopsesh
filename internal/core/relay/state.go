package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/localstate"
)

var ErrUncertain = errors.New("relay operation was started before interruption; inspect its durable native journal before retrying")
var ErrRevoked = errors.New("relay peer is not approved or its grant expired")

// Grant is local authorization. Cloud session grants cannot receive, undo or
// administer a machine. Root scopes are enforced again by the movement planner.
type Grant struct {
	Peer        PublicIdentity `json:"peer"`
	Endpoint    string         `json:"endpoint,omitempty"`
	Roots       []string       `json:"roots,omitempty"`
	Kind        string         `json:"kind"` // device or cloud-session
	Methods     []string       `json:"methods"`
	SendMethods []string       `json:"sendMethods,omitempty"` // requests this device may send; never grants incoming access
	Expires     int64          `json:"expires,omitempty"`
	Revoked     bool           `json:"revoked"`
}

func (g Grant) Allows(method string, now time.Time) bool {
	return g.allows(method, now, g.Methods)
}

func (g Grant) AllowsSend(method string, now time.Time) bool {
	return g.allows(method, now, g.SendMethods)
}

func (g Grant) allows(method string, now time.Time, methods []string) bool {
	if g.Revoked || g.Expires != 0 && g.Expires <= now.Unix() {
		return false
	}
	if g.Kind != "device" && g.Kind != "cloud-session" {
		return false
	}
	if g.Kind == "cloud-session" && method != "observe" && method != "export" {
		return false
	}
	for _, m := range methods {
		if m == method {
			return true
		}
	}
	return false
}

type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}
type Reply struct {
	Method  string  `json:"method"`
	Outcome Outcome `json:"outcome"`
}
type Outcome struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}
type Record struct {
	Authorization string           `json:"authorization,omitempty"`
	Peer          string           `json:"peer"`
	Operation     string           `json:"operation"`
	Method        string           `json:"method"`
	Request       []byte           `json:"request"`         // SHA256, never plaintext
	Scope         []byte           `json:"scope,omitempty"` // source endpoint/root authorization at acceptance
	Phase         string           `json:"phase"`
	Response      Envelope         `json:"response"`
	Outcome       *retainedOutcome `json:"outcome,omitempty"` // encrypted to this owner; allows reply renewal without another native action
	Expires       int64            `json:"expires"`
}

// Store persists operation intent before running any native action. It fails
// closed after a crash, rather than repeating an uncertain vendor creation.
type Store struct{ Directory string }

func (s Store) withLock(ctx context.Context, f func() error) error {
	if err := os.MkdirAll(s.Directory, 0700); err != nil {
		return err
	}
	if err := localstate.PrivateDirectory(s.Directory); err != nil {
		return err
	}
	l, err := localstate.Lock(ctx, filepath.Join(s.Directory, "state.lock"))
	if err != nil {
		return err
	}
	defer l.Close()
	return f()
}
func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".relay-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	return os.Rename(f.Name(), path)
}
func (s Store) Identity(ctx context.Context, endpoints ...string) (Identity, error) {
	var i Identity
	err := s.withLock(ctx, func() error {
		path := filepath.Join(s.Directory, "identity.json")
		b, err := localstate.ReadPrivateFile(path, MaxWireBytes)
		if err == nil {
			if err = json.Unmarshal(b, &i); err != nil {
				return err
			}
			return i.Check()
		}
		if !os.IsNotExist(err) {
			return err
		}
		i, err = GenerateIdentity(endpoints...)
		if err != nil {
			return err
		}
		return writeJSON(path, i)
	})
	return i, err
}
func (s Store) Approve(ctx context.Context, g Grant) error {
	if err := g.Peer.Check(); err != nil {
		return err
	}
	if g.Kind != "device" && g.Kind != "cloud-session" {
		return errors.New("unknown relay grant kind")
	}
	if len(g.Methods) == 0 && len(g.SendMethods) == 0 {
		return errors.New("relay grant has no methods")
	}
	for _, m := range append(append([]string(nil), g.Methods...), g.SendMethods...) {
		switch m {
		case "hello", "plan", "apply", "undo", "observe", "export", "ack":
		default:
			return fmt.Errorf("unsupported relay method %q", m)
		}
		if g.Kind == "cloud-session" && m != "observe" && m != "export" {
			return errors.New("cloud session cannot receive or administer devices")
		}
	}
	if g.Kind == "cloud-session" && (g.Expires <= time.Now().Unix() || g.Expires > time.Now().Add(MaxLifetime).Unix()) {
		return errors.New("cloud session grant must expire within 24 hours")
	}
	return s.withLock(ctx, func() error { return writeJSON(filepath.Join(s.Directory, "peer-"+g.Peer.ID+".json"), g) })
}
func (s Store) Grant(ctx context.Context, id string) (Grant, error) {
	var g Grant
	if !opaque(id) {
		return g, ErrRevoked
	}
	err := s.withLock(ctx, func() error {
		b, err := localstate.ReadPrivateFile(filepath.Join(s.Directory, "peer-"+id+".json"), 8192)
		if os.IsNotExist(err) {
			return ErrRevoked
		}
		if err != nil {
			return err
		}
		if err = json.Unmarshal(b, &g); err != nil {
			return err
		}
		if g.Peer.ID != id {
			return ErrRevoked
		}
		return g.Peer.Check()
	})
	return g, err
}
func (s Store) Revoke(ctx context.Context, id string) error {
	if !opaque(id) {
		return ErrRevoked
	}
	return s.withLock(ctx, func() error {
		path := filepath.Join(s.Directory, "peer-"+id+".json")
		b, err := localstate.ReadPrivateFile(path, MaxWireBytes)
		if err != nil {
			return err
		}
		var g Grant
		if err = json.Unmarshal(b, &g); err != nil {
			return err
		}
		g.Revoked = true
		return writeJSON(path, g)
	})
}
