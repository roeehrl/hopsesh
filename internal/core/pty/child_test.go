package pty_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	xterm "golang.org/x/term"
)

// The test binary is also the tabs' programs: PTY_TEST_ROLE picks one.
//
//	da1      raw mode; shows its size, asks the terminal what it is (DA1) and shows the
//	         answer, then echoes each key as got:<hex> (and its new size after a resize);
//	         q ends it with 3
//	exit     prints bye and ends with PTY_TEST_CODE
//	env      prints PTY_TEST_VARS' values and ends
//	flood    prints PTY_TEST_BYTES bytes, then flood-done, and ends with 0
//	sleep    prints sleeping and sleeps a minute (cooked mode: Ctrl-C ends it)
//	bell     prints working, rings the bell, waits for a key, prints key and ends
//	notify   prints working, sends an OSC 9 notification, waits for a key and ends
//	quiet    prints hello and sleeps a minute
//	print    prints PTY_TEST_TEXT and ends
func TestMain(m *testing.M) {
	if role := os.Getenv("PTY_TEST_ROLE"); role != "" {
		os.Exit(child(role))
	}
	os.Exit(m.Run())
}

func child(role string) int {
	prepareConsole()
	out := os.Stdout
	switch role {
	case "exit":
		fmt.Fprint(out, "bye\r\n")
		code, _ := strconv.Atoi(os.Getenv("PTY_TEST_CODE"))
		return code
	case "env":
		for _, k := range strings.Split(os.Getenv("PTY_TEST_VARS"), ",") {
			v, ok := os.LookupEnv(k)
			fmt.Fprintf(out, "ENV %s=%s set=%v\r\n", k, v, ok)
		}
		fmt.Fprint(out, "env-done\r\n")
		return 0
	case "flood":
		n, _ := strconv.Atoi(os.Getenv("PTY_TEST_BYTES"))
		line := strings.Repeat("x", 99) + "\n"
		for written := 0; written < n; written += len(line) {
			if _, err := out.WriteString(line); err != nil {
				return 9
			}
		}
		fmt.Fprint(out, "flood-done\r\n")
		return 0
	case "sleep":
		fmt.Fprint(out, "sleeping\r\n")
		time.Sleep(time.Minute)
		return 0
	case "quiet":
		fmt.Fprint(out, "hello\r\n")
		time.Sleep(time.Minute)
		return 0
	case "print":
		fmt.Fprint(out, os.Getenv("PTY_TEST_TEXT"))
		return 0
	case "bell", "notify":
		st, err := xterm.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			fmt.Fprintf(out, "no raw mode: %v\r\n", err)
			return 1
		}
		defer xterm.Restore(int(os.Stdin.Fd()), st)
		fmt.Fprint(out, "working\r\n")
		time.Sleep(200 * time.Millisecond)
		if role == "bell" {
			fmt.Fprint(out, "\a")
		} else {
			fmt.Fprint(out, "\x1b]9;Build finished\x1b\\")
		}
		b := make([]byte, 1)
		_, _ = os.Stdin.Read(b)
		fmt.Fprint(out, "key\r\n")
		time.Sleep(300 * time.Millisecond)
		return 0
	case "da1":
		return da1Child()
	}
	return 99
}

func da1Child() int {
	out := os.Stdout
	size := func() string {
		w, h, _ := xterm.GetSize(int(out.Fd()))
		return fmt.Sprintf("%dx%d", w, h)
	}
	st, err := xterm.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintf(out, "no raw mode: %v\r\n", err)
		return 1
	}
	defer xterm.Restore(int(os.Stdin.Fd()), st)
	fmt.Fprintf(out, "ready size=%s\r\n", size())
	keys := make(chan byte, 64)
	go func() {
		b := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(b)
			for _, c := range b[:n] {
				keys <- c
			}
			if err != nil {
				close(keys)
				return
			}
		}
	}()
	fmt.Fprint(out, "\x1b[c")
	reply := ""
	timeout := time.After(5 * time.Second)
wait:
	for !strings.HasSuffix(reply, "c") {
		select {
		case k, ok := <-keys:
			if !ok {
				break wait
			}
			reply += string(k)
		case <-timeout:
			break wait
		}
	}
	fmt.Fprintf(out, "da1=%q\r\n", reply)
	resized := watchSize(size)
	last := size()
	for {
		select {
		case <-resized:
			if s := size(); s != last {
				last = s
				fmt.Fprintf(out, "resized %s\r\n", s)
			}
		case k, ok := <-keys:
			if !ok {
				return 4
			}
			if k == 'q' {
				fmt.Fprint(out, "quitting\r\n")
				return 3
			}
			fmt.Fprintf(out, "got:%02x\r\n", k)
		}
	}
}
