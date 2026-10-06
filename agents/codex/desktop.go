package codex

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Official local-chat route: https://learn.chatgpt.com/docs/reference/commands#deep-links
// No supported query parameter selects CODEX_HOME, login, fork, or a first prompt.
var desktopBundleID = regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>com\.openai\.codex</string>`)

var desktopThreadID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func detectDesktop(h agent.Host, in *agent.Install) {
	in.Desktop = ""
	in.OS = h.Facts().OS
	in.DesktopWhy = "Codex desktop app was not found on this machine"
	native := h.Path().Join(h.Facts().Home, ".codex")
	root, err := h.FS().RealPath(in.Root(home))
	if err != nil {
		root = h.Path().Clean(in.Root(home))
	}
	expected, err := h.FS().RealPath(native)
	if err != nil {
		expected = h.Path().Clean(native)
	}
	if root != expected {
		in.DesktopWhy = "Codex desktop deep links cannot select this account root; use a terminal for this profile"
		return
	}
	if (in.OS == "windows" || in.OS == "linux") && h.Facts().DesktopProtocols["codex"] {
		in.Desktop = "codex protocol handler"
		in.DesktopWhy = ""
		return
	}
	candidates := map[string][]string{
		"darwin": {"/Applications/Codex.app", "~/Applications/Codex.app", "/Applications/ChatGPT.app", "~/Applications/ChatGPT.app"},
	}
	for _, p := range candidates[in.OS] {
		p = agent.Expand(p, h.Facts().Home, nil, h.Path())
		// The ChatGPT bundle name is also used by unrelated ChatGPT desktop builds.
		// Read only public bundle metadata, never select by that name alone.
		info, err := h.FS().ReadFile(h.Path().Join(p, "Contents", "Info.plist"), 1<<20)
		if err == nil && desktopBundleID.Match(info) {
			in.Desktop = p
			in.DesktopWhy = ""
			return
		}
	}
}

func (*Module) CheckApp(in agent.Install, key agent.SessionKey, o agent.ResumeOptions) error {
	if in.OS != "darwin" && in.OS != "windows" && in.OS != "linux" {
		return fmt.Errorf("codex desktop opening is unsupported on %s", in.OS)
	}
	if in.Desktop == "" {
		why := in.DesktopWhy
		if why == "" {
			why = "scan this machine to check the Codex desktop installation"
		}
		return fmt.Errorf("%s", why)
	}
	if in.Profile != nil && !in.Profile.Default {
		return fmt.Errorf("codex desktop deep links cannot select this account profile; use a terminal")
	}
	if !desktopThreadID.MatchString(string(key.Session)) {
		return fmt.Errorf("codex desktop requires a valid thread UUID")
	}
	if o.Fork || o.Prompt != "" {
		return fmt.Errorf("codex desktop opening cannot fork or send a first message; use a terminal for those actions")
	}
	return nil
}
func desktopCommand(in agent.Install, key agent.SessionKey) agent.Command {
	url := "codex://threads/" + strings.ToLower(string(key.Session))
	var argv []string
	switch in.OS {
	case "darwin":
		argv = []string{"/usr/bin/open", "-a", in.Desktop, url}
	case "windows":
		argv = []string{"rundll32.exe", "url.dll,FileProtocolHandler", url}
	case "linux":
		argv = []string{"xdg-open", url}
	}
	return agent.Command{Argv: argv, Wait: true} // no terminal, prompt, shell interpolation or environment switch
}
