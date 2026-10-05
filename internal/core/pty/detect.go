package pty

// notifier finds, in what a program prints, the ways a terminal program says it wants the
// user: a bell (BEL outside any control string) and a desktop notification (OSC 9 as
// iTerm2 and others take it, OSC 777;notify and OSC 99). It keeps no text: only the first
// few bytes of an OSC, to tell which one it is, across reads. A tab with capture off for
// privacy (a sign-in) has no notifier at all.
type notifier struct {
	st   scanState
	head [8]byte // the start of the current OSC
	n    int
}

type scanState uint8

const (
	sGround scanState = iota
	sEsc
	sCSI
	sOSC
	sOSCEsc
	sString // DCS, APC, PM, SOS: up to ST
	sStringEsc
)

// Signal is what the notifier saw.
type Signal uint8

const (
	SignalNone Signal = iota
	SignalBell
	SignalNotification
)

// feed reads p and returns the strongest signal in it.
func (d *notifier) feed(p []byte) Signal {
	sig := SignalNone
	raise := func(s Signal) {
		if s > sig {
			sig = s
		}
	}
	for _, c := range p {
		switch d.st {
		case sGround:
			switch c {
			case 0x1b:
				d.st = sEsc
			case 0x07:
				raise(SignalBell)
			}
		case sEsc:
			d.escape(c)
		case sCSI:
			switch {
			case c == 0x1b:
				d.st = sEsc
			case c >= 0x40 && c <= 0x7e:
				d.st = sGround
			}
		case sOSC:
			switch c {
			case 0x07:
				raise(d.endOSC())
			case 0x1b:
				d.st = sOSCEsc
			default:
				if d.n < len(d.head) {
					d.head[d.n] = c
				}
				d.n++
			}
		case sOSCEsc:
			if c == '\\' {
				raise(d.endOSC())
				continue
			}
			d.escape(c) // an ESC inside an OSC starts something new
		case sString:
			if c == 0x1b {
				d.st = sStringEsc
			}
		case sStringEsc:
			switch c {
			case '\\':
				d.st = sGround
			case 0x1b:
			default:
				d.st = sString
			}
		}
	}
	return sig
}

// escape handles the byte after an ESC.
func (d *notifier) escape(c byte) {
	switch c {
	case '[':
		d.st = sCSI
	case ']':
		d.st, d.n = sOSC, 0
	case 'P', '_', '^', 'X':
		d.st = sString
	case 0x1b:
		d.st = sEsc
	default:
		d.st = sGround
	}
}

// endOSC says what the OSC that just ended was.
func (d *notifier) endOSC() Signal {
	d.st = sGround
	h := string(d.head[:min(d.n, len(d.head))])
	switch {
	case len(h) >= 4 && h[:4] == "777;":
		if d.n >= 11 && h[4:] == "noti" { // 777;notify;…
			return SignalNotification
		}
	case len(h) >= 3 && h[:3] == "99;":
		return SignalNotification
	case len(h) >= 2 && h[:2] == "9;":
		// ConEmu's OSC 9;<number>;… are controls (progress, tab titles, …), not
		// notifications; iTerm2's OSC 9;<text> is one.
		rest := h[2:]
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i > 0 && (i == len(rest) && d.n == 2+i || i < len(rest) && rest[i] == ';') {
			return SignalNone
		}
		if d.n > 2 {
			return SignalNotification
		}
	}
	return SignalNone
}
