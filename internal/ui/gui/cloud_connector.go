package gui

import (
	"context"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
)

func (a *App) RelayCloudInspect(id string) (cloudintegration.Observation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.snapshot().CloudConnectorObservation(ctx, id)
}
func (a *App) RelayCloudPreview(id string) (app.CloudConnectorPreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.snapshot().CloudConnectorConversation(ctx, id)
}
