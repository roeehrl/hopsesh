//go:build !darwin

package lnp

// Only macOS has local network privacy.
func gated() bool { return false }
