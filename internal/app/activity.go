package app

import (
	"errors"
	"sync"
)

type runtimeActivity struct {
	mu       sync.Mutex
	active   int
	draining bool
}

func (a *App) beginRuntimeAction() (func(), error) {
	if a.activity == nil {
		return func() {}, nil
	}
	a.activity.mu.Lock()
	defer a.activity.mu.Unlock()
	if a.activity.draining {
		return nil, errors.New("runtime is draining; reconnect before starting a transfer")
	}
	a.activity.active++
	return func() { a.activity.mu.Lock(); a.activity.active--; a.activity.mu.Unlock() }, nil
}
func (a *App) drainRuntime() error {
	if a.activity == nil {
		return nil
	}
	a.activity.mu.Lock()
	defer a.activity.mu.Unlock()
	if a.activity.active > 0 {
		return errors.New("runtime has an active transfer; finish it before stopping or changing owners")
	}
	a.activity.draining = true
	return nil
}
