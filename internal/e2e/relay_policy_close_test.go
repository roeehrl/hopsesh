package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestRelayPolicyCloseRequiresActualAuthorizationTermination(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		hints                     int
		code                      websocket.StatusCode
		invalid, stayOpen, wantOK bool
	}{
		{name: "queued hints then policy close", hints: 4, code: websocket.StatusPolicyViolation, wantOK: true},
		{name: "normal close is not revocation", hints: 2, code: websocket.StatusNormalClosure},
		{name: "unexpected payload refused", invalid: true, code: websocket.StatusPolicyViolation},
		{name: "unbounded hints refused", hints: 40, code: websocket.StatusPolicyViolation},
		{name: "connected stream is not revocation", hints: 1, stayOpen: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				socket, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer socket.CloseNow()
				for range tc.hints {
					if err = socket.Write(ctx, websocket.MessageText, []byte(`{"type":"mailbox-changed"}`)); err != nil {
						return
					}
				}
				if tc.invalid {
					_ = socket.Write(ctx, websocket.MessageText, []byte(`{"type":"private-payload"}`))
				}
				if tc.stayOpen {
					<-ctx.Done()
					return
				}
				_ = socket.Close(tc.code, "fixture")
			}))
			defer server.Close()
			socket, _, err := websocket.Dial(ctx, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer socket.CloseNow()
			socket.SetReadLimit(256)
			err = readRelayPolicyClose(ctx, socket)
			if (err == nil) != tc.wantOK {
				t.Fatalf("policy termination: error=%v wantOK=%t", err, tc.wantOK)
			}
		})
	}
}
