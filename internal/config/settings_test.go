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

func TestNestedSettingsMapsPreserveSiblingsAndValidateTypes(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	for _, change := range []struct{ key, value string }{{"agents.claude.disabled", "true"}, {"agents.claude.place", `"app"`}, {"agents.codex.disabled", "false"}, {"clouds.codex-cloud.environments.demo", `"default"`}} {
		if _, err := SetSetting(change.key, json.RawMessage(change.value), nil); err != nil {
			t.Fatal(change, err)
		}
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.Agents["claude"].Disabled || c.Agents["claude"].Place != "app" || c.Clouds["codex-cloud"].Environments["demo"] != "default" {
		t.Fatal("nested update lost a sibling", c)
	}
	for _, change := range []struct{ key, value string }{{"agents.claude.missing", "true"}, {"agents.claude.disabled", `"true"`}, {"agents..disabled", "true"}, {"agents.claude.place", `"unknown"`}} {
		if _, err := SetSetting(change.key, json.RawMessage(change.value), nil); err == nil {
			t.Fatal("invalid nested setting accepted", change)
		}
	}
}
