package peer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func pair(t *testing.T, h Handler) *Client {
	t.Helper()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	go func() { _ = Serve(context.Background(), sr, sw, h); sw.Close() }()
	t.Cleanup(func() { cw.Close() })
	return NewClient(cr, cw)
}

func TestHelloFirstAndSameProtocol(t *testing.T) {
	h := func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		return map[string]string{"method": method}, nil
	}
	c := pair(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, MethodPlan, nil, nil); !errors.Is(err, ErrProtocol) {
		t.Fatalf("a request before hello: %v", err)
	}
	if err := c.Call(ctx, MethodHello, Hello{Protocol: Protocol + 1}, nil); !errors.Is(err, ErrProtocol) {
		t.Fatalf("another protocol is refused, never bridged: %v", err)
	}
	var got map[string]string
	if err := c.Call(ctx, MethodHello, Hello{Protocol: Protocol}, &got); err != nil || got["method"] != MethodHello {
		t.Fatalf("hello: %v %v", got, err)
	}
	if err := c.Call(ctx, MethodApply, struct{}{}, &got); err != nil || got["method"] != MethodApply {
		t.Fatalf("after hello: %v %v", got, err)
	}
}

func TestRefusedIsClassified(t *testing.T) {
	c := pair(t, func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		if method == MethodPlan {
			return nil, Refused("box")
		}
		return struct{}{}, nil
	})
	ctx := context.Background()
	_ = c.Call(ctx, MethodHello, Hello{Protocol: Protocol}, nil)
	if err := c.Call(ctx, MethodPlan, nil, nil); !errors.Is(err, ErrRefused) {
		t.Fatalf("refused: %v", err)
	}
}

// The wire is ASCII only, and decodes back to the same text.
func TestASCIIOnTheWire(t *testing.T) {
	var buf strings.Builder
	enc := newEncoder(&buf)
	in := map[string]string{"path": `C:\Users\Ünïcødé\プロジェクト`, "emoji": "done ✓ 🎉"}
	if err := enc.Encode(in); err != nil {
		t.Fatal(err)
	}
	for _, c := range []byte(buf.String()) {
		if c >= 0x80 {
			t.Fatalf("non-ASCII byte in %q", buf.String())
		}
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(buf.String()), &out); err != nil || out["path"] != in["path"] || out["emoji"] != in["emoji"] {
		t.Fatalf("round trip: %v %v", out, err)
	}
}
