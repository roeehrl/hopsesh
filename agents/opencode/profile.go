package opencode

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Account folders (decision D8). OpenCode's background service listens on one port,
// 49374 (28894 on Windows), whatever folders it runs with, so a second folder's service
// fails while the default one runs. Each account folder hopsesh makes gets a port of its
// own, set with `opencode service set port <n>` under the folder's variables: OpenCode
// keeps it in the folder's {config}/service.json. Setting a port stops the service
// registered in the folder's state folder, so it is set only in a folder hopsesh has just
// made, where no service is registered, and never in the default one.

var _ agent.AccountPreparer = (*Module)(nil)

// defaultPort is the port OpenCode's background service takes when nothing sets another
// (service-config.ts defaultPort, release channels): 0xC0DE, and 0x70DE on Windows, where
// the higher range is reserved.
func defaultPort(goos string) int {
	if goos == "windows" {
		return 0x70de
	}
	return 0xc0de
}

// portTries bounds the ports PrepareAccount tries after the default one.
const portTries = 200

// PrepareAccount gives a new account folder its own service port: the first one after
// OpenCode's default that no other account folder was given and nothing listens on.
func (m *Module) PrepareAccount(ctx context.Context, h agent.Host, in agent.Install, o agent.AccountPrep) (agent.AccountPrepared, error) {
	if in.Profile == nil || in.Profile.Default {
		return agent.AccountPrepared{}, errors.New("hopsesh sets OpenCode's service port only in an account folder it made")
	}
	pa, root := h.Path(), in.Profile.Root
	state, config := in.Root(rootState), in.Root(rootConfig)
	for _, dir := range []string{state, config, in.Root(rootData)} {
		if _, ok := pa.Rel(root, dir); dir == "" || !ok {
			return agent.AccountPrepared{}, fmt.Errorf("OpenCode's folders are not all inside the account folder %s, so hopsesh does not change its service settings", root)
		}
	}
	// The variables commands run with must name this folder too: a command run with the
	// default folder's would stop the default service and move it to another port.
	env := map[string]string{}
	for _, e := range in.ProfileEnv() {
		k, v, _ := strings.Cut(e, "=")
		env[k] = v
	}
	for _, k := range []string{"XDG_STATE_HOME", "OPENCODE_CONFIG_DIR"} {
		if _, ok := pa.Rel(root, env[k]); env[k] == "" || !ok {
			return agent.AccountPrepared{}, fmt.Errorf("the account folder %s does not set %s, so hopsesh does not change its service settings", root, k)
		}
	}
	if entries, err := h.FS().ReadDir(state); err == nil {
		for _, e := range entries {
			if n := e.Name(); strings.HasPrefix(n, "service") && strings.HasSuffix(n, ".json") {
				return agent.AccountPrepared{}, fmt.Errorf("OpenCode has a background service registered in %s already; hopsesh leaves its settings as they are", state)
			}
		}
	}
	port := pickPort(defaultPort(h.Facts().OS), o)
	if port == 0 {
		return agent.AccountPrepared{}, fmt.Errorf("hopsesh found no free port for OpenCode's background service after %d", defaultPort(h.Facts().OS))
	}
	opts := agent.RunOptions{Env: in.ProfileEnv(), Timeout: 30 * time.Second}
	res, err := run(ctx, h, []string{"service", "set", "port", strconv.Itoa(port)}, opts)
	if err != nil {
		return agent.AccountPrepared{}, err
	}
	if res.Code != 0 {
		return agent.AccountPrepared{}, commandError("service set port", res)
	}
	got, err := servicePort(ctx, h, opts)
	if err != nil {
		return agent.AccountPrepared{}, err
	}
	if got != port {
		return agent.AccountPrepared{}, fmt.Errorf("OpenCode kept the service port %d, not %d", got, port)
	}
	return agent.AccountPrepared{Port: port}, nil
}

// pickPort is the first port after def that o does not take; 0 when none is left.
func pickPort(def int, o agent.AccountPrep) int {
	taken := map[int]bool{def: true}
	for _, p := range o.Taken {
		taken[p] = true
	}
	for p := def + 1; p <= def+portTries && p <= 65535; p++ {
		if taken[p] || o.Free != nil && !o.Free(p) {
			continue
		}
		return p
	}
	return 0
}

// servicePort asks OpenCode the port its service is set to (`service get port`, which
// prints the number or nothing, and starts nothing); 0 when none is set.
func servicePort(ctx context.Context, h agent.Host, o agent.RunOptions) (int, error) {
	res, err := run(ctx, h, []string{"service", "get", "port"}, o)
	if err != nil {
		return 0, err
	}
	if res.Code != 0 {
		return 0, commandError("service get port", res)
	}
	s := strings.TrimSpace(firstLine(res.Stdout))
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("opencode service get port printed %q", s)
	}
	return n, nil
}
