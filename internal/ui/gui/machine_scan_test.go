package gui

import (
	"testing"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
)

func TestScanMachinePreservesOtherResultsAndSnapshots(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	// Invalid destination fails before any network connection.
	a.core.Cfg.Hosts = []config.Host{{Name: "target", Destination: "", Allowed: true}}
	old := &app.Inventory{
		Machines: []*app.Machine{{Name: "here", Local: true}, {Name: "target", Status: "ok"}, {Name: "other", Status: "ok"}},
		Entries:  []app.Entry{{Machine: "here"}, {Machine: "target"}, {Machine: "other"}, {Machine: "cloud"}},
	}
	a.inv = old
	for range 2 {
		if err := a.ScanMachine("target"); err != nil {
			t.Fatal(err)
		}
		if a.inv == old || old.Machines[1].Status != "ok" || len(old.Entries) != 4 || old.Entries[1].Machine != "target" {
			t.Fatal("modified an existing inventory snapshot")
		}
		if len(a.inv.Machines) != 3 || len(a.inv.Entries) != 3 || a.inv.Machine("other") != old.Machine("other") {
			t.Fatal("lost or duplicated another machine's results")
		}
		s := a.MachineScans()["target"]
		if s.Phase != "done" || s.Error == "" || s.Started == "" || s.Finished == "" {
			t.Fatalf("missing failure status: %+v", s)
		}
		copy := a.MachineScans()
		delete(copy, "target")
		if a.MachineScans()["target"].Phase != "done" {
			t.Fatal("poll result aliases backend state")
		}
	}
}

func TestScanMachineQueuedRequestsCoalesceAndRemovalWins(t *testing.T) {
	home(t)
	a := NewApp(all.Registry())
	a.core.Cfg.Hosts = []config.Host{{Name: "target", Destination: "", Allowed: true}}
	a.scanMu.Lock()
	done := make(chan error, 1)
	go func() { done <- a.ScanMachine("target") }()
	deadline := time.Now().Add(time.Second)
	for a.MachineScans()["target"].Phase != "queued" {
		if time.Now().After(deadline) {
			a.scanMu.Unlock()
			t.Fatal("scan did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	if err := a.ScanMachine("target"); err != nil {
		a.scanMu.Unlock()
		t.Fatal(err)
	}
	a.mu.Lock()
	a.core.Cfg.Hosts = nil
	a.mu.Unlock()
	a.scanMu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if a.inv != nil {
		t.Fatal("a removed machine was scanned")
	}
	if s := a.MachineScans()["target"]; s.Phase != "done" || s.Error == "" {
		t.Fatalf("missing cancellation reason: %+v", s)
	}
	if err := a.ScanMachine("target"); err == nil {
		t.Fatal("unknown machine accepted")
	}
	a.core.Cfg.Hosts = []config.Host{{Name: "target", Allowed: false}}
	if err := a.ScanMachine("target"); err == nil {
		t.Fatal("disabled machine accepted")
	}
}
