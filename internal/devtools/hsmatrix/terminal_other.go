//go:build windows

package main

// hsTerminal is hs on Windows, where the matrix skips the cloud hand-offs.
func (r *runner) hsTerminal(args ...string) (string, error) { return r.hs(true, args...) }
