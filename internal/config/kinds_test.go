package config

import "testing"

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
	if (&Agent{}).SetShows("other", true) {
		t.Fatal("unknown kind accepted")
	}
}
