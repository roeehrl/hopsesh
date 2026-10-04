package host

import (
	"context"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Unsetting is h with every program it runs started without the variables in unset: the
// environment a cloud's driver must not inherit (agent.Cloud.Unset).
func Unsetting(h agent.Host, unset []string) agent.Host {
	if len(unset) == 0 {
		return h
	}
	return unsetHost{Host: h, unset: unset}
}

type unsetHost struct {
	agent.Host
	unset []string
}

func (u unsetHost) Exec() agent.Exec { return unsetExec{u.Host.Exec(), u.unset} }

type unsetExec struct {
	e     agent.Exec
	unset []string
}

func (x unsetExec) Run(ctx context.Context, argv []string, o agent.RunOptions) (agent.Result, error) {
	o.Unset = append(append([]string(nil), o.Unset...), x.unset...)
	return x.e.Run(ctx, argv, o)
}
