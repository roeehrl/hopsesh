package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	in := map[string]string{"path": `C:\Users\Ünïcødé\プロジェクト`, "emoji": "done ✓ 🎉",
		"japanese": "メインファイルのバグを直して", "chinese": "修复主文件中的错误", "arabic": "أصلح الخطأ"}
	if err := enc.Encode(in); err != nil {
		t.Fatal(err)
	}
	for _, c := range []byte(buf.String()) {
		if c >= 0x80 {
			t.Fatalf("non-ASCII byte in %q", buf.String())
		}
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(buf.String()), &out); err != nil {
		t.Fatal(err)
	}
	for k, v := range in {
		if out[k] != v {
			t.Fatalf("%s: %q came back as %q", k, v, out[k])
		}
	}
}

// hopsesh 0.3 (protocol 1) says hello with these bytes, and refuses another protocol
// with this message (and nothing else).
const (
	helloV03  = `{"id":1,"method":"hello","params":{"protocol":1,"version":"0.3.1","from":"old-box"}}` + "\n"
	refuseV03 = `{"id":1,"error":{"code":"protocol","message":"hopsesh here speaks protocol 1, the other machine 2: update hopsesh so both are the same version"}}` + "\n"
)

// fakeV03 answers like hopsesh 0.3 on another machine: it refuses any hello of another
// protocol.
func fakeV03(t *testing.T) *Client {
	t.Helper()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	go func() {
		defer sw.Close()
		var m message
		if err := json.NewDecoder(sr).Decode(&m); err != nil || m.Method != MethodHello {
			return
		}
		_, _ = io.WriteString(sw, refuseV03)
	}()
	t.Cleanup(func() { cw.Close() })
	return NewClient(cr, cw)
}

// A push from this hopsesh to a 0.3 machine fails at hello, and says which one to update.
func TestOlderPeerIsNamed(t *testing.T) {
	if Protocol < 2 || FirstVersion == "" {
		t.Fatalf("hopsesh 0.4 changed the lineage and cloud messages: protocol %d, first version %q", Protocol, FirstVersion)
	}
	c := fakeV03(t)
	err := c.Call(context.Background(), MethodHello, Hello{Protocol: Protocol, Version: "0.4.0", From: "here"}, nil)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("hello to a 0.3 machine: %v", err)
	}
	err = Mismatch(err, "box")
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("still a protocol error: %v", err)
	}
	for _, want := range []string{"box runs an older hopsesh (peer protocol 1)", "update hopsesh on box to " + FirstVersion + " or later"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
}

// A 0.3 machine pushing here is refused with a message it prints as it is (followed by
// " (on <machine>)"), which tells its user to update hopsesh there.
func TestOlderSenderIsTold(t *testing.T) {
	sr, cw := io.Pipe()
	cr, sw := io.Pipe()
	go func() {
		_ = Serve(context.Background(), sr, sw, func(context.Context, string, json.RawMessage) (any, error) { return struct{}{}, nil })
		sw.Close()
	}()
	defer cw.Close()
	go func() { _, _ = io.WriteString(cw, helloV03) }()
	// What hopsesh 0.3 decodes the reply into.
	var reply struct {
		ID    int `json:"id"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(cr).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error == nil || reply.Error.Code != CodeProtocol {
		t.Fatalf("a protocol 1 hello must be refused: %+v", reply)
	}
	shown := reply.Error.Message + " (on studio)"
	for _, want := range []string{"this machine runs an older hopsesh (0.3.1, peer protocol 1)", "update it to " + FirstVersion + " or later", "on the other machine (on studio)"} {
		if !strings.Contains(shown, want) {
			t.Errorf("%q lacks %q", shown, want)
		}
	}
}

// Between this hopsesh and a newer one, each side says the older one is this one.
func TestNewerPeer(t *testing.T) {
	c := pair(t, func(context.Context, string, json.RawMessage) (any, error) { return struct{}{}, nil })
	err := c.Call(context.Background(), MethodHello, Hello{Protocol: Protocol + 1, Version: "9.9.9"}, nil)
	var pe *Error
	if !errors.As(err, &pe) || pe.Protocol != Protocol || !strings.Contains(pe.Message, "update hopsesh to 9.9.9 or later on the other machine") {
		t.Fatalf("a newer sender is told to update the other machine: %#v", err)
	}
	newer := &Error{Code: CodeProtocol, Message: "refused", Protocol: Protocol + 1, Version: "9.9.9"}
	err = Mismatch(newer, "box")
	if !errors.Is(err, ErrProtocol) || !strings.Contains(err.Error(), fmt.Sprintf("box runs a newer hopsesh (9.9.9, peer protocol %d)", Protocol+1)) ||
		!strings.Contains(err.Error(), "update hopsesh on this machine to 9.9.9 or later") {
		t.Fatalf("a newer peer: %v", err)
	}
	other := errors.New("connection reset")
	if Mismatch(other, "box") != other {
		t.Fatal("other errors pass through")
	}
}
