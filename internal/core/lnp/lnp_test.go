package lnp

import (
	"net"
	"testing"
)

func TestIsLocalAddr(t *testing.T) {
	_, lan, _ := net.ParseCIDR("192.168.1.20/24")
	nets := []*net.IPNet{lan}
	cases := map[string]bool{
		"192.168.1.7":     true,  // same Wi-Fi/Ethernet subnet
		"192.168.2.7":     false, // beyond a router
		"169.254.3.4":     true,  // link-local
		"fe80::1":         true,
		"224.0.0.251":     true, // mDNS multicast
		"ff02::fb":        true,
		"255.255.255.255": true,
		"100.101.102.103": false, // Tailscale (VPN, not gated)
		"127.0.0.1":       false,
		"8.8.8.8":         false,
	}
	for s, want := range cases {
		if got := isLocalAddr(net.ParseIP(s), nets); got != want {
			t.Errorf("%s: got %v, want %v", s, got, want)
		}
	}
}

func TestIsLocalName(t *testing.T) {
	for s, want := range map[string]bool{"alices-mbp.local": true, "Mini.LOCAL.": true, "mini.tail1234.ts.net": false, "localhost": false} {
		if IsLocalName(s) != want {
			t.Errorf("%s: want %v", s, want)
		}
	}
}
