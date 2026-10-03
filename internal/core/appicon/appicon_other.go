//go:build !darwin && !windows

package appicon

import "errors"

// iconOf has no app format to read on this system.
func iconOf(string) ([]byte, error) { return nil, errors.New("no desktop app icons here") }
