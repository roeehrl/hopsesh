package opencode

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// required are the tables and columns the module reads. A database that lacks one was
// written by an OpenCode whose schema hopsesh does not know: it reads nothing from it
// rather than guess. Columns OpenCode adds later are fine.
var required = []struct {
	table string
	cols  []string
}{
	{"session_v2", []string{"id", "project_id", "workspace_id", "parent_id", "fork_session_id", "fork_boundary", "slug", "directory", "path", "title",
		"version", "metadata", "cost", "tokens_input", "tokens_output", "tokens_reasoning", "tokens_cache_read", "tokens_cache_write", "revert", "agent",
		"model", "time_created", "time_updated", "time_idle", "time_viewed", "idle_outcome", "time_archived", "time_suspended", "time_compacting"}},
	{"session_message", []string{"id", "session_id", "type", "seq", "time_created", "data"}},
	{"kv", []string{"key", "value", "time_updated"}},
	{"migration", []string{"id"}},
}

// Read when the database has them: OpenCode 2.2.1 added the session_part table and 2.2.2
// session_v2.time_busy (migrations 20261029002625_session_part and
// 20261029073502_session_time_busy). A database last opened by 2.1.x or 2.2.0 has
// neither.
var partCols = []string{"message_id", "session_id", "ordinal", "type", "data"}

const busyCol = "time_busy"

// dbSchema is what the gate found: the optional tables and columns the database has.
type dbSchema struct {
	parts bool // session_part
	busy  bool // session_v2.time_busy
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

// checked caches the gate's verdict for a database at a migration: OpenCode changes its
// schema only by migrations, so a database whose newest migration is the same has the
// same tables.
var checked = struct {
	sync.Mutex
	m map[string]dbSchema
}{m: map[string]dbSchema{}}

// open runs the schema gate (once per database and migration) and returns the reader
// set to the database's optional tables.
func (r sqlReader) open(ctx context.Context, machine string) (sqlReader, error) {
	key := ""
	if m := r.latestMigration(ctx); m != "" {
		key = machine + "\x00" + r.db + "\x00" + m
		checked.Lock()
		s, ok := checked.m[key]
		checked.Unlock()
		if ok {
			r.schema = s
			return r, nil
		}
	}
	s, err := r.checkSchema(ctx)
	if err != nil {
		return r, err
	}
	if key != "" {
		checked.Lock()
		checked.m[key] = s
		checked.Unlock()
	}
	r.schema = s
	return r, nil
}

// openSQL is the install's database behind the schema gate. ok is false where the
// machine has no SQL reader; err is the database's or the gate's refusal.
func openSQL(ctx context.Context, h agent.Host, in agent.Install) (sqlReader, bool, error) {
	r, ok := newSQLReader(h, in)
	if !ok {
		return r, false, nil
	}
	r, err := r.open(ctx, h.Facts().Machine)
	return r, true, err
}

// checkSchema checks that the database has every column the module reads, and which of
// the optional ones.
func (r sqlReader) checkSchema(ctx context.Context) (dbSchema, error) {
	has := func(cols []string, c string) bool {
		return slices.ContainsFunc(cols, func(x string) bool { return strings.EqualFold(x, c) })
	}
	missing := func(table string, cols, want []string) error {
		var out []string
		for _, c := range want {
			if !has(cols, c) {
				out = append(out, c)
			}
		}
		if len(out) > 0 {
			return &schemaError{Why: fmt.Sprintf("its %s table has no %s", table, strings.Join(out, ", ")), Migration: r.latestMigration(ctx)}
		}
		return nil
	}
	var s dbSchema
	for _, t := range required {
		cols, err := r.sq.Columns(ctx, r.db, t.table)
		if err != nil {
			return s, err
		}
		if len(cols) == 0 {
			if t.table == "session_v2" {
				return s, &schemaError{Why: "it has no session_v2 table, so it was written by OpenCode 1.x; hopsesh reads OpenCode " + minVersion + " and newer", Migration: r.latestMigration(ctx)}
			}
			return s, &schemaError{Why: "it has no " + t.table + " table", Migration: r.latestMigration(ctx)}
		}
		if err := missing(t.table, cols, t.cols); err != nil {
			return s, err
		}
		if t.table == "session_v2" {
			s.busy = has(cols, busyCol)
		}
	}
	cols, err := r.sq.Columns(ctx, r.db, "session_part")
	if err != nil {
		return s, err
	}
	if len(cols) > 0 {
		if err := missing("session_part", cols, partCols); err != nil {
			return s, err
		}
		s.parts = true
	}
	return s, nil
}
