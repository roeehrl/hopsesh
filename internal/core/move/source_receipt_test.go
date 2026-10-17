package move

import (
	"context"
	"errors"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/host"
)

func TestSourceAuthorizationRefusedBeforeAnyApplyWork(t *testing.T) {
	refused := errors.New("approved cloud incarnation was revoked")
	called := false
	// Deliberately omit all machinery: no profile, journal, filesystem or native
	// writer may be reached after the source authorization check fails.
	result, err := Apply(t.Context(), &Plan{}, Input{CheckSource: func(context.Context) error { called = true; return refused }}, Env{StateDir: t.TempDir()})
	if !called || !errors.Is(err, refused) || result != nil {
		t.Fatalf("source revocation did not prevent apply: %v", err)
	}
}

func TestReadOnlySourceReceiptHasExplicitLocalOwnership(t *testing.T) {
	owner := &ReceiptOwner{FS: host.LocalFS(), Machine: "local-owner", NativePath: "private-checkpoint-ledger"}
	in := Input{SourceReceipt: owner}
	f, m, p, err := sourceReceipt(t.Context(), in)
	if err != nil || f != owner.FS || m != owner.Machine || p != owner.NativePath {
		t.Fatal("receipt was routed to the vendor source", err)
	}
	if _, err := machinesOf(t.Context(), in)(owner.Machine); err != nil {
		t.Fatal("local source receipt cannot be sealed or recovered", err)
	}
}
