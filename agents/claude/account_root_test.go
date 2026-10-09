package claude

import (
	"context"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"testing"
)

func TestAccountRejectsWrongReportedRoot(t *testing.T) {
	fh, h, in, _ := setup(t)
	fh.Programs["claude"] = func(_ []string, _ agent.RunOptions) agent.Result {
		return agent.Result{Stdout: []byte(`{"loggedIn":true,"email":"wrong@example.com","configDirectory":"/another/profile"}`)}
	}
	if _, err := New().Account(context.Background(), h, in); err == nil {
		t.Fatal("accepted a sibling profile's identity")
	}
	fh.Programs["claude"] = func(_ []string, _ agent.RunOptions) agent.Result {
		return agent.Result{Code: 1, Stdout: []byte(`{"loggedIn":false,"authMethod":"none"}`)}
	}
	a, err := New().Account(context.Background(), h, in)
	if err != nil || a.LoggedIn {
		t.Fatal(a, err)
	}
}
