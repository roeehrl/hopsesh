package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

type baseMod struct{}

func (baseMod) Spec() Spec                                                     { return Spec{ID: "x"} }
func (baseMod) Detect(context.Context, Host) (Install, error)                  { return Install{}, nil }
func (baseMod) List(context.Context, Host, Install) (Listing, error)           { return Listing{}, nil }
func (baseMod) Bundle(context.Context, Host, Install, Summary) (Bundle, error) { return Bundle{}, nil }
func (baseMod) PlanMove(Install, Install, Summary, Bundle, Placement) (MovePlan, error) {
	return MovePlan{}, nil
}
func (baseMod) Verify(context.Context, Host, MovePlan, map[string]string, Placement) error {
	return nil
}
func (baseMod) Resume(Install, SessionKey, Placement, ResumeOptions) Command { return Command{} }

type virtualMod struct{ baseMod }

func (virtualMod) VirtualSessions() {}

type stamperMod struct{ virtualMod }

func (stamperMod) SessionStamp(_ context.Context, _ Host, _ Install, id SessionID) (SessionStamp, error) {
	if id == "gone" {
		return "", ErrNotFound
	}
	return "stamp-" + SessionStamp(id), nil
}

type minterMod struct{ baseMod }

func (minterMod) NewSessionID(Install, time.Time) SessionID { return "ses_new" }

type vendorMod struct {
	baseMod
	via bool
}

func (vendorMod) AfterInstall(context.Context, Host, Install, SessionKey, Placement) error {
	return nil
}
func (v vendorMod) InstallsViaVendor() bool { return v.via }

func TestSessionStoreHelpers(t *testing.T) {
	ctx := context.Background()
	s := Summary{Key: SessionKey{Agent: "x", Session: "ses_1"}, Size: 4, LastActivity: time.Unix(100, 5)}
	if st, ok, err := StampOf(ctx, baseMod{}, nil, Install{}, s); ok || st != "" || err != nil {
		t.Fatalf("a file module has no stamp: %q %v %v", st, ok, err)
	}
	st, ok, err := StampOf(ctx, virtualMod{}, nil, Install{}, s)
	if !ok || err != nil || st != SummaryStamp(s) || st != "4@100000000005" {
		t.Fatalf("a virtual module's stamp comes from its summary: %q", st)
	}
	more := s
	more.Size = 5
	if SummaryStamp(more) == st {
		t.Fatal("a new message must change the stamp")
	}
	if st, ok, err := StampOf(ctx, stamperMod{}, nil, Install{}, s); !ok || err != nil || st != "stamp-ses_1" {
		t.Fatalf("a module's own stamp comes first: %q", st)
	}
	s.Key.Session = "gone"
	if _, ok, err := StampOf(ctx, stamperMod{}, nil, Install{}, s); !ok || !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing session: %v", err)
	}
	if !IsVirtual(stamperMod{}) || IsVirtual(baseMod{}) {
		t.Fatal("IsVirtual")
	}
	uuid := func() string { return "0b0c-uuid" }
	if NewSessionID(minterMod{}, Install{}, time.Now(), uuid) != "ses_new" || NewSessionID(baseMod{}, Install{}, time.Now(), uuid) != "0b0c-uuid" {
		t.Fatal("NewSessionID")
	}
	if !InstallsViaVendor(vendorMod{via: true}) || InstallsViaVendor(vendorMod{}) || InstallsViaVendor(baseMod{}) {
		t.Fatal("InstallsViaVendor")
	}
	// The contracts are optional: they add no capability a module did not have.
	if len(Capabilities(stamperMod{})) != 0 || !Has(vendorMod{}, CapPostInstall) {
		t.Fatalf("Capabilities: %v", Capabilities(stamperMod{}))
	}
}
