package pty

// ring is a tab's scrollback: the last cap bytes its program printed, in memory only, so a
// window that reloads (or a tab shown again) can draw what was there. It is never written
// anywhere; wipe zeroes it when the tab goes. Not safe for concurrent use (the session's
// lock guards it).
type ring struct {
	buf   []byte
	start int // where the oldest byte is, once full
	full  bool
}

func newRing(capacity int) *ring { return &ring{buf: make([]byte, 0, capacity)} }

func (r *ring) write(p []byte) {
	c := cap(r.buf)
	if c == 0 {
		return
	}
	if len(p) >= c {
		r.buf = append(r.buf[:0], p[len(p)-c:]...)
		r.start, r.full = 0, true
		return
	}
	if !r.full {
		if room := c - len(r.buf); len(p) <= room {
			r.buf = append(r.buf, p...)
			r.full = len(r.buf) == c
			return
		}
		room := c - len(r.buf)
		r.buf = append(r.buf, p[:room]...)
		p = p[room:]
		r.full = true
	}
	for len(p) > 0 {
		n := copy(r.buf[r.start:], p)
		p = p[n:]
		r.start = (r.start + n) % c
	}
}

// bytes is a copy of what is kept, oldest first.
func (r *ring) bytes() []byte {
	out := make([]byte, 0, len(r.buf))
	if !r.full {
		return append(out, r.buf...)
	}
	out = append(out, r.buf[r.start:]...)
	return append(out, r.buf[:r.start]...)
}

// wipe zeroes what is kept and forgets it.
func (r *ring) wipe() {
	clear(r.buf[:cap(r.buf)])
	r.buf, r.start, r.full = r.buf[:0], 0, false
}
