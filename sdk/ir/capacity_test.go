package ir

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCapacityBoundary(t *testing.T) {
	c := Capacity{Window: 1000, Source: "test"}
	items := []Item{{Text: strings.Repeat("x", 268)}}
	if err := c.Check(items); err != nil {
		t.Fatal(err)
	}
	items[0].Text += "x"
	if c.Check(items) == nil {
		t.Fatal("one over boundary accepted")
	}
	// An existing session's usage includes its instructions and tools: adding to it
	// keeps 10% for the next response.
	c.Existing = 699
	if c.Allowance() != 201 {
		t.Fatalf("existing session allowance %d, want 201", c.Allowance())
	}
	c.Existing = 899
	if c.Allowance() != 1 {
		t.Fatal("existing context ignored")
	}
	c.Unknown = true
	if c.Check(nil) == nil {
		t.Fatal("unknown active state accepted")
	}
}
func TestBoundedReaderCancellationAndLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := BoundedReader{Context: ctx, Reader: strings.NewReader("x")}
	if _, err := io.ReadAll(&r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r = BoundedReader{Context: context.Background(), Reader: strings.NewReader("xx"), ReadBytes: MaxTranscriptBytes - 1}
	if _, err := io.ReadAll(&r); err == nil {
		t.Fatal("silent transcript truncation")
	}
	r = BoundedReader{Context: context.Background(), Reader: strings.NewReader("x"), ReadBytes: MaxTranscriptBytes - 1}
	if got, err := io.ReadAll(&r); err != nil || string(got) != "x" {
		t.Fatalf("exact limit must be accepted: %q, %v", got, err)
	}
}
