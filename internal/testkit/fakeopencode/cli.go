package fakeopencode

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// session plays `opencode session <verb>`.
func (r *run) session(verb string, args []string) int {
	switch verb {
	case "list", "export", "import", "delete":
	default:
		return r.fail("the OpenCode stand-in does not play `opencode session %s`", verb)
	}
	if r.getenv("FAKE_OPENCODE_FAIL") == "session "+verb {
		return r.fail("Error: the stand-in was told to fail `opencode session %s`", verb)
	}
	r.server()
	db, err := r.open()
	if err != nil {
		return r.fail("Error: %v", err)
	}
	defer db.Close()
	switch verb {
	case "list":
		return r.list(db)
	case "export":
		if len(args) != 1 {
			return r.fail("the OpenCode stand-in exports a session only by its id (OpenCode asks which one)")
		}
		doc, err := export(db, args[0])
		if errors.Is(err, errNotFound) {
			return r.fail("Session not found: %s", args[0])
		} else if err != nil {
			return r.fail("Error: %v", err)
		}
		_, _ = r.Stdout.Write(append(doc.pretty(), '\n'))
		return 0
	case "import":
		return r.importFile(db, args)
	default: // delete
		if len(args) != 1 {
			return r.fail("ERROR\n  Missing argument <sessionID>")
		}
		if err := remove(db, args[0]); errors.Is(err, errNotFound) {
			return r.fail("Session not found: %s", args[0])
		} else if err != nil {
			return r.fail("Error: %v", err)
		}
		fmt.Fprintf(r.Stdout, "Session %s deleted\n", args[0])
		return 0
	}
}

// importFile plays `session import <file> [--directory D]`, with what OpenCode 2.3.0
// prints: "Imported session: <id>" on success; "Session already exists" on stderr, and
// exit status 0, when the id is taken; the server's refusal as "Error: Failed to import
// session: <status text>" otherwise (a missing parent is Not Found).
func (r *run) importFile(db *sql.DB, args []string) int {
	if len(args) != 1 {
		return r.fail("ERROR\n  Missing argument <file>")
	}
	file := args[0]
	if strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://") {
		return r.fail("the OpenCode stand-in imports only files")
	}
	if !filepath.IsAbs(file) && !strings.HasPrefix(file, "/") {
		file = filepath.Join(r.workdir(), file)
	}
	b, err := r.readFile(file)
	if err != nil {
		return r.fail("Error: Failed to read session data: ENOENT: no such file or directory, open '%s'", file)
	}
	doc, err := parseJSON(b)
	if err != nil {
		return r.fail("SchemaError: Expected a valid JSON string")
	}
	dir := r.dir
	if dir == "" {
		dir = r.workdir()
	} else if !filepath.IsAbs(dir) && !strings.HasPrefix(dir, "/") {
		dir = filepath.Join(r.workdir(), dir)
	}
	sid, err := importDoc(db, doc, dir, r.now(), r.serverVersion(), r.env, r.isDir)
	switch {
	case errors.Is(err, errExists):
		fmt.Fprintln(r.Stderr, "Session already exists")
		return 0
	case errors.Is(err, errNoFolder):
		return r.fail("LocationNotFoundError: Location not found: %s", dir)
	case errors.Is(err, errNotFound):
		return r.fail("Error: Failed to import session: Not Found")
	case errors.Is(err, errBadRequest):
		r.log("opencode: import refused: " + err.Error())
		return r.fail("Error: Failed to import session: Bad Request")
	case err != nil:
		r.log("opencode: import failed: " + err.Error())
		return r.fail("Error: Failed to import session: Internal Server Error")
	}
	fmt.Fprintf(r.Stdout, "Imported session: %s\n", sid)
	return 0
}

// list plays `session list`: the top-level sessions of the working folder's project,
// newest first, as a table (id, title, time) or JSON.
func (r *run) list(db *sql.DB) int {
	p := resolveProject(r.workdir(), r.env)
	limit := r.maxCount
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.Query("SELECT "+rowColumns+" FROM session_v2 WHERE project_id = ? AND parent_id IS NULL ORDER BY time_updated DESC, id DESC LIMIT ?", p.id, limit)
	if err != nil {
		return r.fail("Error: %v", err)
	}
	defer rows.Close()
	out := arr()
	var table []string
	for rows.Next() {
		s, err := scanRow(rows)
		if err != nil {
			return r.fail("Error: %v", err)
		}
		e := obj()
		e.set("id", str(s.id))
		e.set("title", str(s.title.String))
		e.set("updated", num(s.updated))
		e.set("created", num(s.created))
		e.set("projectId", str(s.projectID))
		e.set("directory", str(s.directory))
		out.vals = append(out.vals, e)
		table = append(table, fmt.Sprintf("%s\t%s\t%s", s.id, s.title.String, msTime(s.updated).Local().Format("1/2/2006, 3:04:05 PM")))
	}
	if r.format == "json" {
		_, _ = r.Stdout.Write(append(out.pretty(), '\n'))
		return 0
	}
	for _, l := range table {
		fmt.Fprintln(r.Stdout, l)
	}
	return 0
}
