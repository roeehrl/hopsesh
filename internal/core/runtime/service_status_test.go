package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceStatusUsesSupervisorAndNeverClaimsFileIsEnabled(t *testing.T) {
	root := t.TempDir()
	n := Namespace{ID: "abc123", Config: filepath.Join(root, "config"), State: filepath.Join(root, "state"), Directory: filepath.Join(root, "runtime")}
	for _, tc := range []struct {
		os, response                 string
		registered, enabled, running bool
	}{
		{"linux", "LoadState=loaded\nActiveState=inactive\nUnitFileState=disabled\n", true, false, false},
		{"linux", "LoadState=not-found\nActiveState=inactive\nUnitFileState=\n", false, false, false},
		{"linux", "LoadState=loaded\nActiveState=active\nUnitFileState=enabled\n", true, true, true},
		{"darwin", "state = running\n", true, true, true},
		{"darwin", "state = waiting\n", true, true, false},
		{"windows", `{"registered":true,"enabled":true,"running":false}`, true, true, false},
		{"windows", `{"registered":false,"enabled":false,"running":false}`, false, false, false},
	} {
		p, err := PlanService(n, filepath.Join(root, "hopsesh"), tc.os, root, "1000")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Dir(p.Path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(p.Path, []byte(p.Definition), 0600); err != nil {
			t.Fatal(err)
		}
		s := p.status(context.Background(), func(context.Context, []string) ([]byte, error) { return []byte(tc.response), nil })
		if !s.Known || !s.Definition || s.Registered != tc.registered || s.Enabled != tc.enabled || s.Running != tc.running {
			t.Fatalf("%s: %+v", tc.os, s)
		}
		s = p.status(context.Background(), func(context.Context, []string) ([]byte, error) { return nil, errors.New("no supervisor") })
		if s.Known || s.Enabled || s.Running || s.Error == "" {
			t.Fatalf("failed query claimed service: %+v", s)
		}
	}
}

func TestWindowsServiceStatusRejectsIncompleteRepliesAndBoundsQueries(t *testing.T) {
	p := ServicePlan{Platform: "windows", Path: filepath.Join(t.TempDir(), "absent"), Name: "test"}
	for _, reply := range []string{`{}`, `null`, `{"registered":false}`, `{"registered":true,"enabled":null,"running":true}`, `garbage`} {
		s := p.status(t.Context(), func(ctx context.Context, _ []string) ([]byte, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 15*time.Second {
				t.Fatal("OS service query has no bounded deadline")
			}
			return []byte(reply), nil
		})
		if s.Known || !strings.Contains(s.Error, "invalid supervisor response") {
			t.Fatalf("incomplete status accepted: %q: %+v", reply, s)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := p.status(ctx, func(ctx context.Context, _ []string) ([]byte, error) { return nil, ctx.Err() })
	if s.Known || !strings.Contains(s.Error, "context canceled") {
		t.Fatalf("canceled query lost its reason: %+v", s)
	}
}

func TestSchedulerPhaseNeverIncludesUntrustedOutput(t *testing.T) {
	for _, tc := range []struct{ stderr, want string }{
		{"private path and secret", "PowerShell startup"},
		{"hopsesh-service-phase=create\r\nhopsesh-service-phase=connect\r\nraw secret", "Task Scheduler connect"},
		{"hopsesh-service-phase=task\nhopsesh-service-phase=private-secret", "Task Scheduler task"},
	} {
		if got := schedulerPhase(tc.stderr); got != tc.want {
			t.Fatalf("phase %q; want %q", got, tc.want)
		}
	}
}

func TestServiceDefinitionRejectsLinksAndOtherDefinitions(t *testing.T) {
	root := t.TempDir()
	p := ServicePlan{Path: filepath.Join(root, "service"), Definition: "expected"}
	if err := os.WriteFile(p.Path, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.definitionPresent(); err == nil {
		t.Fatal("unrelated definition accepted")
	}
	if err := os.Remove(p.Path); err != nil {
		t.Fatal(err)
	}
	if present, err := p.definitionPresent(); err != nil || present {
		t.Fatal(present, err)
	}
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte(p.Definition), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p.Path); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	if _, err := p.definitionPresent(); err == nil {
		t.Fatal("linked definition accepted")
	}
}
