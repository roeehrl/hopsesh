package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeCommandRejectsInvalidSelectionBeforeExecution(t *testing.T) {
	for _, args := range [][]string{{"-t", "0"}, {"-only", "99999"}, {"-shard", "999/999"}, {"-timeout", "0"}, {"-source", "does-not-exist"}, {"unexpected-positional-argument"}} {
		out := filepath.Join(t.TempDir(), "must-not-exist")
		flags := append([]string{"-out", out}, args...)
		if status := runtimeMain("runtime-run", flags); status != 2 {
			t.Fatal("invalid command did not fail before execution", args, status)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("invalid selection created execution files", err)
		}
	}
}
