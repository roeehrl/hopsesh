package agent

import (
	"reflect"
	"testing"
)

func TestAddOwnerMergesConsecutiveRunsOfOneAccount(t *testing.T) {
	var runs []OwnerRun
	for _, r := range []OwnerRun{{Key: "a"}, {Key: "a", Person: "u1"}, {Key: "a"}, {Key: "b"}, {}, {Key: "a", Person: "u2"}} {
		runs = AddOwner(runs, r)
	}
	want := []OwnerRun{{Key: "a", Person: "u1"}, {Key: "b"}, {Key: "a", Person: "u2"}}
	if !reflect.DeepEqual(runs, want) {
		t.Fatalf("%+v", runs)
	}
}
