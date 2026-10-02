//go:build !darwin || !cgo

package lnp

import (
	"context"
	"time"
)

func probe(context.Context, string, int, time.Duration) State { return Unknown }

// CanProbe reports whether this build can run Probe (macOS builds with cgo).
const CanProbe = false
