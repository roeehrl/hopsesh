package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchivePagination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "{\"kind\":\"message\",\"text\":\"message %d %s\"}\n", i, strings.Repeat("x", 5000))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{path, "--offset", "3", "--limit", "1"}, {path, "--search", "message 19"}, {path, "--limit", "50"}} {
		cmd := archiveCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if out.Len() > 8400 {
			t.Fatal("unbounded archive output")
		}
		if !strings.Contains(out.String(), "quoted data") {
			t.Fatal("archive lacked boundary")
		}
	}
}
