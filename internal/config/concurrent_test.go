package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentWritersCannotLoseUpdates(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	a, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	b := a
	a.Appearance = "dark"
	b.Terminal.FontSize = 18
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, c := range []*Config{&a, &b} {
		wg.Go(func() { <-start; errs <- Save(c) })
	}
	close(start)
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflict)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	c.Terminal.FontSize = 20
	if err = Save(&c); err != nil {
		t.Fatal(err)
	}
	c.Terminal.FontSize = 21
	if err = Save(&c); err != nil {
		t.Fatal(err)
	}
}
func TestInvalidAndExternallyEditedSettingsArePreserved(t *testing.T) {
	t.Setenv("HOPSESH_CONFIG_DIR", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err = Save(&c); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(Path())
	c.Appearance = "invalid"
	if err = Save(&c); err == nil {
		t.Fatal("invalid value saved")
	}
	after, _ := os.ReadFile(Path())
	if string(before) != string(after) {
		t.Fatal("invalid settings changed file")
	}
	c.Appearance = "light"
	external := append(before, []byte("\n# external edit\n")...)
	if err = os.WriteFile(Path(), external, 0600); err != nil {
		t.Fatal(err)
	}
	if err = Save(&c); !errors.Is(err, ErrConflict) {
		t.Fatalf("external edit: %v", err)
	}
	after, _ = os.ReadFile(Path())
	if string(after) != string(external) {
		t.Fatal("external edit lost")
	}
	leftovers, _ := filepath.Glob(filepath.Join(Dir(), ".config-*"))
	if len(leftovers) != 0 {
		t.Fatal(leftovers)
	}
}
