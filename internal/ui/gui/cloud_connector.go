package gui

import (
	"context"
	"errors"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func (a *App) RelayCloudInspect(id string) (cloudintegration.Observation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.snapshot().CloudConnectorObservation(ctx, id)
}

type CloudCheckpointPlanDTO struct {
	Plan       *PlanDTO                    `json:"plan"`
	Checkpoint cloudintegration.Checkpoint `json:"checkpoint"`
	SHA256     string                      `json:"sha256"`
	Operation  string                      `json:"operation"`
}

func (a *App) RelayCloudPlan(id, target, dir, profile, operation string, fork bool) (CloudCheckpointPlanDTO, error) {
	core := a.snapshot()
	a.mu.Lock()
	inv := a.inv
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, in, e, err := core.PlanCloudCheckpoint(ctx, inv, id, agent.ID(target), move.Options{TargetDir: dir, TargetProfile: profile, OperationID: operation, Fork: fork})
	if err != nil {
		return CloudCheckpointPlanDTO{}, err
	}
	a.mu.Lock()
	a.plan, a.input, a.res = p, in, nil
	a.mu.Unlock()
	entry := app.Entry{Agent: in.Source.Module.Spec().ID, AgentName: in.Source.Module.Spec().Name, Machine: in.Source.Machine.Name}
	return CloudCheckpointPlanDTO{Plan: planDTO(p, entry, in.Target.Module), Checkpoint: e.Checkpoint, SHA256: e.SHA256, Operation: p.OperationID}, nil
}

// RelayCloudApply binds the button to the exact reviewed plan, even if another UI surface
// prepared an operation in the meantime. The core rechecks the current grant.
func (a *App) RelayCloudApply(operation string) (*DoneDTO, error) {
	a.mu.Lock()
	p, in := a.plan, a.input
	a.mu.Unlock()
	if p == nil || p.OperationID != operation || in.SourceReceipt == nil {
		return nil, errors.New("cloud checkpoint review changed; prepare it again")
	}
	// Apply uses the shared progress/result path. Native plan identity remains
	// pinned and each write is protected by the core's operation/destination locks.
	return a.applyReviewedPlan(a.snapshot(), p, in)
}
func (a *App) RelayCloudPreview(id string) (app.CloudConnectorPreview, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.snapshot().CloudConnectorConversation(ctx, id)
}
