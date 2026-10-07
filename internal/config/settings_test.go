package config

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSettingsRevisionTypesAndProtectedKeys(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	s, err := ReadSettings()
	if err != nil {
		t.Fatal(err)
	}
	empty := s.Revision
	s, err = SetSetting("peer.receive", json.RawMessage(`true`), &empty)
	if err != nil {
		t.Fatal(err)
	}
	v, err := SettingValue(s, "peer.receive")
	if err != nil || v != true {
		t.Fatalf("get: %v %v", v, err)
	}
	if _, err = SetSetting("appearance", json.RawMessage(`"dark"`), &empty); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	for _, x := range []struct{ k, v string }{{"schema", "5"}, {"peer.receive", `"true"`}, {"runtime.reconcile_seconds", "2"}, {"runtime.mode", `"magic"`}, {"layout", `"magic"`}, {"missing", "true"}} {
		if _, err = SetSetting(x.k, json.RawMessage(x.v), nil); err == nil {
			t.Fatalf("invalid setting accepted: %+v", x)
		}
	}
	s, err = SetSetting("runtime.reconcile_seconds", json.RawMessage("30"), nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err = SettingValue(s, "runtime.reconcile_seconds")
	if err != nil || v != int64(30) {
		t.Fatalf("get: %T %v %v", v, v, err)
	}
}
