package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// Voice conversations and Work threads stay hidden until the user shows that kind.
func TestAgentShowsKinds(t *testing.T) {
	var voiceOnly, all Agent
	voiceOnly.SetShows("voice", true)
	all.SetShows("voice", true)
	all.SetShows("work", true)
	for _, c := range []struct {
		cfg   Agent
		kinds []string
		want  bool
	}{{Agent{}, nil, true}, {Agent{}, []string{"voice"}, false}, {Agent{}, []string{"work"}, false}, {voiceOnly, []string{"voice"}, true}, {voiceOnly, []string{"voice", "work"}, false}, {all, []string{"voice", "work"}, true}} {
		if c.cfg.Shows(c.kinds) != c.want {
			t.Errorf("%+v shows %v != %v", c.cfg, c.kinds, c.want)
		}
	}
	if (&Agent{}).SetShows("", true) {
		t.Fatal("a kind with no name accepted")
	}
}

// A kind of a newer agent has no setting of its own: it goes in Kinds, and turning it off
// leaves the agent with default settings.
func TestAgentShowsOtherKinds(t *testing.T) {
	var a Agent
	if !a.SetShows("archived", true) || !a.Shows([]string{"archived"}) || a.Shows([]string{"archived", "voice"}) {
		t.Fatalf("%+v", a)
	}
	a.SetShows("archived", false)
	if !a.IsZero() || a.Shows([]string{"archived"}) {
		t.Fatalf("%+v", a)
	}
}

// voice and work are written where earlier versions read them.
func TestAgentKindsFileFormat(t *testing.T) {
	var a Agent
	a.SetShows("voice", true)
	a.SetShows("archived", true)
	var b strings.Builder
	if err := toml.NewEncoder(&b).Encode(map[string]Agent{"codex": a}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "voice_threads = true") || strings.Contains(b.String(), "voice =") {
		t.Fatalf("voice is not where older versions read it:\n%s", b.String())
	}
	var back map[string]Agent
	if _, err := toml.Decode("[codex]\nwork_threads = true\n[codex.kinds]\narchived = true\n", &back); err != nil {
		t.Fatal(err)
	}
	if !back["codex"].Shows([]string{"work", "archived"}) {
		t.Fatalf("%+v", back)
	}
}
