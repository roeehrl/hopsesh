package link

import (
	"fmt"
	"runtime"
	"strings"
)

// Context is what the new session should know about its move.
type Context struct {
	SourceHost    string
	SourceOS      string
	SourceVersion string // Claude Code version that last wrote the transcript
	SourceCWD     string
	TargetHost    string
	TargetOS      string
	TargetCWD     string
	Branch        string
	WorktreeNote  string // e.g. "a new worktree was created at …" / "the main checkout is used"
	Unpushed      int
	Dirty         int
	UnmappedNote  string // paths that were not remapped, if any
	SecretsFound  int
	Redacted      bool
	ThinkingDrop  bool
	Live          bool   // the source session was still running
	Fork          bool   // the source keeps running (fork) instead of handing off
	NotifyOld     bool   // ask this session to message the old one
	OldName       string // the old session's name, for SendMessage
	NewName       string // this session's Remote Control name
}

// StartPrompt is the first message of the moved session. It tells Claude what happened,
// what that implies, and asks it to verify the environment before continuing.
func StartPrompt(c Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[hopsesh] This conversation was moved here from another machine. ")
	fmt.Fprintf(&b, "It ran on %s (%s", c.SourceHost, nonEmpty(c.SourceOS, "unknown OS"))
	if c.SourceVersion != "" {
		fmt.Fprintf(&b, ", Claude Code %s", c.SourceVersion)
	}
	fmt.Fprintf(&b, ") in %s, and now continues on %s (%s) in %s.\n\n", c.SourceCWD, c.TargetHost, nonEmpty(c.TargetOS, runtime.GOOS), c.TargetCWD)

	b.WriteString("What that means:\n")
	b.WriteString("- Absolute paths in this conversation's history (the repository, home folder and Claude config folder) were rewritten to this machine's paths. Paths outside those may not exist here.\n")
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
		fmt.Fprintf(&b, "- Left behind on %s: %d unpushed commit(s) and %d uncommitted file(s). They are not in this checkout unless they were brought over separately.\n", c.SourceHost, c.Unpushed, c.Dirty)
	}
	if c.UnmappedNote != "" {
		fmt.Fprintf(&b, "- %s\n", c.UnmappedNote)
	}
	if c.SecretsFound > 0 {
		if c.Redacted {
			fmt.Fprintf(&b, "- %d likely secret(s) in earlier tool output were redacted in this copy; re-obtain them from their source if needed.\n", c.SecretsFound)
		} else {
			fmt.Fprintf(&b, "- %d likely secret(s) appear in earlier tool output; do not repeat them.\n", c.SecretsFound)
		}
	}
	if c.ThinkingDrop {
		b.WriteString("- Earlier reasoning blocks were removed because this machine uses a different account; the visible conversation is intact.\n")
	}
	if c.Live && c.Fork {
		fmt.Fprintf(&b, "- The original session on %s is still running and may keep changing its own copy of the repository.\n", c.SourceHost)
	}

	b.WriteString("\nBefore continuing the previous task, please check that everything is in place:\n")
	b.WriteString("1. Run git status and git log -1 here: confirm the branch, the last commit and the working tree match what the conversation expects.\n")
	b.WriteString("2. Check that the files and directories the recent conversation relied on exist at their (rewritten) paths.\n")
	b.WriteString("3. Check that the tools, runtimes, dependencies, environment variables and credentials the task needs are available on this machine (don't print secret values).\n")
	if c.Unpushed > 0 || c.Dirty > 0 {
		fmt.Fprintf(&b, "4. Work out whether any of the work left behind on %s is needed to continue.\n", c.SourceHost)
	}
	if c.NotifyOld && c.OldName != "" {
		fmt.Fprintf(&b, "\nAlso use ListAgents and SendMessage to tell the session %q on %s that this work now continues on %s", c.OldName, c.SourceHost, c.TargetHost)
		if c.NewName != "" {
			fmt.Fprintf(&b, " as %q", c.NewName)
		}
		if c.Fork {
			b.WriteString(", so you can coordinate.\n")
		} else {
			b.WriteString(", and ask it to stop working on this task and not edit these files.\n")
		}
	}
	b.WriteString("\nThen give me a short report of anything missing or different, and wait for my go-ahead before resuming the task.")
	return b.String()
}

// OldSessionNotice is the text for the user to paste into the old session when automatic
// notification is not possible (no Remote Control on both sides).
func OldSessionNotice(targetHost, targetCWD, newName string, fork bool) string {
	s := fmt.Sprintf("[hopsesh] This conversation was copied to %s (%s)", targetHost, targetCWD)
	if newName != "" {
		s += fmt.Sprintf(" and continues there as %q", newName)
	}
	if fork {
		return s + ". Both copies are active; coordinate before editing shared files."
	}
	return s + ". Please stop working on this task here and don't edit these files any more."
}

// Resume describes how to start the moved session.
type Resume struct {
	Dir         string
	SessionID   string
	Fork        bool
	RemoteCtl   bool
	Name        string // Remote Control / session name
	StartPrompt string
	// PromptFile, when set, holds StartPrompt; shell commands read it instead of inlining
	// a long quoted prompt.
	PromptFile string
	// Desktop opens the session in the Claude desktop app (claude --desktop), where the
	// installed Claude Code has that flag.
	Desktop bool
}

// Argv returns the claude command line (without changing directory).
func (r Resume) Argv() []string {
	argv := []string{"claude"}
	if r.Desktop {
		argv = append(argv, "--desktop")
	}
	argv = append(argv, "--resume", r.SessionID)
	if r.Fork {
		argv = append(argv, "--fork-session")
	}
	if r.RemoteCtl {
		if r.Name != "" {
			argv = append(argv, "--remote-control", r.Name)
		} else {
			argv = append(argv, "--remote-control")
		}
	}
	if r.StartPrompt != "" {
		argv = append(argv, r.StartPrompt)
	}
	return argv
}

// Shell returns a copy-pasteable command for the given shell family ("posix" or
// "powershell").
func (r Resume) Shell(family string) string {
	argv := r.Argv()
	usesFile := r.PromptFile != "" && r.StartPrompt != ""
	if usesFile {
		argv = argv[:len(argv)-1]
	}
	if family == "powershell" {
		parts := make([]string, len(argv))
		for i, a := range argv {
			parts[i] = psQuote(a)
		}
		if usesFile {
			parts = append(parts, "(Get-Content -Raw "+psQuote(r.PromptFile)+")")
		}
		return "Set-Location " + psQuote(r.Dir) + "; & " + strings.Join(parts, " ")
	}
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shQuote(a)
	}
	if usesFile {
		parts = append(parts, `"$(cat `+shQuote(r.PromptFile)+`)"`)
	}
	return "cd " + shQuote(r.Dir) + " && " + strings.Join(parts, " ")
}

// DefaultShell is the shell family of this machine.
func DefaultShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "posix"
}

func shQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@,+", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func nonEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
