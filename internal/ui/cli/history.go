package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/roeehrl/hopsesh/internal/config"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

func historyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show or set how much history transfers carry and the resources they may use",
		Long: `When a session continues in another agent, the destination gets a bounded working
context; the complete conversation stays in a portable archive it can consult (hopsesh archive).

  --context-budget   lower the destination context (0: automatic, from the destination model;
                     a budget can never exceed what the model allows)
  --older            extract: a labeled extract of older history plus recent turns (default);
                     recent: recent turns only, with a note that older entries were left out

Resource limits (MiB; 0 restores the default) stop a transfer before anything is written
when reached, naming the limit. History is never silently truncated:

  --read-memory-mb   conversation a native transcript may hold in memory while read
  --record-mb        one native record
  --archive-mb       the portable archive, including earlier transfers
  --native-file-mb   whole native files read for fork checks, recovery and verification

Without flags it shows the current values. pull --context-budget/--older override them for
one transfer.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := newRun(cmd)
			if err != nil {
				return err
			}
			defer r.app.Catalog.Close()
			f := cmd.Flags()
			h := r.app.Cfg.History
			changed := false
			for _, fl := range []struct {
				name string
				dst  *int
			}{{"context-budget", &h.ContextBudget}, {"read-memory-mb", &h.ReadMemoryMB}, {"record-mb", &h.RecordMB}, {"archive-mb", &h.ArchiveMB}, {"native-file-mb", &h.NativeFileMB}} {
				if f.Changed(fl.name) {
					*fl.dst, _ = f.GetInt(fl.name)
					changed = true
				}
			}
			if f.Changed("older") {
				h.Older, _ = f.GetString("older")
				if h.Older == ir.OlderExtract {
					h.Older = ""
				}
				changed = true
			}
			if changed {
				c := r.app.Cfg
				c.History = h
				if err := c.Check(); err != nil {
					return err
				}
				r.app.Cfg.History = h
				if err := config.Save(r.app.Cfg); err != nil {
					return err
				}
			}
			l := h.Limits()
			if asJSON, _ := f.GetBool("json"); asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(l)
			}
			budget := "automatic (from the destination model)"
			if l.ContextBudget > 0 {
				budget = fmt.Sprintf("up to %d (upper estimate)", l.ContextBudget)
			}
			d := ir.DefaultLimits()
			size := func(v, def int64) string {
				s := fmt.Sprintf("%d MiB", v>>20)
				if v == def {
					s += " (default)"
				}
				return s
			}
			r.printf("Context budget:    %s\n", budget)
			r.printf("Older history:     %s\n", map[string]string{ir.OlderExtract: "labeled extract plus recent turns", ir.OlderRecent: "recent turns only"}[l.Older])
			r.printf("Reading memory:    %s\n", size(l.ReadBytes, d.ReadBytes))
			r.printf("Largest record:    %s\n", size(l.RecordBytes, d.RecordBytes))
			r.printf("Portable archive:  %s\n", size(l.ArchiveBytes, d.ArchiveBytes))
			r.printf("Native file checks: %s\n", size(l.NativeFileBytes, d.NativeFileBytes))
			return nil
		},
	}
	f := cmd.Flags()
	f.Int("context-budget", 0, "destination context budget (0: automatic)")
	f.String("older", "", "extract or recent")
	f.Int("read-memory-mb", 0, "reading memory, MiB (0: default)")
	f.Int("record-mb", 0, "largest native record, MiB (0: default)")
	f.Int("archive-mb", 0, "portable archive size, MiB (0: default)")
	f.Int("native-file-mb", 0, "native file checks, MiB (0: default)")
	f.Bool("json", false, "output JSON")
	return cmd
}
