package codex

import (
	"context"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/agent/agenttest"
	"strings"
	"testing"
)

func TestAccountProtocolWaitsForInitializeAndParsesReplies(t *testing.T) {
	for _, signedIn := range []bool{false, true} {
		h := agenttest.NewFakeHost("/home/u")
		h.AddBinary("codex", "codex-cli 0.160.1")
		m := New()
		in, err := m.Detect(context.Background(), h)
		if err != nil {
			t.Fatal(err)
		}
		h.Programs["codex"] = func(_ []string, o agent.RunOptions) agent.Result {
			if strings.Contains(string(o.Stdin), "account/read") || o.StdinReply == nil {
				t.Fatal("requests sent before initialization")
			}
			if next, done := o.StdinReply([]byte(`{"method":"account/updated","params":{}}`)); len(next) > 0 || done {
				t.Fatal("notification released initialization")
			}
			next, done := o.StdinReply([]byte(`{ "result": {}, "id": 1 }`))
			if done || !strings.Contains(string(next), "account/read") || !strings.Contains(string(next), "initialized") {
				t.Fatal("handshake did not release requests")
			}
			line := `{ "result": {"account":null}, "id":2 }`
			if signedIn {
				line = `{ "result": {"account":{"type":"chatgpt","email":"alice@example.com","planType":"pro"}}, "id":2 }`
			}
			if _, done := o.StdinReply([]byte(line)); !done {
				t.Fatal("response did not finish exchange")
			}
			return agent.Result{Stdout: []byte(line + "\n")}
		}
		a, err := m.Account(context.Background(), h, in)
		if err != nil || a.LoggedIn != signedIn {
			t.Fatalf("%+v %v", a, err)
		}
		if signedIn && (a.Email != "alice@example.com" || a.Confidence != "limited") {
			t.Fatal(a)
		}
	}
}
