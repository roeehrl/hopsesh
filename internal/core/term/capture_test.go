package term_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/term"
)

// What a program prints becomes plain text: escape sequences out, moves to other lines as
// line breaks, carriage returns as line breaks, OSC 8 targets kept.
func TestPlain(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b[1mbold\x1b[22m and \x1b[38;2;1;2;3mcolour\x1b[0m": "bold and colour",
		"one\r\ntwo\rthree\n":                "one\ntwo\nthree\n",
		"a\x1b[2;1Hb\x1b[1Ac\x1b[5Cd\x1b[Ge": "a\nb\nc d e",
		"\x1b]0;title\x07x\x1b]8;;https://example.com/a\x1b\\link\x1b]8;;\x1b\\": "x https://example.com/a link",
		"\x1bP+q544e\x1b\\y\x1b(Bz\x1b7\x1b8":                                    "yz",
		"tab\there\x00\x07\x08":                                                  "tab\there",
		"cut \x1b[":                                                              "cut ",
		"ünïcödé ❯ \xff ok":                                                      "ünïcödé ❯  ok",
	} {
		if got := term.Plain([]byte(in)); got != want {
			t.Errorf("Plain(%q) = %q, want %q", in, got, want)
		}
	}
}

// A capture keeps only the last MaxCapture bytes, and forgets them on Reset.
func TestCaptureIsBounded(t *testing.T) {
	var c term.Capture
	_, _ = c.Write(bytes.Repeat([]byte("a"), term.MaxCapture))
	_, _ = c.Write([]byte("tail"))
	text := c.Text()
	if len(text) != term.MaxCapture || !strings.HasSuffix(text, "tail") {
		t.Fatalf("kept %d bytes", len(text))
	}
	c.Reset()
	if c.Text() != "" {
		t.Error("Reset keeps something")
	}
}
