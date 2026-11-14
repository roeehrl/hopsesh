package agent

import "context"

// HostResumer is Resume for an agent whose resume command depends on the machine as it is
// now: it checks the session is there (OpenCode makes an empty session for an id it does
// not have) and picks the program to run (OpenCode's must be of its running background
// service's version, or it restarts that service). The core uses it, when a module has
// it, to resume a session already on this machine; a plan's command, made before the
// session is there, still comes from Resume.
type HostResumer interface {
	ResumeOn(ctx context.Context, h Host, in Install, key SessionKey, p Placement, o ResumeOptions) (Command, error)
}

// HostStarter picks the program a new session runs, for an agent whose program depends
// on the machine as it is now (OpenCode's TUI restarts its running background service
// when it is of another version). Without restart (the person agreed) a start that would
// restart the service is ErrRestartsService. The core uses it, when a module has it, for
// a new session on this machine; otherwise the program Detect found runs.
type HostStarter interface {
	StartProgram(ctx context.Context, h Host, in Install, restart bool) (string, error)
}
