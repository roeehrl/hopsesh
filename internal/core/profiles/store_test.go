package profiles

import (
	"encoding/json"
	"fmt"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"sync"
	"testing"
	"time"
)

func TestProfilesTagsIdentityAndConcurrentRegistration(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Register(agent.RuntimeProfile{Endpoint: "endpoint", Agent: "claude", Name: "Personal", Root: fmt.Sprintf("/home/alice/account-%d", i), Tags: []string{" Personal ", "personal", "Side   projects"}})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	ps, err := s.List()
	if err != nil || len(ps) != 16 {
		t.Fatalf("concurrent profiles lost: %d %v", len(ps), err)
	}
	p := ps[0]
	if len(p.Tags) != 2 || p.Tags[0] != "Personal" || p.Tags[1] != "Side projects" {
		t.Fatalf("tags %+v", p.Tags)
	}
	if _, err := s.Register(p); err == nil {
		t.Fatal("duplicate root accepted")
	}
	if err := s.Edit(p.ID, "Two personal accounts are allowed", []string{"Work"}, p.Generation); err != nil {
		t.Fatal(err)
	}
	if err := s.Edit(p.ID, "lost update", nil, p.Generation); err == nil {
		t.Fatal("stale edit accepted")
	}
}
func TestObservedBindingsDoNotTreatEmailAsIdentity(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	p, err := s.Register(agent.RuntimeProfile{Endpoint: "one", Agent: "codex", Name: "Research", Root: "/home/alice/research"})
	if err != nil {
		t.Fatal(err)
	}
	acct := &agent.Account{Email: "alice@example.com", Provider: "chatgpt", Confidence: "limited", LoggedIn: true, Observation: "visible-one"}
	one, err := s.Observe(p.ID, acct, "")
	if err != nil {
		t.Fatal(err)
	}
	same, _ := s.Observe(p.ID, acct, "")
	if same.Binding != one.Binding || same.Generation != one.Generation || same.Account.Confidence != "limited" {
		t.Fatal("unchanged observation claimed a new or verified identity")
	}
	changed := *acct
	changed.Observation = "visible-two"
	two, _ := s.Observe(p.ID, &changed, "")
	if two.Binding == one.Binding || two.Generation == one.Generation {
		t.Fatal("visible login change did not invalidate plans")
	}
	stale, _ := s.Observe(p.ID, nil, "unreachable")
	if stale.Binding != two.Binding || stale.Error != "unreachable" {
		t.Fatal("failed observation reset the binding")
	}
}

func TestRemoteObservationKeepsLocalPreferencesAndIdentity(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	p, err := s.Register(agent.RuntimeProfile{ID: "profile", Endpoint: "remote", Agent: "codex", Root: "/home/alice/research", Name: "My label", Tags: []string{"Personal"}})
	if err != nil {
		t.Fatal(err)
	}
	remote := p
	remote.Name = "Remote label"
	remote.Tags = []string{"Private tag"}
	remote.Binding = "owner-binding"
	remote.CheckedAt = time.Now().UTC()
	remote.Account = &agent.Account{Key: "private-key", Email: "alice@example.com", Label: "Private label", Provider: "chatgpt", LoggedIn: true, Observation: "public-fingerprint", Confidence: "limited"}
	b, _ := json.Marshal(disk{Version: 1, Profiles: []agent.RuntimeProfile{remote}})
	decoded, err := DecodeRegistrations(b)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded[0]
	if len(got.Tags) > 0 || got.Account.Key != "" || got.Account.Email != remote.Account.Email || got.Account.Label != remote.Account.Label || got.Binding != remote.Binding {
		t.Fatalf("unexpected discovery: %+v", got)
	}
	if err = s.ImportObservation(p.ID, got); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.List()
	current := ps[0]
	if current.Name != p.Name || len(current.Tags) != 1 || current.Tags[0] != "Personal" || current.Binding != "owner-binding" || current.Generation != p.Generation+1 {
		t.Fatalf("import overwrote local state: %+v", current)
	}
	got.CheckedAt = got.CheckedAt.Add(-time.Hour)
	got.Binding = "stale"
	if err = s.ImportObservation(p.ID, got); err != nil {
		t.Fatal(err)
	}
	ps, _ = s.List()
	if ps[0].Binding != current.Binding {
		t.Fatal("stale owner observation reset binding")
	}
	got.Endpoint = "different"
	if err = s.ImportObservation(p.ID, got); err == nil {
		t.Fatal("accepted different endpoint")
	}
}

func TestChangingConfiguredDefaultPreservesIdentityAndInvalidatesPlans(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	a, err := s.Register(agent.RuntimeProfile{Endpoint: "endpoint", Agent: "codex", Name: "Personal", Root: "/home/alice/.codex", Default: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Register(agent.RuntimeProfile{Endpoint: "endpoint", Agent: "codex", Name: "Another personal", Root: "/home/alice/other"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDefault("endpoint", "codex", b.ID); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.List()
	if ps[0].Default || !ps[1].Default || ps[0].Generation != a.Generation+1 || ps[1].Generation != b.Generation+1 || ps[0].ID != a.ID || ps[1].ID != b.ID {
		t.Fatalf("default switch: %+v", ps)
	}
	if err = s.SetDefault("endpoint", "codex", b.ID); err != nil {
		t.Fatal(err)
	}
	same, _ := s.List()
	if same[1].Generation != ps[1].Generation {
		t.Fatal("unchanged default invalidated plans")
	}
}

func TestFailedOwnerObservationPreservesPublicIdentityAndSanitizesError(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	p, err := s.Register(agent.RuntimeProfile{Endpoint: "remote", Agent: "claude", Root: "/home/user/.claude", Name: "My personal"})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.ObserveFrom(p.ID, &agent.Account{Email: "personal@example.com", LoggedIn: true, Observation: "one"}, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	remote := p
	remote.CheckedAt = p.CheckedAt.Add(time.Second)
	remote.Error = "a private owner-machine error"
	b, _ := json.Marshal(disk{Version: 1, Profiles: []agent.RuntimeProfile{remote}})
	decoded, err := DecodeRegistrations(b)
	if err != nil {
		t.Fatal(err)
	}
	if decoded[0].Error == remote.Error || decoded[0].Error == "" {
		t.Fatal("owner error not safely surfaced")
	}
	if err := s.ImportObservation(p.ID, decoded[0]); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.List()
	if ps[0].Account.Email != p.Account.Email || ps[0].Error == "" || ps[0].Binding != p.Binding {
		t.Fatal("failed owner check lost last known identity")
	}
}
