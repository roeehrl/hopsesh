package iterm2api

import (
	"context"
	"os"
	"testing"
)

// TestMain makes sure no test can run osascript (and so send an Apple Event or show a
// consent prompt): the hook that would run it panics instead.
func TestMain(m *testing.M) {
	osascript = func(context.Context, string) ([]byte, []byte, error) {
		panic("a test tried to run osascript")
	}
	os.Exit(m.Run())
}
