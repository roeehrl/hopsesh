package runtime

import (
	"context"
	"crypto/rand"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Diagnostic branch only: observe the unchanged production query under the
// same deadline. Never register tasks, retry a failed query, or accept unknown.
func TestSchedulerPhaseDiagnostic(t *testing.T) {
	p := ServicePlan{Platform: "windows", Path: filepath.Join(t.TempDir(), "absent"), Name: "hopsesh-missing-" + rand.Text()}
	phase := regexp.MustCompile(`\[Console\]::Error.WriteLine\('hopsesh-service-phase=([a-z]+)'\)`)
	safe := regexp.MustCompile(`^hopsesh-diagnostic-phase=[a-z-]+ elapsed-ms=[0-9]+$`)
	mark := func(name string) string {
		return "[Console]::Error.WriteLine(('hopsesh-diagnostic-phase=" + name + " elapsed-ms={0}' -f $hopseshClock.ElapsedMilliseconds))"
	}
	started := time.Now()
	s := p.status(t.Context(), func(ctx context.Context, args []string) ([]byte, error) {
		args = append([]string(nil), args...)
		script := args[len(args)-1]
		script = phase.ReplaceAllStringFunc(script, func(s string) string { return mark(phase.FindStringSubmatch(s)[1]) })
		script = strings.Replace(script, "catch {", "catch {\n"+mark("task-error"), 1)
		script = strings.Replace(script, "@{registered=$false", mark("missing-json")+"\n  @{registered=$false", 1)
		script = strings.ReplaceAll(script, "| ConvertTo-Json -Compress", "| ConvertTo-Json -Compress\n"+mark("json-done"))
		args[len(args)-1] = "$hopseshClock=[Diagnostics.Stopwatch]::StartNew();\n" + script
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.WaitDelay = time.Second
		var out, stderr boundedServiceOutput
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		for _, line := range strings.Split(stderr.text.String(), "\n") {
			line = strings.TrimSpace(line)
			if safe.MatchString(line) {
				t.Log(line)
			}
		}
		if out.overflow || stderr.overflow {
			return nil, fmt.Errorf("diagnostic output exceeded bound")
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return []byte(out.text.String()), err
	})
	t.Logf("query elapsed=%s known=%t", time.Since(started), s.Known)
	if !s.Known || s.Registered || s.Enabled || s.Running || s.Error != "" {
		t.Fatalf("missing native task was not identified: %+v", s)
	}
}
