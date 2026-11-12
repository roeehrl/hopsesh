// Package clitree is hopsesh's visible command tree as `--help` shows it, and the checks
// that hold the skill to it: every command and flag the skill names exists, and every
// visible flag is in the skill or allowed to be missing from it. The CLI's tests build
// the tree from the cobra commands; internal/devtools/skillcheck builds it from a built
// program's help, so a release is checked as shipped. Both print the same golden.
package clitree

import (
	"bufio"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Flag is one flag as help prints it: "-y, --yes" or "--to string".
type Flag struct {
	Name  string // "to"
	Short string // "y", or ""
	Arg   string // the value's name ("string", "dir"), "" for a switch
}

func (f Flag) String() string {
	s := "--" + f.Name
	if f.Short != "" {
		s = "-" + f.Short + ", " + s
	}
	if f.Arg != "" {
		s += " " + f.Arg
	}
	return s
}

// Command is one visible command.
type Command struct {
	Path     string   // "hopsesh clouds cleanup"
	Children []string // visible subcommands' names
	Flags    []Flag   // its own (help's "Flags:"), help excluded
	Global   []Flag   // inherited (help's "Global Flags:")
}

// Has reports whether the command takes --name, its own or inherited.
func (c Command) Has(name string) bool {
	match := func(f Flag) bool { return f.Name == name }
	return name == "help" || slices.ContainsFunc(c.Flags, match) || slices.ContainsFunc(c.Global, match)
}

// Tree is every visible command by path.
type Tree map[string]Command

// Skip are commands left out of the tree everywhere: cobra's own.
var Skip = map[string]bool{"help": true, "completion": true}

// Golden is the tree as the checked-in golden file has it: each command, then its own
// flags, sorted.
func (t Tree) Golden() string {
	paths := make([]string, 0, len(t))
	for p := range t {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(p + "\n")
		fs := slices.Clone(t[p].Flags)
		sort.Slice(fs, func(i, j int) bool { return fs[i].Name < fs[j].Name })
		for _, f := range fs {
			b.WriteString("  " + f.String() + "\n")
		}
	}
	return b.String()
}

// Diff lists the lines of a golden only one side has: "- " the want's, "+ " the got's.
// Flags repeat under several commands, so it points at a change rather than placing it.
func Diff(want, got string) string {
	set := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(s, "\n") {
			m[l] = true
		}
		return m
	}
	w, g := set(want), set(got)
	var b strings.Builder
	for _, l := range strings.Split(want, "\n") {
		if !g[l] {
			b.WriteString("- " + l + "\n")
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if !w[l] {
			b.WriteString("+ " + l + "\n")
		}
	}
	return b.String()
}

// Resolve follows words from the root as far as they name subcommands: the command they
// reach and the words left.
func (t Tree) Resolve(root string, words []string) (Command, []string) {
	c := t[root]
	for len(words) > 0 && slices.Contains(c.Children, words[0]) {
		c = t[c.Path+" "+words[0]]
		words = words[1:]
	}
	return c, words
}

// ParseFlags reads pflag's FlagUsages lines ("  -y, --yes   usage"); help is left out.
func ParseFlags(usages string) []Flag {
	var out []Flag
	for _, line := range strings.Split(usages, "\n") {
		head := strings.TrimSpace(line)
		if !strings.HasPrefix(head, "-") {
			continue // a continuation of the usage above
		}
		if i := strings.Index(head, "  "); i >= 0 {
			head = head[:i]
		}
		var f Flag
		if s, rest, ok := strings.Cut(head, ", "); ok && len(s) == 2 && s[0] == '-' && s[1] != '-' {
			f.Short, head = s[1:], rest
		}
		name, arg, _ := strings.Cut(head, " ")
		if !strings.HasPrefix(name, "--") {
			continue
		}
		f.Name, f.Arg = strings.TrimPrefix(name, "--"), arg
		if i := strings.Index(f.Name, "[="); i >= 0 { // a bool with another NoOptDefVal
			f.Name, f.Arg = f.Name[:i], f.Name[i:]
		}
		if f.Name != "help" {
			out = append(out, f)
		}
	}
	return out
}

// Help is what one `--help` says: the visible subcommands and the flags.
type Help struct {
	Children []string
	Flags    []Flag
	Global   []Flag
}

// ParseHelp reads cobra's default usage text.
func ParseHelp(text string) Help {
	var h Help
	section := ""
	var flags, global strings.Builder
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line != "" && !strings.HasPrefix(line, " ") && strings.HasSuffix(line, ":") {
			section = line
			continue
		}
		if line == "" {
			section = ""
			continue
		}
		switch {
		case section == "Flags:":
			flags.WriteString(line + "\n")
		case section == "Global Flags:":
			global.WriteString(line + "\n")
		case strings.HasSuffix(section, "Commands:"):
			if f := strings.Fields(line); len(f) > 0 && strings.HasPrefix(line, "  ") && !Skip[f[0]] {
				h.Children = append(h.Children, f[0])
			}
		}
	}
	h.Flags, h.Global = ParseFlags(flags.String()), ParseFlags(global.String())
	return h
}

// FromHelp builds the tree by asking for help: help(path words) returns the text of
// `<program> <words...> --help`.
func FromHelp(root string, help func(words []string) (string, error)) (Tree, error) {
	t := Tree{}
	var walk func(words []string) error
	walk = func(words []string) error {
		text, err := help(words)
		if err != nil {
			return fmt.Errorf("%s --help: %w", strings.Join(append([]string{root}, words...), " "), err)
		}
		h := ParseHelp(text)
		path := strings.Join(append([]string{root}, words...), " ")
		t[path] = Command{Path: path, Children: h.Children, Flags: h.Flags, Global: h.Global}
		for _, c := range h.Children {
			if err := walk(append(slices.Clone(words), c)); err != nil {
				return err
			}
		}
		return nil
	}
	return t, walk(nil)
}

var (
	span      = regexp.MustCompile("`([^`]+)`")
	longFlag  = regexp.MustCompile(`--([a-z][a-z-]*)`)
	anyFlag   = regexp.MustCompile("(?:^|[\\s`(|/\\[])--([a-z][a-z-]*)")
	heading   = regexp.MustCompile(`(?m)^## (.+) flags$`)
	plainWord = regexp.MustCompile(`^[a-z][a-z-]*$`)
)

// Documented is every --flag the skill files name anywhere.
func Documented(files map[string][]byte) map[string]bool {
	out := map[string]bool{}
	for _, b := range files {
		for _, m := range anyFlag.FindAllSubmatch(b, -1) {
			out[string(m[1])] = true
		}
	}
	return out
}

// CheckSkill returns what in the skill files (rendered with the program named root) does
// not exist in the tree: a command an invocation names (`hopsesh clouds cleanup --list`,
// or a bare `hosts allow`), a flag on the command it is given to, or a flag in a
// "## <commands> flags" table that one of those commands lacks.
func CheckSkill(t Tree, root string, files map[string][]byte) []string {
	var errs []string
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		text := string(files[name])
		for _, m := range span.FindAllStringSubmatch(text, -1) {
			words := strings.Fields(m[1])
			// An invocation, wherever the program's name starts one in the span.
			for i, w := range words {
				if w == root {
					errs = append(errs, checkCall(t, root, name, words[i+1:])...)
				}
			}
			// A command named without the program: `hosts allow`, `terminals --use`.
			if len(words) > 1 && words[0] != root && slices.Contains(t[root].Children, words[0]) {
				errs = append(errs, checkCall(t, root, name, words)...)
			}
		}
		for _, loc := range heading.FindAllStringSubmatchIndex(text, -1) {
			body := text[loc[1]:]
			if next := strings.Index(body, "\n## "); next >= 0 {
				body = body[:next]
			}
			for _, cmd := range strings.Split(text[loc[2]:loc[3]], " and ") {
				c, rest := t.Resolve(root, strings.Fields(cmd))
				if len(rest) > 0 || c.Path == "" {
					errs = append(errs, fmt.Sprintf("%s: the %q table names %s, which is not a %s command", name, text[loc[2]:loc[3]]+" flags", cmd, root))
					continue
				}
				for _, row := range strings.Split(body, "\n") {
					cells := strings.Split(row, "|")
					if len(cells) < 3 {
						continue
					}
					for _, f := range longFlag.FindAllStringSubmatch(cells[1], -1) {
						if !c.Has(f[1]) {
							errs = append(errs, fmt.Sprintf("%s: the %s flags table names --%s, which %s does not take", name, cmd, f[1], c.Path))
						}
					}
				}
			}
		}
	}
	return errs
}

func checkCall(t Tree, root, file string, words []string) []string {
	if len(words) == 0 || !plainWord.MatchString(words[0]) {
		return nil
	}
	c, rest := t.Resolve(root, words)
	if c.Path == root {
		return []string{fmt.Sprintf("%s: `%s %s` names no %s command", file, root, strings.Join(words, " "), root)}
	}
	var errs []string
	// The next plain word after a command with subcommands must be one of them.
	if len(c.Children) > 0 && len(rest) > 0 && plainWord.MatchString(rest[0]) {
		errs = append(errs, fmt.Sprintf("%s: `%s %s`: %s has no subcommand %s", file, root, strings.Join(words, " "), c.Path, rest[0]))
	}
	for _, f := range longFlag.FindAllStringSubmatch(strings.Join(rest, " "), -1) {
		if !c.Has(f[1]) {
			errs = append(errs, fmt.Sprintf("%s: `%s %s`: %s does not take --%s", file, root, strings.Join(words, " "), c.Path, f[1]))
		}
	}
	return errs
}

// Allowed is the allowlist of visible flags the skill need not mention: lines of
// "<command path> --flag  # why", blank lines and #-comments ignored.
type Allowed map[string]string // "hopsesh hosts setup --json" → why

// ParseAllowed reads an allowlist, refusing entries without a reason and duplicates.
func ParseAllowed(text string) (Allowed, []string) {
	out := Allowed{}
	var errs []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry, why, _ := strings.Cut(line, "#")
		entry, why = strings.Join(strings.Fields(entry), " "), strings.TrimSpace(why)
		if why == "" || !strings.Contains(entry, " --") {
			errs = append(errs, fmt.Sprintf("allowlist line %d: want \"<command> --flag  # why\": %s", i+1, line))
			continue
		}
		if _, dup := out[entry]; dup {
			errs = append(errs, fmt.Sprintf("allowlist line %d: %s is listed twice", i+1, entry))
		}
		out[entry] = why
	}
	return out, errs
}

// CheckDocumented returns every visible flag of every command that the skill does not
// mention and the allowlist does not excuse, and every allowlist entry that is stale: its
// flag is gone, or the skill mentions it now.
func CheckDocumented(t Tree, files map[string][]byte, allowed Allowed) []string {
	doc := Documented(files)
	var errs []string
	seen := map[string]bool{}
	for _, c := range t {
		for _, f := range c.Flags {
			key := c.Path + " --" + f.Name
			seen[key] = true
			_, ok := allowed[key]
			switch {
			case doc[f.Name] && ok:
				errs = append(errs, fmt.Sprintf("allowlist: %s is in the skill now; remove its entry", key))
			case !doc[f.Name] && !ok:
				errs = append(errs, fmt.Sprintf("%s is not in the skill: document it, or add \"%s  # why\" to the allowlist", key, key))
			}
		}
	}
	for key := range allowed {
		if !seen[key] {
			errs = append(errs, fmt.Sprintf("allowlist: %s is no visible flag (gone or hidden); remove its entry", key))
		}
	}
	sort.Strings(errs)
	return errs
}

// Undocumented lists the entries an allowlist needs now, for a first one.
func Undocumented(t Tree, files map[string][]byte) []string {
	doc := Documented(files)
	var out []string
	for _, c := range t {
		for _, f := range c.Flags {
			if !doc[f.Name] {
				out = append(out, c.Path+" --"+f.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}
