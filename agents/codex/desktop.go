package codex

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Official local-chat route: https://learn.chatgpt.com/docs/reference/commands#deep-links
// No supported query parameter selects CODEX_HOME, login, fork, or a first prompt.
var desktopBundleID = regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>com\.openai\.codex</string>`)

var desktopVersion = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`)

// desktopApps are where the Codex app installs on macOS. ChatGPT.app is the current
// name (com.openai.codex); Codex.app the earlier one.
var desktopApps = []string{"/Applications/ChatGPT.app", "~/Applications/ChatGPT.app", "/Applications/Codex.app", "~/Applications/Codex.app"}

// bundledRuntime is the codex the app ships and runs headless (resume, fork, exec, login,
// app-server) when no CLI is installed: <bundle>/<bundledRuntime>/codex-package.json
// names its entrypoint ("bin/codex") and version. It shares ~/.codex with the CLI.
const bundledRuntime = "Contents/Resources/codex-cli"

// findDesktopApp returns the verified Codex app bundle on a Mac and its public version.
func findDesktopApp(h agent.Host) (bundle, version string) {
	if h.Facts().OS != "darwin" {
		return "", ""
	}
	for _, p := range desktopApps {
		p = agent.Expand(p, h.Facts().Home, nil, h.Path())
		if info, ok := desktopInfo(h, p); ok {
			if m := desktopVersion.FindSubmatch(info); len(m) == 2 {
				version = strings.TrimSpace(string(m[1]))
			}
			return p, version
		}
	}
	return "", ""
}

// desktopInfo reads a bundle's Info.plist when it is the Codex app. The ChatGPT bundle
// name is also used by unrelated ChatGPT desktop builds: read only public bundle
// metadata, never select by that name alone.
func desktopInfo(h agent.Host, bundle string) ([]byte, bool) {
	info, err := h.FS().ReadFile(h.Path().Join(bundle, "Contents", "Info.plist"), 1<<20)
	return info, err == nil && desktopBundleID.Match(info)
}

// appRuntime accepts a codex found inside an app bundle only as the verified Codex app's
// bundled runtime at the entrypoint its package names, inside the bundle; anything else
// there is dropped. The package's version is the runtime's ("0.162.0-alpha.17.2").
func appRuntime(h agent.Host, in *agent.Install) {
	i := strings.Index(in.Binary, ".app/")
	if i < 0 {
		return // a command-line install
	}
	bundle, bin := in.Binary[:i+len(".app")], in.Binary
	probed := in.Version
	in.Binary, in.Version, in.Runtime = "", "", ""
	if _, ok := desktopInfo(h, bundle); !ok {
		return
	}
	dir := h.Path().Join(bundle, bundledRuntime)
	b, err := h.FS().ReadFile(h.Path().Join(dir, "codex-package.json"), 64<<10)
	var pkg struct {
		Version    string `json:"version"`
		Entrypoint string `json:"entrypoint"`
	}
	if err != nil || json.Unmarshal(b, &pkg) != nil {
		return
	}
	entry := path.Clean(pkg.Entrypoint)
	if pkg.Entrypoint == "" || path.IsAbs(entry) || entry == ".." || strings.HasPrefix(entry, "../") || strings.ContainsAny(entry, "\\:") {
		return
	}
	if h.Path().Join(append([]string{dir}, strings.Split(entry, "/")...)...) != bin {
		return
	}
	in.Binary, in.Runtime, in.Version = bin, agent.RuntimeApp, probed
	if _, ok := agent.CompareVersions(pkg.Version, pkg.Version); ok {
		in.Version = pkg.Version
	}
}

var desktopThreadID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func detectDesktop(h agent.Host, in *agent.Install) {
	in.Desktop, in.DesktopVersion = "", ""
	in.OS = h.Facts().OS
	in.DesktopWhy = "Codex desktop app was not found on this machine"
	bundle, version := findDesktopApp(h)
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
	if bundle != "" {
		in.Desktop, in.DesktopVersion, in.DesktopWhy = bundle, version, ""
		var pkg struct {
			Version string `json:"version"`
		}
		if b, err := h.FS().ReadFile(h.Path().Join(bundle, bundledRuntime, "codex-package.json"), 64<<10); err == nil && json.Unmarshal(b, &pkg) == nil {
			in.DesktopEngine = pkg.Version
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
