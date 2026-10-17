package app

import "testing"

func TestDrainRefusesInFlightAndAllNewActionsAfterAcceptance(t *testing.T) {
	a := &App{activity: &runtimeActivity{}}
	done, err := a.beginRuntimeAction()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.drainRuntime(); err == nil {
		t.Fatal("accepted stop during transfer")
	}
	copy := *a
	done2, err := copy.beginRuntimeAction()
	if err != nil {
		t.Fatal(err)
	}
	done2()
	done()
	if err = a.drainRuntime(); err != nil {
		t.Fatal(err)
	}
	if _, err = copy.beginRuntimeAction(); err == nil {
		t.Fatal("snapshot escaped drain")
	}
}
