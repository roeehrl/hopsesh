//go:build !darwin && !linux && !windows

package serve

import "os"

// Elsewhere the private folder alone keeps other users out.
func peerUID(int) int { return os.Getuid() }
