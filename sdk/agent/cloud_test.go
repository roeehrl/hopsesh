package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// cloudOnly is a module with no data folders that lists one cloud.
type cloudOnly struct {
	NoLocal
	spec Spec
}

func (m cloudOnly) Spec() Spec                                    { return m.spec }
func (m cloudOnly) Detect(context.Context, Host) (Install, error) { return Install{}, nil }
func (cloudOnly) ListCloud(context.Context, Host, Install, CloudQuery) (CloudListing, error) {
	return CloudListing{}, nil
}

func goodCloud() Cloud {
	return Cloud{Name: "jules", Title: "Jules", Driver: "jules", Tested: []string{"1.0"}, Hosts: []string{"github.com"},
		Up: FidBrief, Down: FidText, CodeUp: []CodeWay{ViaBranch}, CodeDown: []CodeWay{ViaPR}, Needs: []Need{NeedGitHub}}
}

func TestCheckClouds(t *testing.T) {
	spec := Spec{ID: "jules", Name: "Jules", Binaries: []Binary{{Name: "jules"}}, Clouds: []Cloud{goodCloud()}}
	if err := CheckClouds(cloudOnly{spec: spec}); err != nil {
		t.Fatal(err)
	}
	if !Has(cloudOnly{spec: spec}, CapCloudList) || Has(cloudOnly{spec: spec}, CapCloudSend) {
		t.Fatal("capabilities come from the cloud interfaces a module implements")
	}
	bad := map[string]func(c *Cloud, s *Spec){
		"must match":            func(c *Cloud, _ *Spec) { c.Name = "Codex cloud" },
		"not one of the Spec's": func(c *Cloud, _ *Spec) { c.Driver = "curl" },
		"fidelity":              func(c *Cloud, _ *Spec) { c.Up = "full" },
		"code way":              func(c *Cloud, _ *Spec) { c.CodeDown = []CodeWay{"email"} },
		"need":                  func(c *Cloud, _ *Spec) { c.Needs = []Need{"luck"} },
		"tested":                func(c *Cloud, _ *Spec) { c.Tested = nil },
		"two clouds":            func(c *Cloud, s *Spec) { s.Clouds = append(s.Clouds, *c) },
		"declares no cloud":     func(_ *Cloud, s *Spec) { s.Clouds = nil },
	}
	for want, change := range bad {
		s := spec
		s.Clouds = []Cloud{goodCloud()}
		change(&s.Clouds[0], &s)
		if len(s.Clouds) == 0 {
			s.Clouds = nil
		}
		err := CheckClouds(cloudOnly{spec: s})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

// A cloud-only module lists no local sessions and moves nothing.
func TestNoLocal(t *testing.T) {
	var m NoLocal
	if l, err := m.List(context.Background(), nil, Install{}); err != nil || len(l.Sessions) != 0 {
		t.Fatal("NoLocal lists nothing")
	}
	if _, err := m.Bundle(context.Background(), nil, Install{}, Summary{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal("NoLocal has no bundles")
	}
	if _, err := m.PlanMove(Install{}, Install{}, Summary{}, Bundle{}, Placement{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal("NoLocal moves nothing")
	}
	if len(m.Resume(Install{}, SessionKey{}, Placement{}, ResumeOptions{}).Argv) != 0 {
		t.Fatal("NoLocal resumes nothing here")
	}
}

// Cloud names read back from the labels older hopsesh versions wrote.
func TestCloudLocationInLegacyLabels(t *testing.T) {
	l := CloudLocation("codex-cloud")
	if !l.IsCloud() || MachineLocation("studio").IsCloud() || l.String() != "codex-cloud" {
		t.Fatalf("%+v", l)
	}
	m, title, ok := StripLegacyLabel("↪ continued in Codex on " + l.Name + " · Fix it")
	if !ok || m.Location != "codex-cloud" || m.AgentName != "Codex" || title != "Fix it" {
		t.Fatalf("%+v %q", m, title)
	}
	if !(Cloud{Tested: []string{"2.1"}}).TestedWith("2.1.289") || (Cloud{Tested: []string{"2.1"}}).TestedWith("2.2.0") {
		t.Fatal("cloud tested versions are prefixes")
	}
}
