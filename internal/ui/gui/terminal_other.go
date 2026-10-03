//go:build !windows

package gui

import "errors"

func openWindowsTerminal(string) error { return errors.New("not Windows") }
