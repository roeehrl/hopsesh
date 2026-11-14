package fakeopencode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The stand-in is held to what OpenCode 2.3.0 printed in the sandbox capture
// (agents/opencode/testdata/2.3.0): its schema, its exports and its answers.

const fixture = "../../../agents/opencode/testdata/2.3.0"

// The embedded schema is the fixture's, with the migrations OpenCode recorded.
func TestSchemaIsTheFixtures(t *testing.T) {
	schema, err := os.ReadFile(filepath.Join(fixture, "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(fixture, "data.sql"))
	if err != nil {
		t.Fatal(err)
	}
	want := string(schema) + "-- The migrations OpenCode 2.3.0 has run on a new database.\n"
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, `INSERT INTO "migration"`) {
			want += l + "\n"
		}
	}
	if schemaSQL != want {
		t.Error("schema.sql differs from agents/opencode/testdata/2.3.0 (schema.sql and the migration rows of data.sql)")
	}
}

type indexed struct{ file, id, parent string }

func exportIndex(t *testing.T) []indexed {
	t.Helper()
	f, err := os.Open(filepath.Join(fixture, "export-index.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []indexed
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c := strings.Split(sc.Text(), "\t")
		if len(c) >= 3 {
			out = append(out, indexed{file: c[0], id: c[1], parent: c[2]})
		}
	}
	if len(out) == 0 {
		t.Fatal("export-index.tsv lists no export")
	}
	return out
}

// fixtureDB is a database holding the sandbox's rows.
func fixtureDB(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "opencode.db")
	db, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	data, err := os.ReadFile(filepath.Join(fixture, "data.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, l := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(l, `INSERT INTO "migration"`) {
			rows = append(rows, l)
		}
	}
	if _, err := db.Exec(strings.Join(rows, "\n")); err != nil {
		t.Fatal(err)
	}
	return file
}

// The stand-in exports the sandbox's sessions byte for byte as OpenCode 2.3.0 did, from
// the same rows: member order, number and string printing, part rows merged, unsettled
// messages left out.
func TestExportIsOpenCodes(t *testing.T) {
	file := fixtureDB(t)
	db, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, e := range exportIndex(t) {
		want, err := os.ReadFile(filepath.Join(fixture, e.file))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := export(db, e.id)
		if err != nil {
			t.Fatalf("%s: %v", e.id, err)
		}
		if got := append(doc.pretty(), '\n'); !bytes.Equal(got, want) {
			t.Errorf("%s (%s): the export differs from OpenCode's:\n%s", e.file, e.id, firstDiff(string(got), string(want)))
		}
	}
}

// Importing OpenCode's export and exporting it again gives the same document, except
// what OpenCode's import itself sets anew: the update time, the folder (here another
// one), and the fork a session came from (import does not carry it).
func TestImportExportRoundTrip(t *testing.T) {
	dir := gitRepo(t, "https://example.com/u/demo.git")
	file := filepath.Join(t.TempDir(), "opencode.db")
	now := time.UnixMilli(1794600000000)
	idx := exportIndex(t)
	// Parents before their children.
	for pass := 0; pass < 2; pass++ {
		for _, e := range idx {
			if (e.parent == "") != (pass == 0) {
				continue
			}
			b, err := os.ReadFile(filepath.Join(fixture, e.file))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Import(file, b, dir, now); err != nil {
				t.Fatalf("%s: %v", e.file, err)
			}
		}
	}
	db, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, e := range idx {
		b, _ := os.ReadFile(filepath.Join(fixture, e.file))
		var want, got map[string]any
		if err := json.Unmarshal(b, &want); err != nil {
			t.Fatal(err)
		}
		doc, err := export(db, e.id)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(doc.compact(), &got); err != nil {
			t.Fatal(err)
		}
		wi, gi := want["info"].(map[string]any), got["info"].(map[string]any)
		if gi["time"].(map[string]any)["updated"] != float64(now.UnixMilli()) {
			t.Errorf("%s: imported at %v", e.file, gi["time"])
		}
		if gi["location"].(map[string]any)["directory"] != dir {
			t.Errorf("%s: imported into %v", e.file, gi["location"])
		}
		if gi["projectID"] != demoProject {
			t.Errorf("%s: imported into the project %v, want %s", e.file, gi["projectID"], demoProject)
		}
		for _, m := range []map[string]any{wi, gi} {
			delete(m["time"].(map[string]any), "updated")
			delete(m, "location")
			delete(m, "projectID")
			delete(m, "fork")
		}
		if !reflect.DeepEqual(got, want) {
			gb, _ := json.MarshalIndent(got, "", " ")
			wb, _ := json.MarshalIndent(want, "", " ")
			t.Errorf("%s: the round trip changed the session:\n%s", e.file, firstDiff(string(gb), string(wb)))
		}
	}
}

// demoProject is the project id OpenCode gave the sandbox's repository, whose origin was
// https://example.com/u/demo.git.
const demoProject = "e1eece87d8935f099635f505ed301e85a3e1ae27"

// gitRepo is a repository with an origin, in a temporary folder.
func gitRepo(t *testing.T, origin string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{{"init", "-q"}, {"remote", "add", "origin", origin}} {
		cmd := exec.Command("git", a...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", a, err, out)
		}
	}
	return dir
}

// firstDiff shows where two texts first differ.
func firstDiff(got, want string) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	from := max(0, i-120)
	return "got:  …" + clip(got[from:], 240) + "\nwant: …" + clip(want[from:], 240)
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
