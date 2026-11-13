package opencode

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// OpenCode's ids (schema/identifier.ts): a prefix, then 12 hex digits of the low 48 bits
// of ms·4096 + a counter within the millisecond (bitwise NOT for session ids, so newer
// sessions sort first; as is for message and call ids, so they sort in order), then 14
// random base62 characters. Import only checks the prefix.

const (
	idTail = 14
	base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

var (
	idMu      sync.Mutex
	idLast    int64
	idCounter int64
)

// newID is an id with prefix ("ses_", "msg_", "call_") at t.
func newID(prefix string, descending bool, t time.Time) string {
	ms := t.UnixMilli()
	idMu.Lock()
	if ms != idLast {
		idLast, idCounter = ms, 0
	}
	idCounter++
	n := idCounter
	idMu.Unlock()
	return prefix + idTime(ms, n, descending) + randomTail()
}

// idTime is the 12 hex digits of an id made at ms with the millisecond's counter n.
func idTime(ms, n int64, descending bool) string {
	v := uint64(ms)*0x1000 + uint64(n)
	if descending {
		v = ^v
	}
	var b [6]byte
	for i := range b {
		b[i] = byte(v >> (40 - 8*i))
	}
	return hex.EncodeToString(b[:])
}

func randomTail() string {
	var b [idTail]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported systems
	}
	for i := range b {
		b[i] = base62[int(b[i])%len(base62)]
	}
	return string(b[:])
}

// idOrder is the 48-bit time value an id carries (ms·4096 + counter, mod 2^48), as is for
// either direction: ids of one kind made later have a larger value. ok is false for an id
// that is not in OpenCode's form.
func idOrder(sid string) (uint64, bool) {
	prefix, rest, found := strings.Cut(sid, "_")
	if !found || len(rest) < 12 {
		return 0, false
	}
	b, err := hex.DecodeString(rest[:12])
	if err != nil {
		return 0, false
	}
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	if prefix == "ses" {
		v = ^v & (1<<48 - 1)
	}
	return v, true
}

// NewSessionID mints a session id in OpenCode's form, for a copy kept beside the
// original or a session written from another agent.
func (*Module) NewSessionID(_ agent.Install, now time.Time) agent.SessionID {
	return agent.SessionID(newID("ses_", true, now))
}
