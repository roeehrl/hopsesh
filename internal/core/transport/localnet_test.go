package transport

import (
	"errors"
	"fmt"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/lnp"
)

func TestExplainLocalNetwork(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app") // not Terminal: the setting applies
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	c := &Conn{}
	noRoute := fmt.Errorf("%w: ssh: connect to host 192.168.1.9 port 22: No route to host", ErrUnreachable)
	lan := []gatedTarget{{host: "192.168.1.9", port: 22}}

	var ln *LocalNetworkError
	if err := c.explainLocalNetwork(noRoute, lan); !errors.As(err, &ln) || ln.Responsible != "iTerm" || ln.State == lnp.Denied {
		t.Fatalf("no route to a LAN host should be explained as probable: %v", err)
	}
	if !errors.Is(ln, ErrUnreachable) {
		t.Fatal("LocalNetworkError must still be ErrUnreachable")
	}
	if err := c.explainLocalNetwork(noRoute, nil); errors.As(err, &ln) && err != noRoute {
		t.Fatal("a destination that is not gated must not be relabelled")
	}
	allowed := []gatedTarget{{host: "192.168.1.9", port: 22, state: lnp.Allowed}}
	if err := c.explainLocalNetwork(noRoute, allowed); err != noRoute {
		t.Fatal("a probe that got through means the setting is not the cause")
	}
	refused := fmt.Errorf("%w: kex_exchange_identification: Connection closed by remote host", ErrUnreachable)
	denied := []gatedTarget{{host: "mini.local", port: 22, state: lnp.Denied}}
	if err := c.explainLocalNetwork(refused, denied); !errors.As(err, &ln) || ln.State != lnp.Denied || ln.Host != "mini.local" {
		t.Fatalf("a confirmed denial explains any connection failure: %v", err)
	}
	if err := c.explainLocalNetwork(ErrAuth, denied); err != ErrAuth {
		t.Fatal("an authentication failure is never a local network problem")
	}
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	if err := c.explainLocalNetwork(noRoute, lan); err != noRoute {
		t.Fatal("Terminal is always allowed, so its failures are real network failures")
	}
	if !retryable(&LocalNetworkError{Host: "x"}) {
		t.Fatal("a local network block should make Run try the Tailscale route")
	}
}
