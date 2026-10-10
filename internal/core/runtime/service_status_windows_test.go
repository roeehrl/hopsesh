package runtime

import (
	"crypto/rand"
	"path/filepath"
	"testing"
)

// Exercise the real COM missing-task result through Windows PowerShell. Mocked
// JSON alone cannot catch a changed CIM error ID or a wrapped COM exception.
// This read-only check neither registers a task nor needs administrator access.
func TestWindowsSchedulerMissingTaskIsKnown(t *testing.T) {
	p := ServicePlan{Platform: "windows", Path: filepath.Join(t.TempDir(), "absent"), Name: "hopsesh-missing-" + rand.Text()}
	s := p.Status(t.Context())
	if !s.Known || s.Registered || s.Enabled || s.Running || s.Error != "" {
		t.Fatalf("missing native task was not identified: %+v", s)
	}
}
