package claude

import (
	"context"
	"fmt"
	"regexp"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

var desktopBundleID = regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>com\.anthropic\.claudefordesktop</string>`)
var desktopVersion = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`)

var desktopSessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// https://code.claude.com/docs/en/desktop#move-a-cli-session-to-desktop
// Let the supported CLI launcher check Desktop installation, subscription login and
// live ownership. A chat deep link is not a local Code session, and undocumented
// Desktop storage/routes must not become an alternative session identity.
func detectDesktop(h agent.Host, in *agent.Install) {
	f := h.Facts()
	in.OS = f.OS
	in.Desktop = ""
	in.DesktopWhy = "Claude desktop opening requires macOS or Windows x64"
	if f.OS != "darwin" && !(f.OS == "windows" && f.Arch == "amd64") {
		return
	}
	in.DesktopWhy = "Update Claude Code to 2.1.285 or newer to open a session in Claude desktop"
	if in.Binary == "" || !agent.HookVersionAtLeast(in.Version, "2.1.285") {
		return
	}
	root, err := h.FS().RealPath(in.Root(home))
	if err != nil {
		root = h.Path().Clean(in.Root(home))
	}
	native := h.Path().Join(f.Home, ".claude")
	expected, err := h.FS().RealPath(native)
	if err != nil {
		expected = h.Path().Clean(native)
	}
	in.DesktopWhy = "Claude desktop cannot select this account root; use a terminal for this profile"
	if root != expected {
		return
	}
	in.Desktop = "Claude Code desktop launcher"
	in.DesktopWhy = ""
	if f.OS == "darwin" {
		for _, path := range []string{"/Applications/Claude.app", h.Path().Join(f.Home, "Applications", "Claude.app")} {
			info, err := h.FS().ReadFile(h.Path().Join(path, "Contents", "Info.plist"), 1<<20)
			if err != nil || !desktopBundleID.Match(info) {
				continue
			}
			in.Desktop = path
			if match := desktopVersion.FindSubmatch(info); len(match) == 2 {
				in.DesktopVersion = string(match[1])
			}
			break
		}
	}
}

func (*Module) CheckApp(in agent.Install, key agent.SessionKey, o agent.ResumeOptions) error {
	if in.Desktop == "" {
		why := in.DesktopWhy
		if why == "" {
			why = "Scan this machine to check Claude desktop support"
		}
		return fmt.Errorf("%s", why)
	}
	// The CLI refuses even a session already running in Desktop. Its own resume
	// URL focuses the owner in verified modern Desktop builds. Gate that exception
	// on live Desktop ownership and public bundle version, never historical IDs.
	// Older Desktop versions can duplicate a native chat (upstream issue #80773).
	if o.AppRunning && (in.OS != "darwin" || !agent.HookVersionAtLeast(in.DesktopVersion, "2.19675.1")) {
		return fmt.Errorf("this session is already in Claude desktop; exact focus requires a verified Claude desktop build (macOS 2.19675.1 or newer)")
	}
	if in.Profile != nil && !in.Profile.Default {
		return fmt.Errorf("claude desktop cannot select this account profile; use a terminal")
	}
	if !desktopSessionID.MatchString(string(key.Session)) {
		return fmt.Errorf("claude desktop requires a session UUID, not a name or path")
	}
	if o.Fork || o.RemoteControl || o.Prompt != "" {
		return fmt.Errorf("claude desktop opening cannot fork, start remote control or send a first message; use a terminal")
	}
	return nil
}

func (m *Module) VerifyAppFocus(ctx context.Context, h agent.Host, in agent.Install, key agent.SessionKey) error {
	current, err := m.Live(ctx, h, in, []agent.SessionID{key.Session})
	if err != nil {
		return fmt.Errorf("checking desktop session ownership: %w", err)
	}
	live := current[key.Session]
	if live.State != agent.Live || !live.App {
		return fmt.Errorf("the desktop session changed or closed; refresh sessions before opening it")
	}
	return nil
}
