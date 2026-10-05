//go:build !darwin

package gui

// osNotify: no system notifications here (see notify.go); the window's taskbar button
// flashes on Windows.
func osNotify(*Terminals, string, string, string) {}

// setBadge: no Dock here.
func setBadge(int) {}
