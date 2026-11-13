package opencode

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// required are the tables and columns the module reads. A database that lacks one was
// written by an OpenCode whose schema hopsesh does not know: it lists nothing from it
// rather than guess. Columns OpenCode adds later are fine.
var required = []struct {
	table string
	cols  []string
}{
	{"session_v2", []string{"id", "project_id", "workspace_id", "parent_id", "fork_session_id", "fork_boundary", "slug", "directory", "path", "title",
		"version", "metadata", "cost", "tokens_input", "tokens_output", "tokens_reasoning", "tokens_cache_read", "tokens_cache_write", "revert", "agent",
		"model", "time_created", "time_updated", "time_busy", "time_idle", "time_viewed", "idle_outcome", "time_archived", "time_suspended", "time_compacting"}},
	{"session_message", []string{"id", "session_id", "type", "seq", "time_created", "data"}},
	{"session_part", []string{"message_id", "session_id", "ordinal", "type", "data"}},
}

// schemaError is a database whose schema the module does not read.
type schemaError struct {
	Why       string
	Migration string // the database's newest migration, when known
}

func (e *schemaError) Error() string {
	msg := "hopsesh does not recognise this OpenCode database: " + e.Why
	if e.Migration != "" {
		msg += " (its latest migration is " + e.Migration + ")"
	}
	return msg
}

func (e *schemaError) Unwrap() error { return agent.ErrUnsupported }

// checkSchema checks that the database has every column the module reads.
func (r sqlReader) checkSchema(ctx context.Context) error {
	for _, t := range required {
		cols, err := r.sq.Columns(ctx, r.db, t.table)
		if err != nil {
			return err
		}
		if len(cols) == 0 {
			if t.table == "session_v2" {
				return &schemaError{Why: "it has no session_v2 table, so it was written by OpenCode 1.x; hopsesh reads OpenCode " + minVersion + " and newer", Migration: r.latestMigration(ctx)}
			}
			return &schemaError{Why: "it has no " + t.table + " table", Migration: r.latestMigration(ctx)}
		}
		var missing []string
		for _, c := range t.cols {
			if !slices.ContainsFunc(cols, func(x string) bool { return strings.EqualFold(x, c) }) {
				missing = append(missing, c)
			}
		}
		if len(missing) > 0 {
			return &schemaError{Why: fmt.Sprintf("its %s table has no %s", t.table, strings.Join(missing, ", ")), Migration: r.latestMigration(ctx)}
		}
	}
	return nil
}
