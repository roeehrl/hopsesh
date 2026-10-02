// Package launch renders how a user continues a moved session: the first message that
// tells the agent what happened, and the command line for the user's shell.
package launch

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Context is what the moved session should know about its move.
type Context struct {
	AgentName      string // the agent that resumes ("Claude Code")
	SourceLocation string
	SourceOS       string
	SourceVersion  string // the agent version that last wrote the session
	SourceCWD      string
	TargetLocation string
	TargetOS       string
	TargetCWD      string
	Branch         string
	WorktreeNote   string
	Unpushed       int
	Dirty          int
	SecretsFound   int
	Redacted       bool
	OtherAccount   bool   // account-bound content was removed
	Live           bool   // the source session was still running
	Fork           bool   // the source keeps running
	Notify         string // the module's instruction for telling the old session, if any
}

// StartPrompt is the first message of a moved session: what happened, what that implies,
// and a request to check the environment before continuing.
func StartPrompt(c Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[hopsesh] This conversation was moved here from another machine. ")
	fmt.Fprintf(&b, "It ran on %s (%s", c.SourceLocation, nonEmpty(c.SourceOS, "unknown OS"))
	if c.SourceVersion != "" {
		fmt.Fprintf(&b, ", %s %s", c.AgentName, c.SourceVersion)
	}
	fmt.Fprintf(&b, ") in %s, and now continues on %s (%s) in %s.\n\n", c.SourceCWD, c.TargetLocation, nonEmpty(c.TargetOS, OSName(runtime.GOOS)), c.TargetCWD)

	b.WriteString("What that means:\n")
	fmt.Fprintf(&b, "- Absolute paths in this conversation's history (the repository, home folder and %s's data folder) were rewritten to this machine's paths. Paths outside those may not exist here.\n", c.AgentName)
	b.WriteString("- Background shells, monitors, scheduled tasks and running processes from the old machine did not come along, and nothing outside the repository was copied.\n")
	if c.Branch != "" {
		fmt.Fprintf(&b, "- The session was on branch %s.", c.Branch)
		if c.WorktreeNote != "" {
			fmt.Fprintf(&b, " %s", c.WorktreeNote)
		}
		b.WriteString("\n")
	} else if c.WorktreeNote != "" {
		fmt.Fprintf(&b, "- %s\n", c.WorktreeNote)
	}
	if c.Unpushed > 0 || c.Dirty > 0 {
		fmt.Fprintf(&b, "- Left behind on %s: %d unpushed commit(s) and %d uncommitted file(s). They are not in this checkout unless they were brought over separately.\n", c.SourceLocation, c.Unpushed, c.Dirty)
	}
	if c.SecretsFound > 0 {
		if c.Redacted {
			fmt.Fprintf(&b, "- %d likely secret(s) in earlier tool output were redacted in this copy; re-obtain them from their source if needed.\n", c.SecretsFound)
		} else {
			fmt.Fprintf(&b, "- %d likely secret(s) appear in earlier tool output; do not repeat them.\n", c.SecretsFound)
		}
	}
	if c.OtherAccount {
		b.WriteString("- Earlier reasoning was removed because this machine uses a different account; the visible conversation is intact.\n")
	}
	if c.Live && c.Fork {
		fmt.Fprintf(&b, "- The original session on %s is still running and may keep changing its own copy of the repository.\n", c.SourceLocation)
	}

	b.WriteString("\nBefore continuing the previous task, please check that everything is in place:\n")
	b.WriteString("1. Run git status and git log -1 here: confirm the branch, the last commit and the working tree match what the conversation expects.\n")
	b.WriteString("2. Check that the files and directories the recent conversation relied on exist at their (rewritten) paths.\n")
	b.WriteString("3. Check that the tools, runtimes, dependencies, environment variables and credentials the task needs are available on this machine (don't print secret values).\n")
	if c.Unpushed > 0 || c.Dirty > 0 {
		fmt.Fprintf(&b, "4. Work out whether any of the work left behind on %s is needed to continue.\n", c.SourceLocation)
	}
	if c.Notify != "" {
		b.WriteString("\n" + c.Notify + "\n")
	}
	b.WriteString("\nThen give me a short report of anything missing or different, and wait for my go-ahead before resuming the task.")
	return b.String()
}

// OldSessionNotice is the text for the user to paste into the old session when the agent
// cannot deliver it.
func OldSessionNotice(targetLocation, targetCWD, newName string, fork bool) string {
	s := fmt.Sprintf("[hopsesh] This conversation was copied to %s (%s)", targetLocation, targetCWD)
	if newName != "" {
		s += fmt.Sprintf(" and continues there as %q", newName)
	}
	if fork {
		return s + ". Both copies are active; coordinate before editing shared files."
	}
	return s + ". Please stop working on this task here and don't edit these files any more."
}

// Shell renders a command for a shell family ("posix" or "powershell"). When promptFile
// is set, the command's last argument (the prompt) is read from that file instead of
// being inlined.
func Shell(c agent.Command, promptFile, family string) string {
	argv := c.Argv
	if promptFile != "" && len(argv) > 0 {
		argv = argv[:len(argv)-1]
	}
	if family == "powershell" {
		parts := make([]string, len(argv))
		for i, a := range argv {
			parts[i] = PSQuote(a)
		}
		if promptFile != "" {
			parts = append(parts, "(Get-Content -Raw "+PSQuote(promptFile)+")")
		}
		return "Set-Location " + PSQuote(c.Dir) + "; & " + strings.Join(parts, " ")
	}
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = ShQuote(a)
	}
	if promptFile != "" {
		parts = append(parts, `"$(cat `+ShQuote(promptFile)+`)"`)
	}
	return "cd " + ShQuote(c.Dir) + " && " + strings.Join(parts, " ")
}

// DefaultShell is this machine's shell family.
func DefaultShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "posix"
}

// ShQuote quotes for POSIX shells, leaving plain words as they are.
func ShQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@,+", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// PSQuote quotes for PowerShell.
func PSQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// OSName is a GOOS for people.
func OSName(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	}
	return goos
}

// SessionName is a short name for Remote Control and session lists: "fix-tests@laptop".
func SessionName(title, location string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	n := strings.Trim(b.String(), "-")
	if len(n) > 40 {
		n = strings.Trim(n[:40], "-")
	}
	if n == "" {
		n = "session"
	}
	return n + "@" + strings.ToLower(strings.Split(location, ".")[0])
}

func nonEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
