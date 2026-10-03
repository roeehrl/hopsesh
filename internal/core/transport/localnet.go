package transport

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/audit"
	"github.com/roeehrl/hopsesh/internal/core/lnp"
	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// LocalNetworkError means macOS local network privacy is what stopped the connection.
type LocalNetworkError struct {
	Host        string    // the address or .local name that needs local network access
	State       lnp.State // Denied when an in-process probe confirmed it, Unknown otherwise
	Responsible string    // the app whose Local Network setting applies
	Detail      string    // ssh's own message
}

func (e *LocalNetworkError) Error() string {
	who := e.Responsible
	if who == "" {
		who = "hopsesh"
	}
	s := "macOS local network privacy blocked " + who + " from reaching " + e.Host
	if e.State != lnp.Denied {
		s = "probably " + s
	}
	if e.Detail != "" {
		s += " (" + e.Detail + ")"
	}
	return s
}

// Unwrap keeps errors.Is(err, ErrUnreachable) true for callers that only care about that.
func (e *LocalNetworkError) Unwrap() error { return ErrUnreachable }

// Hint is the advice for this error.
func (e *LocalNetworkError) Hint() string { return lnp.Hint(e.Responsible) }

type gatedTarget struct {
	host  string
	port  int
	state lnp.State
}

// localTargets lists the places ssh (or its ProxyJump/ProxyCommand) will connect to that
// need local network access. It reads ssh -G, so the user's ssh config decides.
func (c *Conn) localTargets(ctx context.Context) []gatedTarget {
	args := []string{"-G"}
	if c.override != "" {
		args = append(args, "-o", "HostName="+c.override)
	}
	out, err := proc.CommandContext(ctx, c.sshBinary, append(args, c.Dest)...).Output()
	if err != nil {
		return nil
	}
	cfg := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		if _, seen := cfg[k]; !seen {
			cfg[k] = v
		}
	}
	port, _ := strconv.Atoi(cfg["port"])
	if port == 0 {
		port = 22
	}
	var cands []gatedTarget
	proxyCmd := cfg["proxycommand"]
	if proxyCmd == "" || proxyCmd == "none" || strings.Contains(proxyCmd, "%h") {
		cands = append(cands, gatedTarget{host: cfg["hostname"], port: port})
	}
	if pc := proxyCmd; pc != "" && pc != "none" {
		// A proxy script may try a LAN name before anything else; any .local name or
		// literal address in it is a place it can connect to.
		for _, tok := range strings.Fields(pc) {
			tok = strings.Trim(tok, `"'`)
			if lnp.IsLocalName(tok) || net.ParseIP(tok) != nil {
				cands = append(cands, gatedTarget{host: tok, port: 22})
			}
		}
	}
	if pj := cfg["proxyjump"]; pj != "" && pj != "none" {
		// The first jump host is the only one this machine connects to itself.
		first := strings.Split(pj, ",")[0]
		first = strings.TrimPrefix(first, "ssh://")
		if i := strings.LastIndexByte(first, '@'); i >= 0 {
			first = first[i+1:]
		}
		h, p := first, 22
		if hh, pp, err := net.SplitHostPort(first); err == nil {
			h = hh
			p, _ = strconv.Atoi(pp)
		}
		if sub, err := proc.CommandContext(ctx, c.sshBinary, "-G", h).Output(); err == nil {
			for _, line := range strings.Split(string(sub), "\n") {
				if v, ok := strings.CutPrefix(line, "hostname "); ok {
					h = v
					break
				}
			}
		}
		cands = append(cands, gatedTarget{host: h, port: p})
	}
	var gated []gatedTarget
	for _, t := range cands {
		if need, _ := lnp.Needs(ctx, t.host); need {
			gated = append(gated, t)
		}
	}
	return gated
}

// localNetworkPreflight runs once per destination name, before the first ssh. In the
// app it connects to each gated target in-process, so that macOS shows the prompt for
// hopsesh (a spawned ssh does not reliably bring it up) and so that ssh starts only
// after the person has answered. It never blocks a connection by itself.
func (c *Conn) localNetworkPreflight(ctx context.Context) []gatedTarget {
	if !lnp.Gated() {
		return nil
	}
	c.lnMu.Lock()
	defer c.lnMu.Unlock()
	key := c.Dest + "|" + c.override
	if c.lnChecked == nil {
		c.lnChecked = map[string][]gatedTarget{}
	}
	if t, ok := c.lnChecked[key]; ok {
		return t
	}
	targets := c.localTargets(ctx)
	// Once access has been granted there is no prompt to bring up, so skip the probe;
	// a failure is checked afterwards (recheckLocalNetwork) in case it was turned off.
	if !lnp.Granted(c.StateDir) {
		c.probeTargets(ctx, targets, lnp.Wait(c.StateDir))
	}
	c.lnChecked[key] = targets
	return targets
}

// probeTargets probes each target in-process, when this process is an app that can.
func (c *Conn) probeTargets(ctx context.Context, targets []gatedTarget, wait time.Duration) {
	if !lnp.InApp() || !lnp.CanProbe {
		return
	}
	for i := range targets {
		start := time.Now()
		targets[i].state = lnp.Probe(ctx, targets[i].host, targets[i].port, wait)
		lnp.Remember(c.StateDir, targets[i].state, time.Since(start))
		c.Log.Write(audit.Entry{Action: "local-network.probe", Host: c.Dest, Detail: map[string]any{
			"target": targets[i].host, "state": string(targets[i].state), "ms": time.Since(start).Milliseconds()}})
	}
}

// recheckLocalNetwork probes gated targets that were not probed before ssh ran, after
// ssh failed to reach the machine, so the error can say whether access was turned off.
func (c *Conn) recheckLocalNetwork(ctx context.Context, err error, targets []gatedTarget) {
	if !errors.Is(err, ErrUnreachable) || len(targets) == 0 {
		return
	}
	c.lnMu.Lock() // targets is the cached slice shared with other commands
	defer c.lnMu.Unlock()
	var unprobed []gatedTarget
	for _, t := range targets {
		if t.state == "" {
			unprobed = append(unprobed, t)
		}
	}
	if len(unprobed) == 0 {
		return
	}
	c.probeTargets(ctx, unprobed, 4*time.Second)
	for i := range targets {
		for _, u := range unprobed {
			if u.host == targets[i].host && u.port == targets[i].port {
				targets[i].state = u.state
			}
		}
	}
}

// explainLocalNetwork turns an ssh failure into a LocalNetworkError when local network
// privacy is the likely cause.
func (c *Conn) explainLocalNetwork(err error, targets []gatedTarget) error {
	if err == nil || len(targets) == 0 || !errors.Is(err, ErrUnreachable) {
		return err
	}
	var already *LocalNetworkError
	if errors.As(err, &already) {
		return err
	}
	resp := lnp.Responsible()
	if resp == "" {
		return err // Terminal and SSH sessions are always allowed
	}
	low := strings.ToLower(err.Error())
	blocked := strings.Contains(low, "no route to host") ||
		(strings.Contains(low, "could not resolve") && lnp.IsLocalName(targets[0].host))
	t := targets[0]
	for _, x := range targets {
		if x.state == lnp.Denied {
			t, blocked = x, true // confirmed in-process: trust it over ssh's wording
			break
		}
		if x.state == lnp.Allowed {
			blocked = false
		}
	}
	if !blocked {
		return err
	}
	return &LocalNetworkError{Host: t.host, State: t.state, Responsible: resp, Detail: firstLine(strings.TrimPrefix(err.Error(), ErrUnreachable.Error()+": "))}
}
