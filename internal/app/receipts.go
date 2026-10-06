package app

import (
	"context"
	"fmt"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
)

// RecoverReceipts retries only the durable metadata acknowledgments of one journal.
// Native destination guards remain mandatory, and an undone journal cannot be revived.
func (a *App) RecoverReceipts(ctx context.Context, id string) error {
	j, err := journal.Load(a.StateDir, id)
	if err != nil {
		return err
	}
	if j.Undone {
		return fmt.Errorf("this operation was undone; receipts cannot be retried")
	}
	machines := map[string]*host.Machine{}
	defer func() {
		for _, m := range machines {
			m.Close()
		}
	}()
	err = j.RecoverReceipts(func(name string) (host.FS, error) {
		if name == LocalName() {
			return host.LocalFS(), nil
		}
		m := machines[name]
		if m == nil {
			h := a.Cfg.FindHost(name)
			if h == nil {
				return nil, fmt.Errorf("%s is not a configured machine", name)
			}
			var e error
			m, e = a.Connect(ctx, *h)
			if e != nil {
				return nil, e
			}
			machines[name] = m
		}
		return m.FS(ctx)
	})
	if err != nil {
		return err
	}
	return nil
}
