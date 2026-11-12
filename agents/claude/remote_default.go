package claude

import "github.com/roeehrl/hopsesh/sdk/agent"

var _ agent.RemoteControlDefaulter = (*Module)(nil)

// remoteDefaultFloor: from 2.1.222 remoteControlAtStartup turns Remote Control on only
// from user or managed settings (a project can turn it off, not on), which is where
// hopsesh sets it. Contract checked 2026-10-29:
// https://code.claude.com/docs/en/settings-reference#remotecontrolatstartup
const remoteDefaultFloor = "2.1.222"

// RemoteControlDefault is remoteControlAtStartup in the profile's user settings.json:
// every interactive session it starts connects to Remote Control.
func (m *Module) RemoteControlDefault(h agent.Host, in agent.Install) *agent.SettingFile {
	f := &agent.SettingFile{Path: h.Path().Join(in.Root(home), "settings.json"), Key: "remoteControlAtStartup",
		Evidence: "https://code.claude.com/docs/en/settings-reference#remotecontrolatstartup"}
	if !agent.HookVersionAtLeast(in.Version, remoteDefaultFloor) {
		f.Reason = agent.NeedsVersion("Claude Code", in.Version, remoteDefaultFloor)
	}
	return f
}
