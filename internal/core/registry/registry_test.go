package registry

import (
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A module's mark is shown as an image: it must be plain SVG with nothing that runs or
// reaches outside it.
func TestCheckSVG(t *testing.T) {
	ok := []string{
		"",
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8"><defs><linearGradient id="g"/></defs><rect fill="url(#g)" width="8" height="8"/><use href="#g"/></svg>`,
	}
	bad := []string{
		`<div/>`,
		`<svg><script>alert(1)</script></svg>`,
		`<svg onload="alert(1)"/>`,
		`<svg><image href="https://example.com/x.png"/></svg>`,
		`<svg><foreignObject/></svg>`,
		`<svg><rect`,
	}
	for _, s := range ok {
		if err := checkSVG(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range bad {
		if checkSVG(s) == nil {
			t.Errorf("accepted %s", s)
		}
	}
}

// A module may have no data folders when it declares a cloud; every cloud is checked, and
// two modules cannot claim one cloud.
func TestCloudModules(t *testing.T) {
	r, err := New(fakecloud.Stub())
	if err != nil {
		t.Fatalf("a cloud-only module must register: %v", err)
	}
	if m, c, ok := r.Cloud(fakecloud.FakeCloud); !ok || m.Spec().ID != "fakecloud" || c.Driver != "fakecloud" {
		t.Fatalf("Cloud(%s): %v %+v", fakecloud.FakeCloud, ok, c)
	}
	if _, err := New(fakecloud.Stub(), renamed{fakecloud.Stub()}); err == nil || !strings.Contains(err.Error(), "both declare the cloud") {
		t.Fatalf("two modules on one cloud: %v", err)
	}
	if _, err := New(noClouds{fakecloud.Stub()}); err == nil || !strings.Contains(err.Error(), "roots or clouds") {
		t.Fatalf("a module with neither roots nor clouds: %v", err)
	}
	if _, err := New(claude.New(), codex.New(), fakecloud.Stub()); err != nil {
		t.Fatal(err)
	}
}

// renamed is the stub under another id.
type renamed struct{ agent.Module }

func (r renamed) Spec() agent.Spec {
	s := r.Module.Spec()
	s.ID, s.Name = "fakecloud2", "Fake 2"
	return s
}

// noClouds is the stub without its cloud.
type noClouds struct{ agent.Module }

func (r noClouds) Spec() agent.Spec { s := r.Module.Spec(); s.Clouds = nil; return s }
