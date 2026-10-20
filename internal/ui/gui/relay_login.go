package gui

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/relay"
)

type relayLoginState struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// RelayLogin keeps all OAuth secrets in the backend, and opens only the
// validated approval page in the external browser. One login per app at a time.
func (a *App) RelayLogin(origin string) error {
	a.relayLogin.mu.Lock()
	if a.relayLogin.cancel != nil {
		a.relayLogin.mu.Unlock()
		return errors.New("a relay login is already awaiting browser approval")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	a.relayLogin.cancel = cancel
	a.relayLogin.mu.Unlock()
	defer func() {
		cancel()
		a.relayLogin.mu.Lock()
		a.relayLogin.cancel = nil
		a.relayLogin.mu.Unlock()
	}()
	core := a.snapshot()
	store := relay.Store{Directory: filepath.Join(core.StateDir, "relay")}
	public, err := store.Public()
	if err != nil {
		return errors.New("create this endpoint's identity before connecting")
	}
	identity, err := store.Identity(ctx, public.Endpoint)
	if err != nil {
		return err
	}
	connection, err := (relay.Enrollment{Origin: origin}).Browser(ctx, identity, openInBrowser)
	if err != nil {
		if ctx.Err() != nil {
			return errors.New("relay login canceled or expired; start again when ready")
		}
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = store.SetConnection(ctx, connection); err != nil {
		return err
	}
	if err = a.RelayEnable(true); err != nil {
		return errors.New("relay credential saved, but delivery could not be enabled; reload settings and enable it")
	}
	return nil
}

func (a *App) RelayCancelLogin() {
	a.relayLogin.mu.Lock()
	defer a.relayLogin.mu.Unlock()
	if a.relayLogin.cancel != nil {
		a.relayLogin.cancel()
	}
}
