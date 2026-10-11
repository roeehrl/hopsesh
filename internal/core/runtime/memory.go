package runtime

import (
	"os"
	"runtime/debug"
)

// MemoryLimit keeps a collecting process's resident memory near what it uses: Go's
// collector otherwise lets the heap grow to twice the live data before collecting, so
// one large session read left hundreds of megabytes resident. It costs nothing while
// idle. Every process that may own the runtime (the desktop app, a headless runtime, a
// terminal UI) applies it: the cold-start peak happens in whichever one collects.
const MemoryLimit = 384 << 20

// LimitMemory applies MemoryLimit unless GOMEMLIMIT sets one.
func LimitMemory() {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(MemoryLimit)
	}
}
