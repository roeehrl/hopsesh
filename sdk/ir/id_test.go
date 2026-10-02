package ir

import (
	"testing"
	"time"
)

func TestCanonical(t *testing.T) {
	got, err := Canonical(map[string]any{"b": 1, "a": "x\nyé", "\uE000": true, "\U0001F600": nil, "c": []any{2, "z"}})
	if err != nil {
		t.Fatal(err)
	}
	// RFC 8785 orders by UTF-16 code units: U+1F600 (surrogates D83D DE00) sorts before
	// U+E000, although its code point is larger.
	want := "{\"a\":\"x\\nyé\",\"b\":1,\"c\":[2,\"z\"],\"\U0001F600\":null,\"\uE000\":true}"
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if _, err := Canonical(1.5); err == nil {
		t.Fatal("a float must be refused")
	}
}

func TestNodeIDs(t *testing.T) {
	mk := func(ts time.Time) []Node {
		ns := []Node{
			{Kind: KindMessage, Actor: User, Text: "fix the build", Time: ts},
			{Kind: KindMessage, Actor: Agent, Text: "done", Time: ts, Native: &Native{Format: "x", Payload: []byte(`{"id":"1"}`)}},
		}
		Chain(ns, "")
		return ns
	}
	a, b := mk(time.Unix(1, 0)), mk(time.Unix(99, 0))
	if a[1].ID != b[1].ID || a[1].Parent != a[0].ID {
		t.Fatal("ids must ignore time and native data, and chain to the parent")
	}
	c := []Node{{Kind: KindMessage, Actor: User, Text: "fix the build"}, {Kind: KindMessage, Actor: Agent, Text: "Done"}}
	Chain(c, "")
	if c[0].ID != a[0].ID || c[1].ID == a[1].ID {
		t.Fatal("different content must change the id; the same prefix must not")
	}
}
