package hops

import (
	"testing"
	"time"
)

func TestLedger(t *testing.T) {
	d := t.TempDir()
	now := time.Now()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(Append(d, Hop{Time: now, SessionID: "s1", From: "laptop", To: "studio", Mark: MarkPending}))
	must(Append(d, Hop{Time: now, SessionID: "s2", From: "laptop", To: "studio", Mark: MarkDone}))
	must(Append(d, Hop{Time: now, SessionID: "s3", From: "mini", To: "studio", Mark: MarkPending}))
	if p := Pending(d, "laptop"); len(p) != 1 || p[0].SessionID != "s1" {
		t.Fatalf("pending: %+v", p)
	}
	must(Update(d, "s1", "laptop", func(h *Hop) { h.Mark = MarkDone }))
	if p := Pending(d, "laptop"); len(p) != 0 {
		t.Fatalf("after update: %+v", p)
	}
	// A newer hop of the same session supersedes an older pending one.
	must(Append(d, Hop{Time: now, SessionID: "s3", From: "mini", To: "studio", Mark: MarkOff}))
	if p := Pending(d, "mini"); len(p) != 0 {
		t.Fatalf("superseded: %+v", p)
	}
	if h, ok := Last(d, "s3"); !ok || h.Mark != MarkOff {
		t.Fatalf("last: %+v", h)
	}
	if len(Load(d)) != 4 {
		t.Fatal("all hops kept")
	}
}
