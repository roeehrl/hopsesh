// Command skillcheck holds a built hopsesh program to its own skill, as the release ships
// it: it reads the visible command tree from the program's help (every command's
// `--help`), installs the skill the program writes into a throwaway home, and fails when
// the skill names a command or flag the program lacks, when a visible flag is neither in
// the skill nor in the allowlist (or an allowlist entry is stale), or when the tree differs
// from the checked-in golden. CI and the release workflow run it; the same checks run in
// internal/ui/cli's tests from the cobra commands. Nothing outside the temporary home is
// read or written, and nothing connects anywhere.
//
//	go run ./internal/devtools/skillcheck -bin dist/hopsesh \
//	  -golden internal/ui/cli/testdata/cli-tree.golden -allow internal/ui/cli/testdata/skill-undocumented-flags.txt
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/devtools/clitree"
)

func main() {
	bin := flag.String("bin", "", "the built hopsesh program")
	golden := flag.String("golden", "", "the checked-in command tree (empty: not compared)")
	allow := flag.String("allow", "", "the allowlist of flags the skill need not mention (empty: not checked)")
	printTree := flag.Bool("print", false, "print the tree read from the program and stop")
	flag.Parse()
	if *bin == "" {
		fmt.Fprintln(os.Stderr, "skillcheck: -bin is required")
		os.Exit(2)
	}
	errs, err := check(*bin, *golden, *allow, *printTree)
	if err != nil {
		fmt.Fprintln(os.Stderr, "skillcheck:", err)
		os.Exit(1)
	}
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "skillcheck:", e)
	}
	if len(errs) > 0 {
		os.Exit(1)
	}
}

func check(bin, golden, allow string, printTree bool) ([]string, error) {
	bin, err := filepath.Abs(bin)
	if err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp("", "skillcheck-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	env := isolated(home)
	tree, err := clitree.FromHelp("hopsesh", func(words []string) (string, error) {
		return run(bin, env, append(words, "--help")...)
	})
	if err != nil {
		return nil, err
	}
	if printTree {
		fmt.Print(tree.Golden())
		return nil, nil
	}
	files, err := installedSkill(bin, env, home)
	if err != nil {
		return nil, err
	}
	errs := clitree.CheckSkill(tree, "hopsesh", files)
	if allow != "" {
		b, err := os.ReadFile(allow)
		if err != nil {
			return nil, err
		}
		allowed, perrs := clitree.ParseAllowed(string(b))
		errs = append(append(errs, perrs...), clitree.CheckDocumented(tree, files, allowed)...)
	}
	if golden != "" {
		b, err := os.ReadFile(golden)
		if err != nil {
			return nil, err
		}
		if want := strings.ReplaceAll(string(b), "\r\n", "\n"); tree.Golden() != want {
			errs = append(errs, fmt.Sprintf("the program's command tree differs from %s (go run ./internal/devtools/skillcheck -bin %s -print shows it; scripts/update-cli-golden.sh rewrites the golden):\n%s", golden, bin, clitree.Diff(want, tree.Golden())))
		}
	}
	if len(errs) == 0 {
		fmt.Printf("skillcheck: %s: %d commands; the skill names only real commands and flags%s%s\n", bin, len(tree),
			map[bool]string{true: "; every visible flag is documented or allowed", false: ""}[allow != ""],
			map[bool]string{true: "; the tree matches the golden", false: ""}[golden != ""])
	}
	return errs, nil
}

// isolated is an environment of a throwaway home: hopsesh's and both agents' folders in
// it, no Tailscale, no desktop app's runtime, no agent on the PATH.
func isolated(home string) []string {
	env := []string{
		"HOME=" + home, "USERPROFILE=" + home,
		"HOPSESH_CONFIG_DIR=" + filepath.Join(home, "hopsesh-config"), "HOPSESH_STATE_DIR=" + filepath.Join(home, "hopsesh-state"),
		"HOPSESH_MACHINE=skillcheck", "HOPSESH_TAILSCALE=off", "HOPSESH_APP_RUNTIMES=off",
		"CLAUDE_CONFIG_DIR=" + filepath.Join(home, "claude"), "CODEX_HOME=" + filepath.Join(home, "codex"),
		"TERM=dumb", "NO_COLOR=1", "LANG=C.UTF-8",
	}
	if runtime.GOOS == "windows" {
		env = append(env, "PATH="+os.Getenv("SystemRoot")+`\System32`, "SystemRoot="+os.Getenv("SystemRoot"),
			"APPDATA="+filepath.Join(home, "AppData", "Roaming"), "LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"))
	} else {
		env = append(env, "PATH=/usr/bin:/bin:/usr/sbin:/sbin")
	}
	return env
}

func run(bin string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Stdin = nil
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// runAs finds how the skill names the program: "Run it as `hopsesh`" (or the absolute
// path it found, which the checks read as hopsesh).
var runAs = regexp.MustCompile("Run it as\\s+`([^`]+)`")

// installedSkill has the program install its skill into the throwaway home and reads it
// back from Claude Code's skills folder there.
func installedSkill(bin string, env []string, home string) (map[string][]byte, error) {
	for _, d := range []string{"claude", "codex"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			return nil, err
		}
	}
	if out, err := run(bin, env, "skill", "install"); err != nil {
		return nil, fmt.Errorf("skill install: %w (%s)", err, out)
	}
	dir := filepath.Join(home, "claude", "skills", "hopsesh")
	files := map[string][]byte{}
	for _, name := range []string{"SKILL.md", "reference.md"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("the program installed no %s: %w", name, err)
		}
		files[name] = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	}
	m := runAs.FindSubmatch(files["SKILL.md"])
	if m == nil {
		return nil, errors.New("SKILL.md does not say how to run hopsesh (\"Run it as `…`\")")
	}
	if as := m[1]; string(as) != "hopsesh" {
		for name, b := range files {
			files[name] = bytes.ReplaceAll(b, as, []byte("hopsesh"))
		}
	}
	return files, nil
}
