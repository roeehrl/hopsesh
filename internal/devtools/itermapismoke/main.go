// Command itermapismoke is the maintainer's manual check of the iTerm2 API client against a
// real iTerm2 (run it through scripts/iterm-api-smoke.sh, from a tab in iTerm2, with the
// API already enabled by you). It lists sessions, finds the tab it runs in by TTY, opens a
// tab running `sh -c 'sleep 8; exit 3'` and waits for its exit, opens a split beside this
// tab, labels it, and focuses this tab again. It sends no text to any session and reads no
// screen. CI never runs it.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/termapp/iterm2api"
)

var failed int

func step(name string, err error) bool {
	if err != nil {
		failed++
		fmt.Printf("FAIL  %s: %v\n", name, err)
		return false
	}
	fmt.Printf("ok    %s\n", name)
	return true
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	opts := iterm2api.Options{}

	if err := iterm2api.Probe(ctx, opts); err != nil {
		fmt.Printf("The API is not listening (%v).\nTurn it on yourself in iTerm2 > Settings > General > Magic > Enable Python API; hopsesh never does.\n", err)
		os.Exit(2)
	}
	fmt.Println("ok    probe: the API is listening (no credentials were sent)")

	c, err := iterm2api.Connect(ctx, opts)
	if !step("connect (cookie through AppleScript; macOS may ask once)", err) {
		os.Exit(1)
	}
	defer c.Close()
	fmt.Printf("      protocol version %q\n", c.ProtocolVersion())

	layout, err := c.ListSessions(ctx)
	if step("list sessions", err) {
		fmt.Printf("      %d windows, %d sessions\n", len(layout.Windows), len(layout.Sessions()))
	}
	win, err := c.KeyWindow(ctx)
	if step("key window", err) && win == "" {
		step("key window", errors.New("none reported"))
	}

	// This tab, found by the TTY the OS gives this process (never by anything printed).
	var here iterm2api.Located
	tty, err := ownTTY()
	if step("own tty from the OS", err) {
		var ok bool
		here, ok, err = c.FindTTY(ctx, tty)
		if err == nil && !ok {
			err = fmt.Errorf("no session has %s (run this from an iTerm2 tab)", tty)
		}
		if step("find this tab by tty "+tty, err) {
			fmt.Printf("      window %s, tab %s, session %s\n", here.WindowID, here.TabID, here.SessionID)
			if win == "" {
				win = here.WindowID
			}
		}
	}

	// hopsesh's AppleScript path names a tab by AppleScript's "unique ID", the API path by
	// the API's session id; a Handle from one may be focused by the other, so they must be
	// the same id. Read only (a tab's tty and id), as hopsesh's AppleScript allowlist has it.
	if here.SessionID != "" {
		as, err := appleScriptID(ctx, tty)
		if err == nil && as != here.SessionID {
			err = fmt.Errorf("AppleScript says %q, the API %q", as, here.SessionID)
		}
		step("AppleScript's unique ID is the API's session id", err)
	}

	step("subscribe to terminate, new-session and focus events", c.Subscribe(ctx))

	// A tab whose program exits 3. The protocol reports only that the session ended; the
	// code comes from the launch's own record, as hopsesh's terminal-step writes one.
	dir, err := os.MkdirTemp("", "itermsmoke")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	statusFile := filepath.Join(dir, "status")
	inner := iterm2api.QuoteArgv([]string{"sh", "-c", "sleep 8; exit 3"})
	cmd := iterm2api.QuoteArgv([]string{"/bin/sh", "-c", inner + "; s=$?; echo $s > " + iterm2api.QuoteArgv([]string{statusFile}) + "; exit $s"})
	fmt.Printf("      tab command: %s\n", cmd)
	tab, err := c.OpenTab(ctx, win, iterm2api.Launch{Command: cmd, Dir: dir})
	if step("open a tab in the key window running sh -c 'sleep 8; exit 3'", err) {
		fmt.Printf("      session %s (window %s, tab %s)\n", tab.SessionID, tab.WindowID, tab.TabID)
		fmt.Println("      waiting for its session to terminate (if your profile keeps ended sessions open, close that tab)")
		if step("terminate event for that session", waitTerminated(ctx, c, tab.SessionID, 2*time.Minute)) {
			b, err := os.ReadFile(statusFile)
			got := strings.TrimSpace(string(b))
			if err == nil && got != "3" {
				err = fmt.Errorf("status file says %q", got)
			}
			step("exit status 3 (from the launch's own record)", err)
		}
	}

	if here.SessionID != "" {
		split, err := c.OpenSplit(ctx, here.SessionID, iterm2api.SplitRight, iterm2api.Launch{Command: iterm2api.QuoteArgv([]string{"sh", "-c", "sleep 10"})})
		if step("open a split beside this tab (closes itself after 4 s)", err) {
			step("label the split (user.hopsesh_title)", c.SetLabels(ctx, split.SessionID, map[string]string{"title": "hopsesh smoke"}))
			step("terminate event for the split", waitTerminated(ctx, c, split.SessionID, time.Minute))
		}
		step("focus this tab again", c.Focus(ctx, here.SessionID))
	}

	if failed > 0 {
		fmt.Printf("%d step(s) failed\n", failed)
		os.Exit(1)
	}
	fmt.Println("all steps passed")
}

func waitTerminated(ctx context.Context, c *iterm2api.Client, session string, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case e, ok := <-c.Events():
			if !ok {
				return errors.New("connection closed")
			}
			if e.Kind == iterm2api.SessionTerminated && e.SessionID == session {
				return nil
			}
		case <-t.C:
			return fmt.Errorf("no terminate event within %v", d)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// appleScriptID is the unique ID AppleScript gives the session on tty.
func appleScriptID(ctx context.Context, tty string) (string, error) {
	script := `tell application id "com.googlecode.iterm2"
	repeat with w in windows
		repeat with t in tabs of w
			repeat with s in sessions of t
				if tty of s is "` + tty + `" then return (unique ID of s)
			end repeat
		end repeat
	end repeat
end tell
return ""`
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("osascript: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ownTTY asks the OS which terminal this process runs on.
func ownTTY() (string, error) {
	cmd := exec.Command("tty")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("standard input is not a terminal: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
