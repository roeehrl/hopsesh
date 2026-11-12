package gui

import (
	"errors"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/app"
)

// Companion is MyCNC Host on this machine, for the window's recommendation and its
// "Follow on your phone" action.
func (a *App) Companion() app.Companion { return app.DetectCompanion() }

// OpenCompanionPage opens MyCNC's page.
func (a *App) OpenCompanionPage() error { return openInBrowser(app.CompanionPage) }

// endpointOf is a machine's identity, from the last scan ("" when it has none yet).
func (a *App) endpointOf(machine string) string {
	if a.inv == nil {
		return ""
	}
	if m := a.inv.Machine(machine); m != nil && m.Host() != nil {
		return m.Host().Facts.Endpoint
	}
	return ""
}

// FollowOnPhone opens a session in MyCNC on this machine, which shows it on the phone.
func (a *App) FollowOnPhone(machine, key string) error {
	if !app.DetectCompanion().Installed {
		return errors.New(app.CompanionName + " Host isn't on this Mac")
	}
	a.mu.Lock()
	e, err := a.find(machine, key)
	endpoint := a.endpointOf(machine)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if e.Location.IsCloud() {
		return errors.New("a cloud session can't be followed from " + app.CompanionName)
	}
	if endpoint == "" {
		return errors.New(machine + " has no hopsesh identity yet: set hopsesh up there first")
	}
	return openInBrowser(app.CompanionLink(endpoint, machine, e.Session.Key))
}

// SessionLink is hopsesh's link to a session, which opens it in hopsesh (Copy link).
func (a *App) SessionLink(machine, key string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, err := a.find(machine, key)
	if err != nil {
		return "", err
	}
	endpoint := a.endpointOf(machine)
	if endpoint == "" || e.Location.IsCloud() {
		return "", errors.New("this session has no link: its machine has no hopsesh identity yet")
	}
	return app.SessionLink(endpoint, e.Session.Key), nil
}

// OpenLink shows the session a hopsesh:// link names (another app opened it). A link to a
// session hopsesh doesn't list yet opens the window on the sessions.
func (a *App) OpenLink(raw string) error {
	if !strings.HasPrefix(raw, "hopsesh://") {
		return errors.New("not a hopsesh link")
	}
	endpoint, k, ok := app.ParseSessionLink(raw)
	if !ok {
		_ = a.QuickOpen("sessions", "", "")
		return errors.New("hopsesh can't read that link")
	}
	show := func() bool {
		a.mu.Lock()
		machine := ""
		if a.inv != nil {
			for _, m := range a.inv.Machines {
				if m.Host() != nil && m.Host().Facts.Endpoint == endpoint {
					machine = m.Name
				}
			}
		}
		a.mu.Unlock()
		return machine != "" && a.QuickOpen("sessions", machine, k.String()) == nil
	}
	if show() {
		return nil
	}
	// Opened before the sessions were read (the link launched hopsesh): show the
	// window now, and the session once a scan finds it.
	if err := a.QuickOpen("sessions", "", ""); err != nil {
		return err
	}
	go func() {
		for range 30 {
			time.Sleep(time.Second)
			if show() {
				return
			}
		}
	}()
	return nil
}

// LinkArg is a hopsesh:// link among a program's arguments (Windows passes the link a
// second launch was opened with), "" when there is none.
func LinkArg(args []string) string {
	for _, s := range args {
		if strings.HasPrefix(s, "hopsesh://") {
			return s
		}
	}
	return ""
}
