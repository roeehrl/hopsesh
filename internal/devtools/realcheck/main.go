// Command realcheck reads every agent's real sessions on this machine with the compiled-in
// modules: listing, the conversation reader and lineage, and reports anything a module
// cannot read. Read-only.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func main() {
	ctx := context.Background()
	a := app.New(config.Defaults(), all.Registry(), os.TempDir(), nil)
	inv := a.Scan(ctx, app.ScanOptions{Hosts: []string{app.LocalName()}, SkipGit: true})
	defer inv.Close()
	here := inv.Local()
	bad := 0
	for _, st := range here.Agents {
		if !st.Install.Present {
			fmt.Printf("%s: not installed\n", st.Name)
			continue
		}
		mod, _ := a.Module(st.Agent)
		h, err := here.Host().For(ctx, mod.Spec(), st.Install, nil)
		if err != nil {
			panic(err)
		}
		l, err := mod.List(ctx, h, st.Install)
		if err != nil {
			fmt.Printf("%s: listing failed: %v\n", st.Name, err)
			bad++
			continue
		}
		read, readErr, nodes, noTitle := 0, 0, 0, 0
		reader, canRead := mod.(agent.Reader)
		for _, s := range l.Sessions {
			if s.Title == "" {
				noTitle++
			}
			if !canRead {
				continue
			}
			seg, err := reader.Read(ctx, h, st.Install, s, ir.Cursor{})
			if err != nil {
				readErr++
				fmt.Printf("  READ %s: %v\n", s.Key, err)
				continue
			}
			read++
			nodes += len(seg.Nodes)
		}
		bad += len(l.Errors) + readErr
		fmt.Printf("%s %s: sessions=%d listErrors=%d read=%d readErrors=%d nodes=%d noTitle=%d\n",
			st.Name, st.Install.Version, len(l.Sessions), len(l.Errors), read, readErr, nodes, noTitle)
		for _, e := range l.Errors {
			fmt.Printf("  LIST %v\n", e)
		}
	}
	if bad > 0 {
		os.Exit(1)
	}
}
