package claude

import (
	"context"
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
)

func TestNewSessionCapacityInfersAccountModel(t *testing.T) {
	fh := agenttest.NewFakeHost("/home/alice")
	in := agent.Install{Agent: id, Version: "2.1.288", Roots: map[string]string{"home": "/home/alice/.claude"}}
	h := agent.Confine(fh, New().Spec(), in)
	c, err := New().ContextCapacity(context.Background(), h, in, nil)
	if err != nil || c.Window != 64_000 {
		t.Fatalf("no evidence must keep the conservative fallback: %+v %v", c, err)
	}
	old := time.Now().Add(-time.Hour)
	fh.Put(in.Root(home)+"/projects/-a/old.jsonl", []byte(`{"type":"assistant","message":{"model":"claude-sonnet-4-5"}}`+"\n"), old)
	fh.Put(in.Root(home)+"/projects/-b/new.jsonl", []byte(`{"type":"assistant","message":{"model":"claude-opus-5-5"}}`+"\n"+`{"type":"assistant","message":{"model":"<synthetic>"}}`+"\n"), time.Now())
	c, err = New().ContextCapacity(context.Background(), h, in, nil)
	if err != nil || c.Model != "claude-opus-5-5" || c.Window != 1_000_000 {
		t.Fatalf("newest session's model not used: %+v %v", c, err)
	}
}
