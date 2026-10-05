package registry

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/agents/claude"
	"github.com/roeehrl/hopsesh/agents/codex"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Every field a module declares reaches the data, so a field added to the SDK is added
// here too (or named below as left out).
func TestSpecDataCoversSpec(t *testing.T) {
	for _, c := range []struct {
		sdk, data any
		renamed   map[string]string
	}{
		{agent.Spec{}, SpecData{}, map[string]string{"Icon": "DesktopApps"}},
		{agent.Binary{}, BinaryData{}, nil},
		{agent.Root{}, RootData{}, nil},
		{agent.Cloud{}, CloudData{}, nil},
		{agent.Watch{}, WatchData{}, nil},
		{agent.Feed{}, FeedData{}, nil},
		{agent.WatchCode{}, CodeData{}, nil},
	} {
		st, dt := reflect.TypeOf(c.sdk), reflect.TypeOf(c.data)
		for i := range st.NumField() {
			name := st.Field(i).Name
			if r, ok := c.renamed[name]; ok {
				name = r
			}
			if _, ok := dt.FieldByName(name); !ok {
				t.Errorf("%s.%s has no field in %s", st.Name(), st.Field(i).Name, dt.Name())
			}
		}
	}
}

func TestSpecDataJSON(t *testing.T) {
	for _, m := range []agent.Module{claude.New(), codex.New()} {
		b, err := json.Marshal(Data(m.Spec()))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if strings.Contains(s, "null") || strings.Contains(s, "<svg") {
			t.Errorf("%s: a null list or the mark: %s", m.Spec().ID, s)
		}
		var back SpecData
		if err := json.Unmarshal(b, &back); err != nil || back.ID != m.Spec().ID || len(back.Clouds) != len(m.Spec().Clouds) {
			t.Errorf("%s: round trip %v", m.Spec().ID, err)
		}
		for _, c := range back.Clouds {
			if c.Watch.Surface == "" || len(c.Watch.Docs) == 0 {
				t.Errorf("%s: cloud %s lost its watch list", m.Spec().ID, c.Name)
			}
		}
	}
}
