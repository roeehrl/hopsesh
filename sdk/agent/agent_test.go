package agent

import (
	"encoding/json"
	"testing"
)

func TestMainRoot(t *testing.T) {
	s := Spec{ID: "x", Roots: []Root{{Name: "data"}, {Name: "config"}}}
	if got := s.MainRoot().Name; got != "data" {
		t.Fatalf("MainRoot = %q, want data", got)
	}
	if got := (Spec{ID: "cloud"}).MainRoot().Name; got != "" {
		t.Fatalf("cloud-only MainRoot = %q, want none", got)
	}
	in := Install{Roots: map[string]string{"data": "/d", "config": "/c"}, MainRoot: "data"}
	if in.Main() != "/d" {
		t.Fatalf("Main = %q", in.Main())
	}
	pinned := s.PinMain(in, "/profile")
	if pinned.Main() != "/profile" || len(pinned.Roots) != 1 || pinned.MainRoot != "data" {
		t.Fatalf("PinMain = %+v", pinned)
	}
	if in.Main() != "/d" {
		t.Fatal("PinMain changed the original install")
	}
	// An install from an older peer does not name its main root; a single root is it.
	var old Install
	if err := json.Unmarshal([]byte(`{"agent":"claude","roots":{"home":"/h"},"present":true}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Main() != "/h" {
		t.Fatalf("old Main = %q", old.Main())
	}
	if (Install{Roots: map[string]string{"a": "/a", "b": "/b"}}).Main() != "" {
		t.Fatal("an unnamed main root among several must not be guessed")
	}
}
