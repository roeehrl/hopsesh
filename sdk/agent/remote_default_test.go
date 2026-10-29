package agent

import "testing"

func TestSettingFile(t *testing.T) {
	f := SettingFile{Key: "remoteControlAtStartup"}
	in := []byte(`{"model":"opus","hooks":{"SessionStart":[]}}`)
	on, err := f.Set(in, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.Get(on); !got {
		t.Fatalf("not on: %s", on)
	}
	if again, _ := f.Set(on, true); string(again) != string(on) {
		t.Fatalf("turning on twice rewrote it")
	}
	off, _ := f.Set(on, false)
	if got, _ := f.Get(off); got || string(off) != "{\n  \"hooks\": {\n    \"SessionStart\": []\n  },\n  \"model\": \"opus\"\n}\n" {
		t.Fatalf("off: %s", off)
	}
	if same, _ := f.Set(in, false); string(same) != string(in) {
		t.Fatalf("off when already off rewrote it: %s", same)
	}
	// An explicit false is off; turning off removes it (the agent's default again).
	explicit := []byte(`{"remoteControlAtStartup": false}`)
	if got, _ := f.Get(explicit); got {
		t.Fatal("false read as on")
	}
	if cleared, _ := f.Set(explicit, false); string(cleared) != "{}\n" {
		t.Fatalf("%s", cleared)
	}
	if _, err := f.Set([]byte(`[1]`), true); err == nil {
		t.Fatal("a settings file that isn't an object was changed")
	}
	if on, _ := f.Set(nil, true); string(on) != "{\n  \"remoteControlAtStartup\": true\n}\n" {
		t.Fatalf("new file: %s", on)
	}
}
