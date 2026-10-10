package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/cloudintegration"
	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	localruntime "github.com/roeehrl/hopsesh/internal/core/runtime"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

func (a *App) cloudConnectorCall(ctx context.Context, id, method string, out any) (relay.Grant, error) {
	var empty relay.Grant
	cfg, err := config.Load()
	if err != nil {
		return empty, err
	}
	if !cfg.Relay.Enabled {
		return empty, errors.New("internet delivery is disabled")
	}
	grant, err := (relay.Store{Directory: filepath.Join(a.StateDir, "relay")}).Grant(ctx, id)
	if err != nil {
		return empty, err
	}
	if grant.Kind != "cloud-session" || !grant.AllowsSend(method, time.Now()) {
		return empty, errors.New("this cloud incarnation is not approved for the requested access")
	}
	operation, err := relay.NewOperationID()
	if err != nil {
		return empty, err
	}
	namespace, err := localruntime.NewNamespace(config.Dir(), a.StateDir)
	if err != nil {
		return empty, err
	}
	err = (localruntime.Client{Namespace: namespace}).Call(ctx, "relay.call", struct {
		Peer      string          `json:"peer"`
		Operation string          `json:"operation"`
		Method    string          `json:"method"`
		Params    json.RawMessage `json:"params"`
	}{id, operation, method, nil}, out)
	if errors.Is(err, os.ErrNotExist) {
		return grant, fmt.Errorf("the local background runtime is unavailable; open Hopsesh or run 'hopsesh runtime start' with the same settings and state directories: %w", err)
	}
	return grant, err
}

// CloudConnectorObservation is an explicit, read-only request to one approved
// session. Merely opening settings never polls or exports a conversation.
func (a *App) CloudConnectorObservation(ctx context.Context, id string) (cloudintegration.Observation, error) {
	var out cloudintegration.Observation
	grant, err := a.cloudConnectorCall(ctx, id, "observe", &out)
	if err != nil {
		return out, err
	}
	if err = out.Check(grant.Peer, time.Now()); err != nil {
		return cloudintegration.Observation{}, err
	}
	if err = a.checkCloudTask(ctx, grant, out); err != nil {
		return cloudintegration.Observation{}, err
	}
	out.ExportAllowed = out.ExportAllowed && grant.AllowsSend("export", time.Now())
	return out, nil
}

type CloudConnectorPreview struct {
	cloudintegration.Observation
	Checkpoint cloudintegration.Checkpoint `json:"checkpoint"`
	SHA256     string                      `json:"sha256"`
	Preview    agent.Preview               `json:"preview"`
}

func (a *App) CloudConnectorConversation(ctx context.Context, id string) (CloudConnectorPreview, error) {
	var out cloudintegration.Export
	grant, err := a.cloudConnectorCall(ctx, id, "export", &out)
	if err != nil {
		return CloudConnectorPreview{}, err
	}
	if err = out.Check(grant.Peer, time.Now()); err != nil {
		return CloudConnectorPreview{}, err
	}
	if err = a.checkCloudTask(ctx, grant, out.Observation); err != nil {
		return CloudConnectorPreview{}, err
	}
	moduleID := agent.ID("claude")
	if out.Provider == "codex-legacy" {
		moduleID = "codex"
	}
	module, ok := a.Module(moduleID)
	if !ok {
		return CloudConnectorPreview{}, errors.New("the native agent module is disabled")
	}
	previewer, ok := module.(agent.Previewer)
	if !ok {
		return CloudConnectorPreview{}, ErrNoPreview
	}
	native := path.Join("/hopsesh-cloud-checkpoint", out.Session+".jsonl")
	snapshot := host.NewSnapshot("cloud-checkpoint", host.Facts{OS: "linux", Home: "/hopsesh-cloud-checkpoint"}, []host.SnapshotFile{{Path: native, Data: out.Data, Mode: 0600}})
	install := agent.Install{Agent: moduleID, Present: true, Roots: map[string]string{"home": "/hopsesh-cloud-checkpoint"}}
	h, err := snapshot.For(ctx, module.Spec(), install, nil)
	if err != nil {
		return CloudConnectorPreview{}, err
	}
	preview, err := previewer.Preview(ctx, h, install, agent.Summary{Key: agent.SessionKey{Agent: moduleID, Session: agent.SessionID(out.Session)}, Path: native, CWD: out.Workspace}, 20)
	if err != nil {
		return CloudConnectorPreview{}, err
	}
	body, err := json.Marshal(preview)
	if err != nil || len(body) > 512<<10 {
		return CloudConnectorPreview{}, errors.New("cloud conversation preview exceeds its bounded display limit")
	}
	return CloudConnectorPreview{Observation: out.Observation, Checkpoint: out.Checkpoint, SHA256: out.SHA256, Preview: preview}, nil
}

func (a *App) checkCloudTask(ctx context.Context, grant relay.Grant, observation cloudintegration.Observation) error {
	if observation.Task == nil {
		return nil
	}
	store := relay.Store{Directory: filepath.Join(a.StateDir, "relay")}
	self, err := store.Public()
	if err != nil {
		return err
	}
	if observation.Task.Verify(self.ID) != nil {
		return errors.New("cloud task provenance belongs to another native issuer")
	}
	c, err := store.Connection(ctx)
	if err != nil {
		return err
	}
	return (relay.AdmissionStore{Directory: filepath.Join(a.StateDir, "cloud-admissions")}).ResolveTask(ctx, c, observation.Admission, *observation.Task, grant.Peer, observation.Provider, observation.Session, observation.Generation)
}
