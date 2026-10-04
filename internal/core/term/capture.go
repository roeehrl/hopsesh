package term

import (
	"strings"
	"sync"
	"unicode/utf8"
)

// MaxCapture is how much of a program's output a Capture keeps: its last 64 KB.
const MaxCapture = 64 << 10

// Capture keeps the last MaxCapture bytes a program printed, in memory only, for a
// module's reader to look at once the program ended. It is safe for concurrent use.
type Capture struct {
	mu  sync.Mutex
	buf []byte
}

// Write keeps p, dropping the oldest bytes beyond MaxCapture.
func (c *Capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf = append(c.buf, p...)
	if over := len(c.buf) - MaxCapture; over > 0 {
		c.buf = append(c.buf[:0], c.buf[over:]...)
	}
	return len(p), nil
}

// Text is what was kept, as plain text (see Plain).
func (c *Capture) Text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Plain(c.buf)
}

// Reset forgets what was kept.
func (c *Capture) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.buf)
	c.buf = c.buf[:0]
}

// Plain turns what a program printed to a terminal into plain text: control sequences
// (CSI, OSC, DCS and the like) are taken out, a cursor move to another line becomes a line
// break and one along the line a space, carriage returns become line breaks, and the other
// control characters but tab go. The target of an OSC 8 hyperlink stays, between spaces,
// since a program may show a link's text and keep its address there. Invalid UTF-8 is
// dropped.
func Plain(b []byte) string {
	var o strings.Builder
	o.Grow(len(b))
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b && i+1 < len(b):
			i = escape(b, i, &o)
		case c == 0x1b:
			i++
		case c == '\r':
			if i+1 < len(b) && b[i+1] == '\n' {
				i++
			}
			o.WriteByte('\n')
			i++
		case c == '\n' || c == '\t':
			o.WriteByte(c)
			i++
		case c < 0x20 || c == 0x7f:
			i++
		case c < utf8.RuneSelf:
			o.WriteByte(c)
			i++
		default:
			r, n := utf8.DecodeRune(b[i:])
			if r != utf8.RuneError || n > 1 {
				if r < 0x80 || r > 0x9f { // C1 controls go too
					o.WriteRune(r)
				}
			}
			i += n
		}
	}
	return o.String()
}

// escape reads the escape sequence at b[i] (an ESC with more after it) and returns where
// the text goes on.
func escape(b []byte, i int, o *strings.Builder) int {
	switch b[i+1] {
	case '[': // CSI: parameters and intermediates, then a final byte in @–~
		j := i + 2
		for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
			j++
		}
		if j >= len(b) {
			return len(b)
		}
		switch b[j] {
		case 'A', 'B', 'E', 'F', 'H', 'f', 'd':
			o.WriteByte('\n')
		case 'C', 'G':
			o.WriteByte(' ')
		}
		return j + 1
	case ']': // OSC, ended by BEL or ST
		j, end := i+2, -1
		for j < len(b) {
			if b[j] == 0x07 {
				end = j + 1
				break
			}
			if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' {
				end = j + 2
				break
			}
			j++
		}
		if end < 0 {
			return len(b)
		}
		body := string(b[i+2 : j])
		if rest, ok := strings.CutPrefix(body, "8;"); ok {
			if _, target, ok := strings.Cut(rest, ";"); ok && target != "" {
				o.WriteString(" " + target + " ")
			}
		}
		return end
	case 'P', '_', '^', 'X': // DCS, APC, PM, SOS: up to ST
		for j := i + 2; j+1 < len(b); j++ {
			if b[j] == 0x1b && b[j+1] == '\\' {
				return j + 2
			}
		}
		return len(b)
	case '(', ')', '*', '+', '-', '.', '/', '#', '%', ' ': // a designation with one more byte
		return min(i+3, len(b))
	}
	return i + 2 // ESC 7, ESC 8, ESC =, ESC >, ESC M, …
}
