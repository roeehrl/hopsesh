package hosts

import (
	"os"
	"path/filepath"
	"testing"
)

const tsJSON = `{"Self":{"HostName":"alice-mac","DNSName":"alice-mac.tail9.ts.net.","OS":"macOS","TailscaleIPs":["100.64.0.10"],"UserID":7},
"Peer":{
 "a":{"HostName":"Alice’s Mac mini","DNSName":"alices-mac-mini.tail9.ts.net.","OS":"macOS","TailscaleIPs":["100.64.0.11"],"Online":true,"UserID":7},
 "b":{"HostName":"Alice’s MacBook Pro","DNSName":"alices-macbook-pro.tail9.ts.net.","OS":"macOS","TailscaleIPs":["100.64.0.12"],"Online":true,"UserID":7},
 "c":{"HostName":"localhost","DNSName":"iphone.tail9.ts.net.","OS":"iOS","Online":true,"UserID":7},
 "d":{"HostName":"Bob’s MacBook Pro","DNSName":"bobs-macbook-pro.tail9.ts.net.","OS":"macOS","Online":true,"UserID":9},
 "e":{"HostName":"ALICE-PC","DNSName":"alice-pc.tail9.ts.net.","OS":"windows","TailscaleIPs":["100.64.0.13"],"Online":false,"UserID":7},
 "f":{"HostName":"old","DNSName":"old.tail9.ts.net.","OS":"linux","Expired":true,"UserID":7}},
"User":{"7":{"LoginName":"alice@example.com"},"9":{"LoginName":"bob@example.com"}}}`

func TestDiscoverMerge(t *testing.T) {
	dir := t.TempDir()
	inc := filepath.Join(dir, "extra")
	os.WriteFile(inc, []byte("Host build\n  HostName 10.0.0.5\n  User ci\n"), 0o600)
	cfg := filepath.Join(dir, "config")
	os.WriteFile(cfg, []byte(`Include `+inc+`
Host github.com
  HostName github.com
  User git
Host laptop Alices-MacBook-Pro.local
    HostName Alices-MacBook-Pro.local
    User alice
Host mini alices-mac-mini
    HostName 100.64.0.11
    User alice
Host alice-pc
    HostName 100.64.0.13
Host *.internal
    User x
Match host foo
    User nope
`), 0o600)
	ts, err := parseTailscale([]byte(tsJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 5 { // self + 4 peers (phone and expired dropped)
		t.Fatalf("tailscale candidates: %d %+v", len(ts), ts)
	}
	aliases := SSHConfigAliases(cfg)
	if len(aliases) != 5 {
		t.Fatalf("aliases: %+v", aliases)
	}
	got := Merge(ts, aliases)
	by := map[string]Candidate{}
	for _, c := range got {
		by[c.Name] = c
	}
	if c := by["mini"]; c.DNSName != "alices-mac-mini.tail9.ts.net" || len(c.Via) != 2 {
		t.Errorf("mini not merged by IP: %+v", c)
	}
	if c := by["laptop"]; c.DNSName != "alices-macbook-pro.tail9.ts.net" {
		t.Errorf("laptop not merged by name: %+v", c)
	}
	if c := by["alice-pc"]; c.OS != "windows" || c.Online == nil || *c.Online {
		t.Errorf("alice-pc: %+v", c)
	}
	if c := by["bobs-macbook-pro"]; !c.OtherOwner || c.Owner != "bob@example.com" {
		t.Errorf("other owner: %+v", c)
	}
	if _, ok := by["github.com"]; ok {
		t.Error("git forges must be filtered")
	}
	if c := by["build"]; c.Destination != "build" || c.Via[0] != "ssh-config" {
		t.Errorf("included alias: %+v", c)
	}
	if c := by["alice-mac"]; !c.Self {
		t.Error("self not marked")
	}
}
