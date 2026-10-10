package journal

import "testing"

func TestRemoteAcknowledgmentSurvivesRestartAndCompletesOnce(t *testing.T) {
	state := t.TempDir()
	j, err := New(state, KindMove, "remote ack")
	if err != nil {
		t.Fatal(err)
	}
	a := Acknowledgment{Machine: "source", Transport: "relay-pull", Peer: "approved-peer", Operation: "original-operation"}
	for range 2 {
		if err = j.QueueAcknowledgment(a); err != nil {
			t.Fatal(err)
		}
	}
	j, err = Load(state, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !j.PendingReceipts() || len(j.Acknowledgments) != 1 {
		t.Fatal("lost pending remote acknowledgment")
	}
	if err = j.Undo(t.Context(), Reach{}, true); err == nil {
		t.Fatal("force undo lost unconfirmed source component")
	}
	for range 2 {
		if err = j.CompleteAcknowledgment(a.Peer, a.Operation, a.Machine, "source-journal"); err != nil {
			t.Fatal(err)
		}
	}
	if j.PendingReceipts() || len(j.Remote) != 1 {
		t.Fatal("acknowledgment duplicated remote undo or remained pending")
	}
	a.Machine = "different-machine"
	if err = j.QueueAcknowledgment(a); err == nil {
		t.Fatal("ack rebound to another machine")
	}
}
