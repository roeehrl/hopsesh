package clientapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// talk runs Serve over pipes and returns a send function and the decoded output lines.
func talk(t *testing.T, h Handler) (send func(string), next func() map[string]any, done func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, inR, outW, h); _ = outW.Close() }()
	sc := bufio.NewScanner(outR)
	lines := make(chan map[string]any, 16)
	go func() {
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("not JSON: %s", sc.Text())
			}
			lines <- m
		}
		close(lines)
	}()
	send = func(s string) {
		if _, err := io.WriteString(inW, s+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	next = func() map[string]any {
		select {
		case m := <-lines:
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("no answer")
		}
		return nil
	}
	done = func() {
		_ = inW.Close()
		cancel()
		if err := <-served; err != nil {
			t.Fatal(err)
		}
	}
	return send, next, done
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func echo(_ context.Context, c *Conn, id json.RawMessage, method string, params json.RawMessage) (any, error) {
	switch method {
	case MethodHello:
		return HelloResult{API: API, MinAPI: MinAPI, Version: "test"}, nil
	case "slow":
		time.Sleep(200 * time.Millisecond)
		return "slow", nil
	case "fast":
		return "fast", nil
	case "events":
		_ = c.Event(id, "tick", 1)
		_ = c.Event(id, "tick", 2)
		return "ok", nil
	case "typed":
		return nil, &Error{Code: CodeOwned, Message: "someone runs it", Fix: "stop it"}
	case "plain":
		return nil, errors.New("boom")
	}
	return nil, Errorf(CodeUnknown, "unknown %s", method)
}

func TestHelloFirst(t *testing.T) {
	send, next, done := talk(t, echo)
	defer done()
	send(`{"id":1,"method":"fast"}`)
	if got := errCode(next()); got != CodeHello {
		t.Fatalf("before hello: %q, want %q", got, CodeHello)
	}
	send(`{"id":2,"method":"hello","params":{"api":1,"client":{"name":"t"}}}`)
	if m := next(); m["error"] != nil {
		t.Fatalf("hello: %v", m)
	}
	send(`{"id":3,"method":"fast"}`)
	if m := next(); m["result"] != "fast" {
		t.Fatalf("after hello: %v", m)
	}
}

func TestHelloAPIMismatch(t *testing.T) {
	send, next, done := talk(t, echo)
	defer done()
	send(`{"id":1,"method":"hello","params":{"api":99,"minApi":99,"client":{"name":"t"}}}`)
	if got := errCode(next()); got != CodeAPI {
		t.Fatalf("future API: %q, want %q", got, CodeAPI)
	}
	send(`{"id":2,"method":"fast"}`)
	if got := errCode(next()); got != CodeHello {
		t.Fatalf("after a refused hello: %q, want %q", got, CodeHello)
	}
}

func TestAgree(t *testing.T) {
	for _, c := range []struct {
		p    HelloParams
		want int
	}{
		{HelloParams{API: 1}, 1},
		{HelloParams{API: 5, MinAPI: 1}, 1},
		{HelloParams{API: 5, MinAPI: 2}, 0},
		{HelloParams{API: 0}, 0},
	} {
		if got := Agree(c.p); got != c.want {
			t.Errorf("Agree(%+v) = %d, want %d", c.p, got, c.want)
		}
	}
}

func TestConcurrentAndEvents(t *testing.T) {
	send, next, done := talk(t, echo)
	defer done()
	send(`{"id":1,"method":"hello","params":{"api":1,"client":{"name":"t"}}}`)
	next()
	send(`{"id":"a","method":"slow"}`)
	send(`{"id":"b","method":"fast"}`)
	if m := next(); m["id"] != "b" {
		t.Fatalf("the fast request should answer first: %v", m)
	}
	if m := next(); m["id"] != "a" {
		t.Fatalf("then the slow one: %v", m)
	}
	send(`{"id":7,"method":"events"}`)
	var ticks int
	for {
		m := next()
		if m["event"] == "tick" {
			if m["for"] != float64(7) {
				t.Fatalf("event for %v, want 7", m["for"])
			}
			ticks++
			continue
		}
		if m["result"] != "ok" || ticks != 2 {
			t.Fatalf("events then result: ticks %d, %v", ticks, m)
		}
		break
	}
}

func TestErrors(t *testing.T) {
	send, next, done := talk(t, echo)
	defer done()
	send(`{"id":1,"method":"hello","params":{"api":1,"client":{"name":"t"}}}`)
	next()
	send(`not json`)
	if got := errCode(next()); got != CodeBadRequest {
		t.Fatalf("bad line: %q", got)
	}
	send(`{"id":2,"method":"typed"}`)
	m := next()
	if errCode(m) != CodeOwned || !strings.Contains(m["error"].(map[string]any)["fix"].(string), "stop") {
		t.Fatalf("typed error: %v", m)
	}
	send(`{"id":3,"method":"plain"}`)
	if got := errCode(next()); got != CodeFailed {
		t.Fatalf("plain error: %q", got)
	}
}
