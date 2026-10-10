package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestTerminalChildFixture(t *testing.T) {
	value := os.Getenv("HSMATRIX_TERMINAL_CHILD_TEST")
	if value == "" {
		return
	}
	if err := json.NewEncoder(os.Stdout).Encode(os.Args); err != nil {
		os.Exit(99)
	}
	fmt.Fprintln(os.Stderr, "fixture terminal diagnostic")
	code, err := strconv.Atoi(value)
	if err != nil {
		os.Exit(98)
	}
	os.Exit(code)
}

func TestTerminalChildPreservesArgumentsExitAndOutput(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{0, 7} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			t.Setenv("HSMATRIX_TERMINAL_CHILD_TEST", strconv.Itoa(code))
			output := filepath.Join(t.TempDir(), "result with spaces 日本語.json")
			args := []string{output, exe, "-test.run=^TestTerminalChildFixture$", "--", "space value", "日本語", "$(literal)", "a&b"}
			if got := terminalChild(args); got != code {
				t.Fatal("child exit status lost", got, code)
			}
			body, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			if err := json.Unmarshal(body, &got); err != nil || len(got) != len(args)-1 {
				t.Fatal("JSON output mixed with terminal diagnostics", err)
			}
			for i, arg := range got {
				if arg != args[i+1] {
					t.Fatal("argument changed", i, arg)
				}
			}
			if got := terminalChild(args); got == 0 {
				t.Fatal("existing output file was overwritten")
			}
			after, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(body, after) {
				t.Fatal("refused output replacement changed bytes", err)
			}
		})
	}
}
