package gui

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// The system's notifications, started on first use; a build or system without them (an
// app that is not bundled, a Linux without a notification server) sends none.
var (
	notesOnce sync.Once
	notesSvc  *notifications.NotificationService
	notesOK   atomic.Bool
	notesSeq  atomic.Int64
)

// osNotify sends one desktop notification about tab id; clicking it shows that tab.
func osNotify(t *Terminals, id, title, body string) {
	defer func() { _ = recover() }() // a notification is never worth the app
	t.mu.Lock()
	app := t.app
	t.mu.Unlock()
	if app == nil {
		return
	}
	notesOnce.Do(func() {
		svc := notifications.New()
		if err := svc.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
			return
		}
		svc.OnNotificationResponse(func(r notifications.NotificationResult) {
			tab, _, _ := strings.Cut(strings.TrimPrefix(r.Response.ID, "tab-"), "-")
			if r.Error == nil && tab != "" {
				t.Focus(tab)
			}
		})
		ok, err := svc.CheckNotificationAuthorization()
		if err == nil && !ok {
			ok, err = svc.RequestNotificationAuthorization()
		}
		notesSvc = svc
		notesOK.Store(err == nil && ok)
	})
	if !notesOK.Load() {
		return
	}
	_ = notesSvc.SendNotification(notifications.NotificationOptions{
		ID: "tab-" + id + "-" + strconv.FormatInt(notesSeq.Add(1), 10), Title: title, Body: body,
	})
}

// setBadge shows n (0: nothing) on the app's Dock icon.
func setBadge(n int) {
	defer func() { _ = recover() }()
	d := dock.New()
	if n == 0 {
		_ = d.RemoveBadge()
		return
	}
	_ = d.SetBadge(strconv.Itoa(n))
}
