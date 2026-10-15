package serve

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
)

// shortHome keeps the socket path under the length limit; Windows has no
// /tmp, and its temporary folder is short enough.
func shortHome(t *testing.T) string {
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = ""
	}
	d, err := os.MkdirTemp(base, "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestServeAnswersOnlyOnceAndDialsByMode(t *testing.T) {
	home := shortHome(t)
	ctx := context.Background()
	if v := Running(ctx, home); v != "" {
		t.Fatalf("answered before listening: %q", v)
	}
	l, err := Listen(home)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := Listen(home); !errors.Is(err, ErrRunning) {
		t.Fatalf("a second hopsesh serve listened: %v", err)
	}
	fi, err := os.Stat(Dir(home))
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("socket folder is not private: %v %v", fi.Mode(), err)
	}
	go func() {
		for {
			c, r, h, err := l.Accept()
			if err != nil {
				return
			}
			_ = Reply(c, Welcome{OK: true, Version: "9.9.9"})
			if h.Mode == ModeTerminal {
				f := NewFrameConn(ctx, r, c, c.Close)
				p, _ := f.Receive()
				_ = f.Send(append([]byte("echo:"), p...))
			}
			c.Close()
		}
	}()
	if v := Running(ctx, home); v != "9.9.9" {
		t.Fatalf("ping: %q", v)
	}
	c, r, _, err := Dial(ctx, home, ModeTerminal)
	if err != nil {
		t.Fatal(err)
	}
	f := NewFrameConn(ctx, r, c, c.Close)
	defer f.Close()
	if err := f.Send([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	got, err := f.Receive()
	if err != nil || !bytes.Equal(got, []byte("echo:hi")) {
		t.Fatalf("%q %v", got, err)
	}
}

func TestFramesRejectOversize(t *testing.T) {
	var b bytes.Buffer
	if err := WriteFrame(&b, make([]byte, MaxFrame+1)); err == nil {
		t.Fatal("wrote an oversized frame")
	}
	b.Write([]byte{0xff, 0xff, 0xff, 0xff})
	if _, err := ReadFrame(&b); err == nil {
		t.Fatal("read an oversized frame")
	}
}
