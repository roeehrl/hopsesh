// Command meshrdv connects CI runners to each other for the cross-machine scenario matrix:
// each runner's sshd is reachable through a dumbpipe (iroh) listener, and the runners swap
// what they need through the workflow run's own artifacts.
//
//	meshrdv listen  -node linux -user hsremote -out rdv/public     # start the listener, write peer.json
//	(upload rdv/public as artifact peer-<node>)
//	meshrdv connect -node linux -peers "windows macos" -dir rdv     # fetch peers, authorize, tunnel, ssh config
//	(run the matrix against hs-<peer>; upload artifact done-<node>)
//	meshrdv barrier -node linux -peers "windows macos" -dir rdv     # wait until every peer is done
//
// A ticket lets anyone reach this runner's sshd while the job runs; logins are by key only
// (the peers' keys, published here), so that reaches nothing. Runs need gh and GH_TOKEN.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Peer is what a runner publishes about itself.
type Peer struct {
	Node   string `json:"node"`
	User   string `json:"user"`   // who to log in as there
	Ticket string `json:"ticket"` // dumbpipe ticket for its sshd
	Key    string `json:"key"`    // its ssh client public key, to authorize here
}

func main() {
	if len(os.Args) < 2 {
		fatal(errors.New("usage: meshrdv listen|connect|barrier …"))
	}
	fl := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	node := fl.String("node", "", "this runner: linux | windows | macos")
	user := fl.String("user", "", "listen: the account peers log in as")
	out := fl.String("out", "rdv/public", "listen: where to write peer.json")
	peers := fl.String("peers", "", "connect, barrier: the other runners")
	dir := fl.String("dir", "rdv", "connect, barrier: working folder")
	wait := fl.Duration("wait", 45*time.Minute, "how long to wait for peers")
	_ = fl.Parse(os.Args[2:])
	var err error
	switch os.Args[1] {
	case "listen":
		err = listen(*node, *user, *out)
	case "connect":
		err = connect(*node, strings.Fields(*peers), *dir, *wait)
	case "barrier":
		err = barrier(strings.Fields(*peers), *dir, *wait)
	default:
		err = fmt.Errorf("unknown command %s", os.Args[1])
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "meshrdv:", err)
	// Tunnel and rendezvous trouble is infrastructure, not hopsesh: say so for the summary.
	if p := os.Getenv("GITHUB_OUTPUT"); p != "" {
		if f, e := os.OpenFile(p, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); e == nil {
			fmt.Fprintln(f, "infra=true")
			f.Close()
		}
	}
	os.Exit(1)
}

func dumbpipe() string {
	if p := os.Getenv("DUMBPIPE"); p != "" {
		return p
	}
	return "dumbpipe"
}

func home() string { h, _ := os.UserHomeDir(); return h }

var ticketRE = regexp.MustCompile(`dumbpipe connect(?:-tcp)?\s+(\S+)`)

// listen starts `dumbpipe listen-tcp` for this runner's sshd, detached so it outlives the
// step, and writes peer.json once it prints its ticket.
func listen(node, user, out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(filepath.Dir(out), "listener.log")
	lf, err := os.Create(logPath)
	if err != nil {
		return err
	}
	cmd := exec.Command(dumbpipe(), "listen-tcp", "--host", "127.0.0.1:22")
	cmd.Stdout, cmd.Stderr = lf, lf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	var ticket string
	for i := 0; i < 120 && ticket == ""; i++ {
		time.Sleep(500 * time.Millisecond)
		b, _ := os.ReadFile(logPath)
		if m := ticketRE.FindSubmatch(b); m != nil {
			ticket = string(m[1])
		}
	}
	if ticket == "" {
		b, _ := os.ReadFile(logPath)
		return fmt.Errorf("dumbpipe printed no ticket:\n%s", b)
	}
	key, err := os.ReadFile(filepath.Join(home(), ".ssh", "id_ed25519.pub"))
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(Peer{Node: node, User: user, Ticket: ticket, Key: strings.TrimSpace(string(key))}, "", "  ")
	fmt.Printf("%s listening (ticket %s…)\n", node, ticket[:min(16, len(ticket))])
	return os.WriteFile(filepath.Join(out, "peer.json"), b, 0o644)
}

// artifact downloads one artifact of this run when it exists (false when not yet).
func artifact(name, dest string) (bool, error) {
	repo, run := os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_RUN_ID")
	out, err := exec.Command("gh", "api", fmt.Sprintf("repos/%s/actions/runs/%s/artifacts?name=%s", repo, run, name), "--jq", ".total_count").Output()
	if err != nil {
		return false, nil // transient: try again
	}
	if n, _ := strconv.Atoi(strings.TrimSpace(string(out))); n == 0 {
		return false, nil
	}
	_ = os.RemoveAll(dest)
	if b, err := exec.Command("gh", "run", "download", run, "-R", repo, "-n", name, "-D", dest).CombinedOutput(); err != nil {
		return false, fmt.Errorf("downloading %s: %v: %s", name, err, b)
	}
	return true, nil
}

func waitFor(name, dest string, d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		ok, err := artifact(name, dest)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("%s never appeared (waited %s)", name, d)
}

// connect fetches each peer's peer.json, lets its key log in here, opens a local port to
// its sshd through dumbpipe and names it hs-<peer> in ~/.ssh/config.
func connect(node string, peers []string, dir string, d time.Duration) error {
	cfg := &strings.Builder{}
	for i, p := range peers {
		dest := filepath.Join(dir, "peers", p)
		if err := waitFor("peer-"+p, dest, d); err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(dest, "peer.json"))
		if err != nil {
			return err
		}
		var peer Peer
		if err := json.Unmarshal(b, &peer); err != nil {
			return err
		}
		if err := authorize(peer.Key); err != nil {
			return fmt.Errorf("authorizing %s: %w", p, err)
		}
		port := 2201 + i
		lf, _ := os.Create(filepath.Join(dir, "connect-"+p+".log"))
		cmd := exec.Command(dumbpipe(), "connect-tcp", "--addr", "127.0.0.1:"+strconv.Itoa(port), peer.Ticket)
		cmd.Stdout, cmd.Stderr = lf, lf
		detach(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		_ = cmd.Process.Release()
		fmt.Fprintf(cfg, "\nHost hs-%s\n  HostName 127.0.0.1\n  Port %d\n  User %s\n  HostKeyAlias hs-%s\n", p, port, peer.User, p)
	}
	sshDir := filepath.Join(home(), ".ssh")
	f, err := os.OpenFile(filepath.Join(sshDir, "config"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, _ = f.WriteString(cfg.String())
	f.Close()
	// Each tunnel answers before the matrix starts (the peer may still be setting up).
	for _, p := range peers {
		var last []byte
		ok := false
		for i := 0; i < 30 && !ok; i++ {
			out, err := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=20", "hs-"+p, "echo mesh-ok").CombinedOutput()
			last, ok = out, err == nil && strings.Contains(string(out), "mesh-ok")
			if !ok {
				time.Sleep(10 * time.Second)
			}
		}
		if !ok {
			return fmt.Errorf("ssh hs-%s through dumbpipe never answered: %s", p, last)
		}
		fmt.Printf("%s → %s: connected\n", node, p)
	}
	return nil
}

// authorize lets a peer's key log in as the account peers use here.
func authorize(key string) error {
	if runtime.GOOS == "windows" {
		f, err := os.OpenFile(`C:\ProgramData\ssh\administrators_authorized_keys`, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString(key + "\r\n")
		return err
	}
	cmd := exec.Command("sudo", "sh", "-c", `h=$(eval echo ~hsremote); mkdir -p "$h/.ssh" && cat >> "$h/.ssh/authorized_keys" && chown -R hsremote "$h/.ssh" && chmod 600 "$h/.ssh/authorized_keys"`)
	cmd.Stdin = strings.NewReader(key + "\n")
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, b)
	}
	return nil
}

// barrier waits until every peer has uploaded done-<peer>, so no runner goes away while
// another still uses it.
func barrier(peers []string, dir string, d time.Duration) error {
	for _, p := range peers {
		if err := waitFor("done-"+p, filepath.Join(dir, "done", p), d); err != nil {
			return err
		}
	}
	return nil
}
