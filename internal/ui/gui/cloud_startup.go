package gui

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/relay"
)

type cloudStartupState struct {
	plan    *cloudintegration.RepositorySetup
	id      string
	expires time.Time
}
type CloudStartupPreviewDTO struct {
	ID    string                            `json:"id"`
	Setup *cloudintegration.RepositorySetup `json:"setup"`
}

func (a *App) CloudStartupPreview(provider, version, origin, repository string) (CloudStartupPreviewDTO, error) {
	repo, err := filepath.Abs(repository)
	if err != nil {
		return CloudStartupPreviewDTO{}, err
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		return CloudStartupPreviewDTO{}, err
	}
	plan, err := cloudintegration.PlanRepository(provider, version, origin, repo)
	if err != nil {
		return CloudStartupPreviewDTO{}, err
	}
	id, err := relay.NewOperationID()
	if err != nil {
		return CloudStartupPreviewDTO{}, err
	}
	a.mu.Lock()
	a.cloudStartup = cloudStartupState{plan: plan, id: id, expires: time.Now().Add(10 * time.Minute)}
	a.mu.Unlock()
	return CloudStartupPreviewDTO{ID: id, Setup: plan}, nil
}

func (a *App) CloudStartupApply(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.cloudStartup
	if state.plan == nil || state.id != id || !state.expires.After(time.Now()) {
		return errors.New("cloud startup preview expired or changed; prepare it again")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := state.plan.Apply(ctx); err != nil {
		return err
	}
	a.cloudStartup = cloudStartupState{}
	return nil
}
