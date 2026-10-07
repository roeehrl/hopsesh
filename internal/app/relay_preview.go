package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/internal/core/relay"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

type relayPreviewRequest struct {
	Key      agent.SessionKey `json:"key"`
	Messages int              `json:"messages"`
}

func (a *App) previewRelay(ctx context.Context, to config.Host, key agent.SessionKey, n int) (agent.Preview, error) {
	if n < 1 || n > 20 {
		return agent.Preview{}, errors.New("relay previews require between one and 20 messages")
	}
	op, err := relay.NewOperationID()
	if err != nil {
		return agent.Preview{}, err
	}
	params, err := json.Marshal(relayPreviewRequest{Key: key, Messages: n})
	if err != nil {
		return agent.Preview{}, err
	}
	var out agent.Preview
	err = a.relayCall(ctx, to, op, "preview", params, &out)
	return out, err
}

// Conversation text is shared only with export approval. An observation grant
// alone cannot read previews, and requesters never supply paths or native roots.
func (a *App) relayPreview(ctx context.Context, grant relay.Grant, params json.RawMessage) (agent.Preview, error) {
	if !grant.Allows("export", time.Now()) {
		return agent.Preview{}, relay.ErrRevoked
	}
	var req relayPreviewRequest
	if len(params) > 4096 {
		return agent.Preview{}, errors.New("preview request exceeds bound")
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return agent.Preview{}, err
	}
	if req.Key.Session == "" || req.Messages < 1 || req.Messages > 20 {
		return agent.Preview{}, errors.New("an exact session key and one to 20 messages are required")
	}
	obs, err := a.ObserveLocal(ctx)
	if err != nil {
		return agent.Preview{}, err
	}
	inv := a.ObservationInventory(ctx, obs)
	defer inv.Close()
	for _, e := range inv.Entries {
		if e.Session.Key != req.Key {
			continue
		}
		if err := withinRelayRoots(e.Session.CWD, grant.Roots); err != nil {
			return agent.Preview{}, err
		}
		out, err := a.Preview(ctx, inv, e, req.Messages)
		if err != nil {
			return agent.Preview{}, err
		}
		b, err := json.Marshal(out)
		if err != nil || len(b) > 512<<10 {
			return agent.Preview{}, errors.New("conversation preview exceeds relay limit")
		}
		return out, nil
	}
	return agent.Preview{}, agent.ErrNotFound
}
