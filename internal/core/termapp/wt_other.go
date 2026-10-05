//go:build !windows

package termapp

import "errors"

func openWindowsTerminal(string) error { return errors.New("not Windows") }
