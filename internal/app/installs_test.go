package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/installs"
)

// fakeInstaller records what an update does, and lists what the machine has after.
type fakeInstaller struct {
	calls   []string
	appFail bool
	after   installs.List
}

func (f *fakeInstaller) run(_ context.Context, program string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, "run "+program+" "+strings.Join(args, " "))
	if f.appFail && len(args) > 0 && args[0] == "update" {
		return []byte("cannot replace /Applications/hopsesh.app (permission denied)"), errors.New("exit 1")
	}
	return []byte("hopsesh 0.6.4 (commit x)\n"), nil
}
func (f *fakeInstaller) put(_ context.Context, p string) error {
	f.calls = append(f.calls, "put "+p)
	return nil
}
func (f *fakeInstaller) link(_ context.Context, p, target string) error {
	f.calls = append(f.calls, "link "+p+" → "+target)
	return nil
}
func (f *fakeInstaller) list(context.Context) (installs.List, error) { return f.after, nil }

const (
	laptopApp = "/Applications/hopsesh.app/Contents/Resources/bin/hopsesh"
	laptopOwn = "/Users/kim/.hopsesh/bin/hopsesh"
)

func laptop(app, own string) installs.List {
	return installs.Parse([]byte(strings.Join([]string{
		"at\t" + laptopOwn + "\t" + laptopOwn, "ver\t" + laptopOwn + "\thopsesh " + own,
		"at\t" + laptopApp + "\t" + laptopApp, "ver\t" + laptopApp + "\thopsesh " + app,
		"term\t" + laptopApp, "svc\t" + laptopOwn + "\t1",
	}, "\n")), "darwin", "arm64", "/Users/kim")
}

func TestApplyInstallsUpdatesTheAppThenLinks(t *testing.T) {
	before := laptop("0.5.0", "0.6.3")
	f := &fakeInstaller{after: laptop("0.6.4", "0.6.3")}
	var steps []SetupStep
	program, _, err := applyInstalls(context.Background(), f, before, installs.Plan(before, "0.6.4", laptopOwn), func(s SetupStep) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run " + laptopApp + " update --yes", "run " + laptopApp + " version", "link " + laptopOwn + " → " + laptopApp}
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s", strings.Join(f.calls, "\n"))
	}
	if program != laptopApp {
		t.Fatalf("program %s", program)
	}
	if len(steps) != 2 || steps[0].Name != "Update the app" || steps[0].Detail != "hopsesh 0.6.4 in /Applications/hopsesh.app" || steps[1].Name != "One hopsesh" {
		t.Fatalf("steps: %+v", steps)
	}
}

// When the app can't be updated, hopsesh's own copy is kept and keeps running the
// machine: linking it to the older app, or starting the app at login, would downgrade it.
func TestApplyInstallsKeepsTheNewerCopyWhenTheAppFails(t *testing.T) {
	before := laptop("0.5.0", "0.6.3")
	f := &fakeInstaller{appFail: true, after: before}
	var steps []SetupStep
	program, _, err := applyInstalls(context.Background(), f, before, installs.Plan(before, "0.6.4", laptopOwn), func(s SetupStep) { steps = append(steps, s) })
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err %v", err)
	}
	if got := f.calls[len(f.calls)-1]; got != "put "+laptopOwn {
		t.Fatalf("hopsesh's own copy is brought to this version instead of linked: %v", f.calls)
	}
	if program != laptopOwn {
		t.Fatalf("program %s", program)
	}
	if steps[0].State != "failed" || steps[1].State != "waiting" || !strings.Contains(steps[1].Detail, "until the app's hopsesh is updated") {
		t.Fatalf("steps: %+v", steps)
	}
}

func TestCopiesCheck(t *testing.T) {
	l := laptop("0.5.0", "0.6.3")
	c := copiesCheck(&l, false)
	if c.State != CheckWarn || c.Fix != FixUpdate || c.Text != "hopsesh's copy 0.6.3 · app 0.5.0" {
		t.Fatalf("%+v", c)
	}
	if c := copiesCheck(&l, true); c.Fix != FixAlign {
		t.Fatalf("here: %+v", c)
	}
	one := laptop("0.6.4", "0.6.4")
	if c := copiesCheck(&one, false); c.State != CheckOK {
		t.Fatalf("%+v", c)
	}
	if c := copiesCheck(nil, false); c.State != "" {
		t.Fatalf("not read: %+v", c)
	}
}
