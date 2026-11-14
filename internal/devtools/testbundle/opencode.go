package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/roeehrl/hopsesh/internal/testkit/fakeopencode"
)

// OpenCode keeps its sessions in a SQLite database, so its fixtures are SQL (schema.sql,
// data.sql, and synthetic rows on top of them) and the exports OpenCode printed. The
// bundle checks that the SQL builds a database whose JSON columns are JSON, that every
// export is a transfer OpenCode would import (the schema's envelope, each message type's
// members, as OpenCode's own schemas decode them), and that none holds what looks real.

// checkTransfer checks what the transfer schema cannot: that OpenCode 2.3.0's schemas
// decode it (each message type's members, content items, tool states; the stand-in
// decodes as OpenCode does) and keep all of it, that no message id is repeated, and that
// it holds settled messages only, as an export does.
func checkTransfer(b []byte) error {
	dropped, err := fakeopencode.CheckTransfer(b)
	if err != nil {
		return fmt.Errorf("OpenCode would refuse it (Bad Request): %w", err)
	}
	var errs []string
	for _, d := range dropped {
		errs = append(errs, d+": OpenCode does not know this member and drops it on import")
	}
	var t struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(b, &t); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, m := range t.Messages {
		id, _ := m["id"].(string)
		typ, _ := m["type"].(string)
		at := fmt.Sprintf("messages[%d] (%s)", i, typ)
		if seen[id] {
			errs = append(errs, at+": the id "+id+" is repeated")
		}
		seen[id] = true
		switch typ {
		case "assistant":
			if tm, _ := m["time"].(map[string]any); tm["completed"] == nil {
				errs = append(errs, at+": an export holds settled replies only (time.completed)")
			}
		case "shell", "compaction":
			if m["status"] == "running" {
				errs = append(errs, at+": an export holds settled messages only")
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// openCodeImports checks that OpenCode (as the stand-in plays it) imports the transfer
// and exports it again unchanged, but for what an import sets anew: the project, folder
// and update time, and the fork it came from. A child session gets a parent first.
func openCodeImports(b []byte) error {
	tmp, err := os.MkdirTemp("", "testbundle-opencode")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	db := filepath.Join(tmp, "opencode.db")
	var want map[string]any
	if err := json.Unmarshal(b, &want); err != nil {
		return err
	}
	info, _ := want["info"].(map[string]any)
	if parent, _ := info["parentID"].(string); parent != "" {
		stub := fmt.Sprintf(`{"info":{"id":%q,"projectID":"global","cost":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":1,"updated":1},"location":{"directory":%q}},"messages":[]}`, parent, filepath.ToSlash(tmp))
		if _, err := fakeopencode.Import(db, []byte(stub), tmp, time.Now()); err != nil {
			return fmt.Errorf("its parent: %w", err)
		}
	}
	sid, err := fakeopencode.Import(db, b, tmp, time.Now())
	if err != nil {
		return fmt.Errorf("OpenCode would not import it: %w", err)
	}
	out, err := fakeopencode.Export(db, sid)
	if err != nil {
		return err
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		return err
	}
	for _, d := range []map[string]any{want, got} {
		in, _ := d["info"].(map[string]any)
		for _, k := range []string{"projectID", "location", "fork", "subpath"} {
			delete(in, k)
		}
		if tm, ok := in["time"].(map[string]any); ok {
			delete(tm, "updated")
		}
	}
	if !reflect.DeepEqual(got, want) {
		return errors.New("OpenCode would not export it as it is after importing it (a member it drops, a time it rounds, or a message that is not settled)")
	}
	return nil
}

// checkOpenCodeSQL builds a database from an OpenCode fixture version's SQL (schema.sql,
// then data.sql, then any other .sql file) and checks that its JSON columns are JSON.
func checkOpenCodeSQL(files map[string][]byte) []string {
	if len(files) == 0 {
		return nil
	}
	tmp, err := os.MkdirTemp("", "testbundle-opencode")
	if err != nil {
		return []string{err.Error()}
	}
	defer os.RemoveAll(tmp)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(tmp, "check.db")))
	if err != nil {
		return []string{err.Error()}
	}
	defer db.Close()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	rank := func(n string) int {
		switch filepath.Base(n) {
		case "schema.sql":
			return 0
		case "data.sql":
			return 1
		}
		return 2
	}
	slices.SortFunc(names, func(a, b string) int {
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
	for _, n := range names {
		if _, err := db.Exec(string(files[n])); err != nil {
			return []string{fmt.Sprintf("%s: does not build a database: %v", n, err)}
		}
	}
	var errs []string
	for _, c := range []struct{ table, key, column string }{
		{"session_message", "id", "data"}, {"session_part", "message_id", "data"},
		{"session_v2", "id", "model"}, {"session_v2", "id", "metadata"}, {"session_v2", "id", "fork_boundary"},
	} {
		rows, err := db.Query(fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s IS NOT NULL", c.key, c.column, c.table, c.column))
		if err != nil {
			continue // a schema without the table (a synthetic set alone)
		}
		for rows.Next() {
			var key, v string
			if rows.Scan(&key, &v) == nil && !json.Valid([]byte(v)) {
				errs = append(errs, fmt.Sprintf("%s.%s of %s is not JSON", c.table, c.column, key))
			}
		}
		rows.Close()
	}
	return errs
}

// sqlStrings are the string literals of SQL text, unquoted: what scrub reads in a .sql
// fixture.
func sqlStrings(b []byte) []string {
	var out []string
	for i := 0; i < len(b); i++ {
		if b[i] != '\'' {
			continue
		}
		var s bytes.Buffer
		for i++; i < len(b); i++ {
			if b[i] == '\'' {
				if i+1 < len(b) && b[i+1] == '\'' {
					s.WriteByte('\'')
					i++
					continue
				}
				break
			}
			s.WriteByte(b[i])
		}
		out = append(out, s.String())
	}
	return out
}

// scrubText returns what in a text (or SQL) fixture does not look made up.
func scrubText(b []byte, isSQL bool) []string {
	parts := []string{string(b)}
	if isSQL {
		parts = sqlStrings(b)
	}
	found := map[string]bool{}
	for _, p := range parts {
		for _, f := range scrubString(p) {
			found[f] = true
		}
	}
	out := make([]string, 0, len(found))
	for f := range found {
		out = append(out, f)
	}
	slices.Sort(out)
	return out
}
