package gui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/secrets"
	"github.com/roeehrl/hopsesh/internal/core/transport"
	"github.com/roeehrl/hopsesh/internal/inventory"
)

// PasswordEvent is the event the window gets when ssh asks for a machine's password.
const PasswordEvent = "hopsesh:password"

// PasswordRequest asks the window for a machine's password.
type PasswordRequest struct {
	ID          string `json:"id"`
	Machine     string `json:"machine"`
	Destination string `json:"destination"`
	Retry       bool   `json:"retry"`       // the last password was refused
	CanRemember bool   `json:"canRemember"` // the Keychain is available
	Remember    bool   `json:"remember"`    // the machine is set to remember it
}

type pwAnswer struct {
	password string
	ok       bool
}

// pwBroker connects ssh's password questions (asked from scans, plans and moves) with the
// window's password dialog. It has its own lock: the questions arrive while App.mu may be
// held by the call that is connecting.
type pwBroker struct {
	mu      sync.Mutex
	prompt  sync.Mutex // one dialog at a time
	pending map[string]chan pwAnswer
	reqs    map[string]PasswordRequest
	cache   map[string]string // destination → password, for this run of the app
	seq     int
	emit    func(PasswordRequest) // tests replace it; nil: the window's event
}

func (a *App) broker() *pwBroker {
	a.pwOnce.Do(func() {
		a.pw = &pwBroker{pending: map[string]chan pwAnswer{}, reqs: map[string]PasswordRequest{}, cache: map[string]string{}}
	})
	return a.pw
}

// passwordFor answers ssh's password question for a machine: from memory, the Keychain,
// or the window's dialog.
func (a *App) passwordFor(h config.Host) transport.PasswordFunc {
	b := a.broker()
	acct := secrets.Account(h.Name, h.Destination)
	return func(ctx context.Context, dest string, retry bool) (string, error) {
		b.prompt.Lock()
		defer b.prompt.Unlock()
		b.mu.Lock()
		if retry {
			delete(b.cache, dest)
		}
		pw := b.cache[dest]
		b.mu.Unlock()
		if retry {
			_ = secrets.Delete(acct) // a refused password must not come back from the Keychain
		}
		if pw != "" {
			return pw, nil
		}
		if h.Keychain && !retry {
			if pw, ok := secrets.Get(acct); ok && pw != "" {
				b.mu.Lock()
				b.cache[dest] = pw
				b.mu.Unlock()
				return pw, nil
			}
		}
		emit := b.emit
		if emit == nil && a.App != nil {
			emit = func(r PasswordRequest) { a.App.Event.Emit(PasswordEvent, r) }
		}
		if emit == nil {
			return "", transport.ErrPasswordCancelled
		}
		b.mu.Lock()
		b.seq++
		req := PasswordRequest{ID: fmt.Sprintf("pw%d", b.seq), Machine: h.Name, Destination: dest, Retry: retry,
			CanRemember: secrets.Available(), Remember: h.Keychain}
		ch := make(chan pwAnswer, 1)
		b.pending[req.ID], b.reqs[req.ID] = ch, req
		b.mu.Unlock()
		defer func() {
			b.mu.Lock()
			delete(b.pending, req.ID)
			delete(b.reqs, req.ID)
			b.mu.Unlock()
		}()
		emit(req)
		select {
		case ans := <-ch:
			if !ans.ok || ans.password == "" {
				return "", transport.ErrPasswordCancelled
			}
			b.mu.Lock()
			b.cache[dest] = ans.password
			b.mu.Unlock()
			return ans.password, nil
		case <-ctx.Done():
			return "", transport.ErrPasswordCancelled
		case <-time.After(10 * time.Minute):
			return "", transport.ErrPasswordCancelled
		}
	}
}

// PendingPasswords lists unanswered password questions (for a window that missed the event).
func (a *App) PendingPasswords() []PasswordRequest {
	b := a.broker()
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []PasswordRequest{}
	for _, r := range b.reqs {
		out = append(out, r)
	}
	return out
}

// ProvidePassword answers a password question. remember stores it in the Keychain and sets
// the machine to remember it; false forgets any remembered one.
func (a *App) ProvidePassword(id, password string, remember bool) error {
	b := a.broker()
	b.mu.Lock()
	ch, req := b.pending[id], b.reqs[id]
	b.mu.Unlock()
	if ch == nil {
		return errors.New("that password question has expired")
	}
	acct := secrets.Account(req.Machine, req.Destination)
	var keyErr error
	if remember && secrets.Available() {
		keyErr = secrets.Set(acct, password)
	} else {
		_ = secrets.Delete(acct)
	}
	select {
	case ch <- pwAnswer{password: password, ok: true}:
	default:
	}
	// The connecting call may hold a.mu; record the choice once it is done.
	go func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if h := a.cfg.FindHost(req.Machine); h != nil && h.Keychain != (remember && keyErr == nil && secrets.Available()) {
			h.Keychain = remember && keyErr == nil && secrets.Available()
			_ = config.Save(a.cfg)
		}
	}()
	return keyErr
}

// CancelPassword declines a password question; that machine is skipped this time.
func (a *App) CancelPassword(id string) {
	b := a.broker()
	b.mu.Lock()
	ch := b.pending[id]
	b.mu.Unlock()
	if ch != nil {
		select {
		case ch <- pwAnswer{}:
		default:
		}
	}
}

// SetAuth switches a machine between key login ("key") and password login ("password").
func (a *App) SetAuth(name, auth string, remember bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.cfg.FindHost(name)
	if h == nil {
		return fmt.Errorf("unknown machine %q", name)
	}
	acct := secrets.Account(h.Name, h.Destination)
	switch auth {
	case "key":
		h.Auth, h.Keychain = "", false
		_ = secrets.Delete(acct)
	case "password":
		h.Auth, h.Keychain = "password", remember && secrets.Available()
		if !h.Keychain {
			_ = secrets.Delete(acct)
		}
	default:
		return fmt.Errorf("unknown login method %q", auth)
	}
	a.forget(h.Destination)
	a.log.Write(audit.Entry{Action: "hosts.auth", Host: h.Name, Detail: map[string]any{"auth": auth, "keychain": h.Keychain}})
	return config.Save(a.cfg)
}

// ForgetPassword removes a machine's remembered password (it is asked for again).
func (a *App) ForgetPassword(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.cfg.FindHost(name)
	if h == nil {
		return fmt.Errorf("unknown machine %q", name)
	}
	a.forget(h.Destination)
	return secrets.Delete(secrets.Account(h.Name, h.Destination))
}

func (a *App) forget(dest string) {
	b := a.broker()
	b.mu.Lock()
	delete(b.cache, dest)
	b.mu.Unlock()
}

// KeyLoginDTO is what SetupKeyLogin did.
type KeyLoginDTO struct {
	PublicKey string `json:"publicKey"`
	Created   bool   `json:"created"`
	Added     bool   `json:"added"`
}

// SetupKeyLogin adds this Mac's public key to a password machine, checks that key login
// works and switches the machine to it. Without createKey it fails with "no-key" when this
// Mac has no key ssh would use (the window then asks before one is made).
func (a *App) SetupKeyLogin(name string, createKey bool) (*KeyLoginDTO, error) {
	a.mu.Lock()
	h := a.cfg.FindHost(name)
	if h == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("unknown machine %q", name)
	}
	hp := *h
	a.mu.Unlock()
	hp.Auth = "password"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := inventory.SetupKeyLogin(ctx, hp, a.passwordFor(hp), createKey, config.StateDir(), a.log)
	if errors.Is(err, inventory.ErrNoLocalKey) {
		return nil, errors.New("no-key")
	}
	if err != nil {
		return nil, err
	}
	if err := a.SetAuth(name, "key", false); err != nil {
		return nil, err
	}
	return &KeyLoginDTO{PublicKey: res.PublicKey, Created: res.Created, Added: res.Added}, nil
}
