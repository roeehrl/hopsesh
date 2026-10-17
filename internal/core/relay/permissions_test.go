package relay

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSendPermissionDoesNotGrantIncomingMachineAccess(t *testing.T) {
	a, _ := identities(t)
	s := Store{Directory: privateTemp(t)}
	g := Grant{Peer: a.Public, Kind: "device", SendMethods: []string{"hello", "plan", "apply", "undo"}}
	if err := s.Approve(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	for _, method := range g.SendMethods {
		if !g.AllowsSend(method, time.Now()) || g.Allows(method, time.Now()) {
			t.Fatal("sending granted incoming access", method)
		}
	}
	g.Methods = []string{"observe"}
	g.SendMethods = nil
	if !g.Allows("observe", time.Now()) || g.AllowsSend("observe", time.Now()) {
		t.Fatal("incoming scope granted outgoing permission")
	}
	g.Revoked = true
	if g.Allows("observe", time.Now()) || g.AllowsSend("apply", time.Now()) {
		t.Fatal("revocation ignored")
	}
}

func TestCloudGrantCannotEscalateThroughSendScope(t *testing.T) {
	s := Store{Directory: privateTemp(t)}
	i, err := s.Identity(context.Background(), "native-endpoint-A")
	if err != nil {
		t.Fatal(err)
	}
	if i.Public.Endpoint != "native-endpoint-A" {
		t.Fatal("endpoint missing")
	}
	g := Grant{Peer: i.Public, Kind: "cloud-session", Methods: []string{"observe"}, SendMethods: []string{"apply"}, Expires: time.Now().Add(time.Hour).Unix()}
	if err = s.Approve(context.Background(), g); err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("cloud session granted machine writes", err)
	}
}
