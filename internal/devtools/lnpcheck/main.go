// Command lnpcheck reports, for each host given, whether macOS local network privacy
// applies to it and what an in-process probe sees. It is a debugging aid.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/lnp"
)

func main() {
	ctx := context.Background()
	fmt.Printf("gated=%v canProbe=%v inApp=%v responsible=%q\n", lnp.Gated(), lnp.CanProbe, lnp.InApp(), lnp.Responsible())
	for _, h := range os.Args[1:] {
		need, addr := lnp.Needs(ctx, h)
		start := time.Now()
		st := lnp.Probe(ctx, h, 22, 3*time.Second)
		fmt.Printf("%-28s needs=%-5v addr=%-16s probe=%s (%v)\n", h, need, addr, st, time.Since(start).Round(time.Millisecond))
	}
}
