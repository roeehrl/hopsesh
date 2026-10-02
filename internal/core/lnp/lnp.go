// Package lnp handles macOS local network privacy (macOS 15 and later).
//
// macOS asks once per app before it lets the app reach a device on the local network,
// and the ssh processes hopsesh starts count as hopsesh (Apple TN3179). The rules this
// package follows come from that note:
//
//   - Only some destinations are gated: addresses on a broadcast-capable interface's
//     subnet (Wi-Fi, Ethernet), link-local and multicast addresses, and names ending in
//     .local. Tailscale and other VPN addresses are not gated.
//   - There is no API that reports the permission. A connection made with Network
//     framework waits with the reason "local network denied", and continues on its own
//     when the person allows access. Probe uses exactly that.
//   - The prompt may deny the first connection before the person answers, so callers
//     wait and retry instead of reporting a failure straight away.
//   - Command-line tools started from Terminal or over SSH are always allowed. Other
//     terminal apps (iTerm, VS Code, …) carry their own permission.
package lnp

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// State is what a probe learned about local network access.
type State string

const (
	// NotNeeded: the destination is not on the local network, or the OS has no gate.
	NotNeeded State = "not-needed"
	// Allowed: an in-process connection to the destination succeeded.
	Allowed State = "allowed"
	// Denied: the connection was still waiting for local network access when the probe
	// gave up. The person either declined, or has not answered the macOS prompt yet;
	// macOS does not say which.
	Denied State = "denied"
	// Unknown: the gate applies but this build cannot probe (no cgo), or the
	// connection failed for some other reason (machine off, firewall).
	Unknown State = "unknown"
)

// SettingsURL opens System Settings at Privacy & Security. macOS has no link straight
// to the Local Network list, so the message tells the person where to scroll.
const SettingsURL = "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension"

// Gated reports whether this OS has local network privacy (macOS 15 or later).
func Gated() bool { return gated() }

// InApp reports whether this process is the main executable of a .app bundle, which is
// the code macOS holds responsible for its network use.
func InApp() bool {
	exe, err := os.Executable()
	return err == nil && strings.Contains(exe, ".app/Contents/MacOS/")
}

// Responsible names the app whose Local Network setting applies to this process, or ""
// when macOS always allows it (a command-line tool run from Terminal or over SSH).
func Responsible() string {
	if InApp() {
		return "hopsesh"
	}
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return ""
	}
	switch tp := os.Getenv("TERM_PROGRAM"); tp {
	case "Apple_Terminal":
		return ""
	case "":
		return "the app that started hopsesh"
	case "iTerm.app":
		return "iTerm"
	case "vscode":
		return "Visual Studio Code (or the editor whose terminal you are using)"
	case "WarpTerminal":
		return "Warp"
	case "ghostty":
		return "Ghostty"
	default:
		return strings.TrimSuffix(tp, ".app")
	}
}

// IsLocalName reports whether a host name is a multicast DNS name (.local).
func IsLocalName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return strings.HasSuffix(h, ".local")
}

var (
	ifMu     sync.Mutex
	ifNets   []*net.IPNet
	ifLoaded time.Time
)

// localNets returns the subnets of broadcast-capable interfaces that are up (Wi-Fi,
// Ethernet). VPN interfaces such as Tailscale's utun are point-to-point and excluded.
func localNets() []*net.IPNet {
	ifMu.Lock()
	defer ifMu.Unlock()
	if time.Since(ifLoaded) < 30*time.Second {
		return ifNets
	}
	ifNets, ifLoaded = nil, time.Now()
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagBroadcast == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				ifNets = append(ifNets, n)
			}
		}
	}
	return ifNets
}

// IsLocalAddr reports whether traffic to ip needs local network access.
func IsLocalAddr(ip net.IP) bool {
	return isLocalAddr(ip, localNets())
}

func isLocalAddr(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return false
	}
	if ip.IsMulticast() || ip.Equal(net.IPv4bcast) || ip.IsLinkLocalUnicast() {
		return true
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Needs reports whether connecting to host needs local network access, and the address
// that decided it. A .local name always does; it is not resolved here, because
// resolving it is itself gated.
func Needs(ctx context.Context, host string) (bool, string) {
	if !Gated() || host == "" {
		return false, ""
	}
	if IsLocalName(host) {
		return true, host
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return IsLocalAddr(ip), ip.String()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return false, ""
	}
	for _, a := range addrs {
		if IsLocalAddr(a.IP) {
			return true, a.IP.String()
		}
	}
	return false, ""
}

// Probe makes an in-process TCP connection to host:port with Network framework and
// reports whether local network access is the obstacle. It waits up to wait for the
// person to answer the macOS prompt; the connection proceeds by itself once they allow
// it. The connection is closed as soon as it is ready; nothing is sent.
func Probe(ctx context.Context, host string, port int, wait time.Duration) State {
	if !Gated() {
		return NotNeeded
	}
	key := net.JoinHostPort(host, strconv.Itoa(port))
	cacheMu.Lock()
	if c, ok := cache[key]; ok && time.Now().Before(c.until) {
		cacheMu.Unlock()
		return c.state
	}
	cacheMu.Unlock()
	st := probe(ctx, host, port, wait)
	// Denied is never cached: the person may be changing the setting right now.
	ttl := map[State]time.Duration{Allowed: 10 * time.Minute, Unknown: 2 * time.Minute}[st]
	if ttl > 0 && ctx.Err() == nil {
		cacheMu.Lock()
		cache[key] = cached{st, time.Now().Add(ttl)}
		cacheMu.Unlock()
	}
	return st
}

type cached struct {
	state State
	until time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cached{}
)

// Hint is the advice shown when local network access blocks a machine.
func Hint(responsible string) string {
	if responsible == "" {
		responsible = "hopsesh"
	}
	return "macOS is blocking " + responsible + " from reaching devices on your local network. " +
		"If macOS is asking, choose Allow. If you chose Don't Allow before, turn " + responsible +
		" on in System Settings → Privacy & Security → Local Network, then try again. " +
		"Machines reached over Tailscale work either way."
}

// memory remembers, per user, whether this app has ever been allowed, so that the long
// wait for an answer to the macOS prompt happens only while the prompt can be showing.
type memory struct {
	Seen         bool `json:"seen,omitempty"`         // a probe has finished at least once
	Allowed      bool `json:"allowed,omitempty"`      // some probe succeeded
	LongWaitDone bool `json:"longWaitDone,omitempty"` // we already waited for an answer once
}

var memMu sync.Mutex

func memPath(stateDir string) string { return filepath.Join(stateDir, "local-network.json") }

func loadMemory(stateDir string) memory {
	var m memory
	if b, err := os.ReadFile(memPath(stateDir)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// Wait returns how long a probe should wait for local network access: long while the
// macOS prompt may be on screen for the first time, short once the person has answered.
func Wait(stateDir string) time.Duration {
	memMu.Lock()
	defer memMu.Unlock()
	m := loadMemory(stateDir)
	if m.Allowed || m.LongWaitDone {
		return 4 * time.Second
	}
	return 45 * time.Second
}

// Granted reports whether access was confirmed before (and not seen denied since).
func Granted(stateDir string) bool {
	memMu.Lock()
	defer memMu.Unlock()
	return loadMemory(stateDir).Allowed
}

// FirstRun reports whether no probe has finished yet, so the macOS prompt may be about
// to appear for the first time.
func FirstRun(stateDir string) bool {
	if !Gated() {
		return false
	}
	memMu.Lock()
	defer memMu.Unlock()
	return !loadMemory(stateDir).Seen
}

// Remember records a probe's outcome.
func Remember(stateDir string, st State, waited time.Duration) {
	memMu.Lock()
	defer memMu.Unlock()
	m := loadMemory(stateDir)
	before := m
	m.Seen = true
	switch {
	case st == Allowed:
		m.Allowed = true
	case st == Denied:
		// Allowed before and denied now means the person turned it off; either way they
		// have answered, so later probes need not wait for a prompt.
		if m.Allowed || waited > 4*time.Second {
			m.LongWaitDone = true
		}
		m.Allowed = false
	}
	if m == before {
		return
	}
	if b, err := json.Marshal(m); err == nil {
		_ = os.MkdirAll(stateDir, 0o700)
		_ = os.WriteFile(memPath(stateDir), b, 0o600)
	}
}

// ResetWait makes the next probe wait long again (the person pressed "Try again"
// after changing the setting, or wants to answer a prompt they dismissed).
func ResetWait(stateDir string) {
	memMu.Lock()
	m := loadMemory(stateDir)
	m.Allowed, m.LongWaitDone = false, false
	if b, err := json.Marshal(m); err == nil {
		_ = os.WriteFile(memPath(stateDir), b, 0o600)
	}
	memMu.Unlock()
	cacheMu.Lock()
	cache = map[string]cached{}
	cacheMu.Unlock()
}
