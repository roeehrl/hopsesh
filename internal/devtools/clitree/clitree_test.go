package clitree

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

const rootHelp = `hopsesh finds your sessions.

Usage:
  hopsesh [flags]
  hopsesh [command]

Available Commands:
  clouds      List the agents' clouds
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  ls          List sessions

Flags:
  -h, --help             help for hopsesh
      --password-stdin   for machines that log in with a password: read it from standard input (for scripts)

Use "hopsesh [command] --help" for more information about a command.
`

const cloudsHelp = `List the agents' clouds

Usage:
  hopsesh clouds [flags]
  hopsesh clouds [command]

Available Commands:
  cleanup     Delete merged hand-off branches

Flags:
  -h, --help   help for clouds
      --json   output JSON

Global Flags:
      --password-stdin   for machines that log in with a password: read it from standard input (for scripts)
`

const cleanupHelp = `Usage:
  hopsesh clouds cleanup [flags]

Flags:
  -h, --help   help for cleanup
      --json   output JSON
      --list   only list
  -y, --yes    delete without asking
      --tag strings   tags (repeat
                      or comma separate)
      --mode[=auto]   a value with a default when given bare

Global Flags:
      --password-stdin   read the password from standard input
`

const lsHelp = `Usage:
  hopsesh ls [flags]

Flags:
      --host string   only this machine
      --json          output JSON
`

func helpTree(t *testing.T) Tree {
	t.Helper()
	texts := map[string]string{"": rootHelp, "clouds": cloudsHelp, "clouds cleanup": cleanupHelp, "ls": lsHelp}
	tree, err := FromHelp("hopsesh", func(words []string) (string, error) {
		s, ok := texts[strings.Join(words, " ")]
		if !ok {
			t.Fatalf("asked for help of %v", words)
		}
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestParseHelp(t *testing.T) {
	h := ParseHelp(rootHelp)
	if !slices.Equal(h.Children, []string{"clouds", "ls"}) {
		t.Errorf("children %v: help and completion are cobra's", h.Children)
	}
	if !reflect.DeepEqual(h.Flags, []Flag{{Name: "password-stdin"}}) || h.Global != nil {
		t.Errorf("flags %+v, global %+v", h.Flags, h.Global)
	}
	c := ParseHelp(cleanupHelp)
	want := []Flag{{Name: "json"}, {Name: "list"}, {Name: "yes", Short: "y"}, {Name: "tag", Arg: "strings"}, {Name: "mode", Arg: "[=auto]"}}
	if !reflect.DeepEqual(c.Flags, want) {
		t.Errorf("flags %+v, want %+v", c.Flags, want)
	}
	if !reflect.DeepEqual(c.Global, []Flag{{Name: "password-stdin"}}) || c.Children != nil {
		t.Errorf("global %+v, children %v", c.Global, c.Children)
	}
}

func TestGolden(t *testing.T) {
	want := `hopsesh
  --password-stdin
hopsesh clouds
  --json
hopsesh clouds cleanup
  --json
  --list
  --mode [=auto]
  --tag strings
  -y, --yes
hopsesh ls
  --host string
  --json
`
	if got := helpTree(t).Golden(); got != want {
		t.Errorf("golden:\n%s", got)
	}
}

func TestCheckSkill(t *testing.T) {
	tree := helpTree(t)
	good := map[string][]byte{"SKILL.md": []byte("Run `hopsesh ls --json [--host M]` and `hopsesh clouds cleanup --list --json`; " +
		"`clouds cleanup` asks; `hopsesh clouds --json`. Run it as `hopsesh`; see `.hopsesh/notes.md`.\n\n" +
		"## clouds cleanup and ls flags\n\n| Flag | Meaning |\n|---|---|\n| `--json` | JSON |\n\n## Other\n\n| `--nope` | not a table of flags |\n")}
	if errs := CheckSkill(tree, "hopsesh", good); len(errs) > 0 {
		t.Errorf("a correct skill: %q", errs)
	}
	for name, c := range map[string]struct{ text, want string }{
		"command":     {"`hopsesh lst --json`", "names no hopsesh command"},
		"subcommand":  {"`hopsesh clouds tidy`", "has no subcommand tidy"},
		"bare":        {"`clouds tidy`", "has no subcommand tidy"},
		"flag":        {"`hopsesh ls --live`", "does not take --live"},
		"local flag":  {"`hopsesh clouds --list`", "does not take --list"},
		"table":       {"## ls flags\n\n| `--yes` | x |\n", "does not take"},
		"table owner": {"## lost flags\n\n| `--yes` | x |\n", "not a hopsesh command"},
	} {
		errs := CheckSkill(tree, "hopsesh", map[string][]byte{"x.md": []byte(c.text)})
		if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, c.want) }) {
			t.Errorf("%s: %q, want %q", name, errs, c.want)
		}
	}
}

func TestCheckDocumented(t *testing.T) {
	tree := helpTree(t)
	files := map[string][]byte{"SKILL.md": []byte("`hopsesh ls --json --host M`, `--list` (`--yes`), --tag x, --mode, --password-stdin")}
	if errs := CheckDocumented(tree, files, nil); len(errs) > 0 {
		t.Errorf("all documented: %q", errs)
	}
	files = map[string][]byte{"SKILL.md": []byte("`hopsesh ls --json`, --list --yes --tag --mode")}
	allowed, perrs := ParseAllowed("# comment\n\nhopsesh ls --host  # scripts\nhopsesh ls --json  # stale: documented\nhopsesh ls --gone  # stale: no flag\n")
	if len(perrs) > 0 {
		t.Fatal(perrs)
	}
	errs := CheckDocumented(tree, files, allowed)
	for _, want := range []string{"hopsesh --password-stdin is not in the skill", "hopsesh ls --json is in the skill now", "hopsesh ls --gone is no visible flag"} {
		if !slices.ContainsFunc(errs, func(e string) bool { return strings.Contains(e, want) }) {
			t.Errorf("%q, want %q", errs, want)
		}
	}
	if len(errs) != 3 {
		t.Errorf("%d errors: %q", len(errs), errs)
	}
	if got := Undocumented(tree, files); !slices.Equal(got, []string{"hopsesh --password-stdin", "hopsesh ls --host"}) {
		t.Errorf("undocumented %v", got)
	}
	if _, errs := ParseAllowed("hopsesh ls --host\nhopsesh ls --json # a\nhopsesh ls --json # b\n"); len(errs) != 2 {
		t.Errorf("an entry without a reason and a duplicate: %q", errs)
	}
}
