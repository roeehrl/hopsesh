// Package peer is how hopsesh on two machines work together over one SSH connection:
// `hopsesh peer --stdio` on the other machine answers requests, one JSON object per line
// each way. Both ends must speak the same Protocol; a mismatch is refused, never bridged.
package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/internal/core/move"
	"github.com/roeehrl/hopsesh/internal/core/repos"
	"github.com/roeehrl/hopsesh/internal/version"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// Protocol is the version of the messages below. Any change to them changes it, and
// FirstVersion with it.
//
//   - 1: hopsesh 0.2 and 0.3.
//   - 2: pre-release lineage/2 manifests and cloud locations.
//   - 3: pre-release causal lineage/3, persistent endpoints and stable transfer IDs.
//   - 4: scoped account profiles and binding-aware lineage/4.
//   - 5: causal movement notices and lineage/5.
const Protocol = 5

// FirstVersion is the first hopsesh release that speaks Protocol: the version to update an
// older machine to.
const FirstVersion = "0.4.0"

// Methods.
const (
	MethodHello = "hello"
	MethodPlan  = "plan"  // plan receiving a pushed session (nothing changes)
	MethodApply = "apply" // carry out the plan made on this connection
	MethodUndo  = "undo"
)

// Error codes.
const (
	CodeProtocol = "protocol" // the two ends speak different protocols
	CodeRefused  = "refused"  // the machine does not receive sessions
	CodeFailed   = "failed"
)

// Error is a failure the other end reported. A protocol mismatch also says which protocol
// and hopsesh the answering end has (hopsesh 0.3 and older sent neither).
type Error struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Protocol int    `json:"protocol,omitempty"`
	Version  string `json:"version,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// ErrProtocol and ErrRefused classify an Error with errors.Is.
var (
	ErrProtocol = errors.New("hopsesh on the two machines speak different protocols")
	ErrRefused  = errors.New("that machine does not receive sessions")
)

// Is maps codes to the sentinel errors.
func (e *Error) Is(target error) bool {
	return target == ErrProtocol && e.Code == CodeProtocol || target == ErrRefused && e.Code == CodeRefused
}

// Refused is the error a peer returns when receiving is off.
func Refused(machine string) error {
	return &Error{Code: CodeRefused, Message: machine + " does not receive sessions; turn it on there with [peer] receive = true in hopsesh's configuration"}
}

// Hello opens a conversation.
type Hello struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"` // the sender's hopsesh
	From     string `json:"from"`    // the sender's machine name
}

// HelloReply describes the peer.
type HelloReply struct {
	Protocol int         `json:"protocol"`
	Version  string      `json:"version"`
	Machine  string      `json:"machine"`
	OS       string      `json:"os"`
	Receive  bool        `json:"receive"` // it accepts pushed sessions
	Agents   []AgentInfo `json:"agents"`
}

// AgentInfo is an agent on the peer.
type AgentInfo struct {
	ID      agent.ID `json:"id"`
	Name    string   `json:"name"`
	Version string   `json:"version,omitempty"`
	Present bool     `json:"present"` // has its data folder there
	Write   bool     `json:"write"`   // hopsesh can write its sessions (continue into it)
}

// Package is one session sent to a peer: its files and what the sender knows about it.
// Nothing else of the sender's machine is reachable from the peer.
type Package struct {
	Location string              `json:"location"` // the sender's machine name
	Facts    host.Facts          `json:"facts"`
	Agent    agent.ID            `json:"agent"`
	Install  agent.Install       `json:"install"`
	Session  agent.Summary       `json:"session"`
	Live     agent.LiveInfo      `json:"live"`
	Git      *repos.GitState     `json:"git,omitempty"`
	GitError string              `json:"gitError,omitempty"`
	Lineage  *lineage.Manifest   `json:"lineage,omitempty"`
	Account  *agent.Account      `json:"account,omitempty"`
	Files    []host.SnapshotFile `json:"files"`
}

// PlanRequest asks a peer to plan receiving a package, in its own agent or another.
type PlanRequest struct {
	Package Package      `json:"package"`
	Target  agent.ID     `json:"target,omitempty"`
	Options move.Options `json:"options"`
}

// PlanReply is the peer's plan.
type PlanReply struct {
	Plan *move.Plan `json:"plan"`
}

// ApplyReply is what the peer did, and what the sender must do on its side: the writes
// meant for its copy (its lineage).
type ApplyReply struct {
	Result  *move.Result         `json:"result"`
	Writes  []host.SnapshotWrite `json:"writes,omitempty"`
	Journal string               `json:"journal"` // the peer's undo id
}

// UndoRequest asks a peer to undo one of its journals.
type UndoRequest struct {
	Journal string `json:"journal"`
	Force   bool   `json:"force,omitempty"` // undo even what was used since
}

// message is one line on the wire: a request (Method set) or a reply (to ID).
type message struct {
	ID     int             `json:"id"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Client calls a peer, one request at a time.
type Client struct {
	mu     sync.Mutex
	enc    *json.Encoder
	dec    *json.Decoder
	next   int
	broken error // a call was abandoned mid-way: replies no longer line up
}

// NewClient talks to a peer over r (its output) and w (its input).
func NewClient(r io.Reader, w io.Writer) *Client {
	return &Client{enc: newEncoder(w), dec: json.NewDecoder(r)}
}

// newEncoder writes one JSON value per line in ASCII only: anything else is escaped
// (\uXXXX), so a Windows shell between the two ends cannot alter it.
func newEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(asciiWriter{w})
	enc.SetEscapeHTML(false)
	return enc
}

// asciiWriter escapes non-ASCII characters of JSON text (they occur only inside strings,
// where \uXXXX means the same).
type asciiWriter struct{ w io.Writer }

func (a asciiWriter) Write(p []byte) (int, error) {
	if !hasNonASCII(p) {
		return a.w.Write(p)
	}
	var b strings.Builder
	b.Grow(len(p) + 64)
	for _, r := range string(p) {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r > 0xFFFF:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&b, "\\u%04x\\u%04x", r1, r2)
		default:
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	if _, err := io.WriteString(a.w, b.String()); err != nil {
		return 0, err
	}
	return len(p), nil
}

func hasNonASCII(p []byte) bool {
	for _, c := range p {
		if c >= 0x80 {
			return true
		}
	}
	return false
}

// Call sends a request and decodes the reply into result (nil to ignore it).
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken != nil {
		return c.broken
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.next++
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	type reply struct {
		m   message
		err error
	}
	// Read while writing: the peer may answer before it has consumed all of the request
	// (its decoder stops at the end of the JSON value), and neither side may wait on the
	// other's write.
	done := make(chan reply, 1)
	go func() {
		var m message
		err := c.dec.Decode(&m)
		done <- reply{m, err}
	}()
	if err := c.enc.Encode(message{ID: c.next, Method: method, Params: p}); err != nil {
		return fmt.Errorf("sending to hopsesh on the other machine: %w", err)
	}
	var r reply
	select {
	case r = <-done:
	case <-ctx.Done():
		c.broken = fmt.Errorf("the connection to hopsesh on the other machine was abandoned: %w", ctx.Err())
		return ctx.Err()
	}
	if r.err != nil {
		c.broken = fmt.Errorf("hopsesh on the other machine stopped answering: %w", r.err)
		return c.broken
	}
	if r.m.ID != c.next {
		return fmt.Errorf("hopsesh on the other machine answered request %d, not %d", r.m.ID, c.next)
	}
	if r.m.Error != nil {
		return r.m.Error
	}
	if result != nil {
		return json.Unmarshal(r.m.Result, result)
	}
	return nil
}

// Handler answers one request.
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// refuseHello is the answer to a hello in another protocol. Its message is read on the
// machine that said hello, where hopsesh 0.3 prints it as it is followed by " (on
// <machine>)", so it speaks from there and ends with the other machine.
func refuseHello(hi Hello) *Error {
	sender := hi.Version
	if sender == "" {
		sender = "unknown version"
	}
	e := &Error{Code: CodeProtocol, Protocol: Protocol, Version: version.Version}
	if hi.Protocol < Protocol {
		e.Message = fmt.Sprintf("this machine runs an older hopsesh (%s, peer protocol %d): update it to %s or later to work with the newer hopsesh (%s, protocol %d) on the other machine",
			sender, hi.Protocol, FirstVersion, version.Version, Protocol)
	} else {
		e.Message = fmt.Sprintf("this machine runs a newer hopsesh (%s, peer protocol %d): update hopsesh to %s or later on the other machine, which runs an older one (%s, protocol %d)",
			sender, hi.Protocol, sender, version.Version, Protocol)
	}
	return e
}

// Mismatch explains, on the machine that said hello, a refusal for another protocol from
// hopsesh on machine: which of the two is older and what to update. A hopsesh 0.3 or older
// sends only its message, which names its protocol; any other error comes back as it is.
func Mismatch(err error, machine string) error {
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != CodeProtocol {
		return err
	}
	theirs := pe.Protocol
	if theirs == 0 {
		if _, serr := fmt.Sscanf(pe.Message, "hopsesh here speaks protocol %d,", &theirs); serr != nil {
			return fmt.Errorf("%w (on %s)", err, machine)
		}
	}
	there := "peer protocol " + strconv.Itoa(theirs)
	if pe.Version != "" {
		there = pe.Version + ", " + there
	}
	out := &Error{Code: CodeProtocol, Protocol: theirs, Version: pe.Version}
	switch {
	case theirs < Protocol:
		out.Message = fmt.Sprintf("%s runs an older hopsesh (%s) than this one (%s, peer protocol %d): update hopsesh on %s to %s or later",
			machine, there, version.Version, Protocol, machine, FirstVersion)
	case theirs > Protocol:
		newer := "the version it runs"
		if pe.Version != "" {
			newer = pe.Version
		}
		out.Message = fmt.Sprintf("%s runs a newer hopsesh (%s) than this one (%s, peer protocol %d): update hopsesh on this machine to %s or later",
			machine, there, version.Version, Protocol, newer)
	default:
		return fmt.Errorf("%w (on %s)", err, machine)
	}
	return out
}

// Serve answers requests from r on w until r ends. The first request must be a hello with
// this Protocol; any other is refused.
func Serve(ctx context.Context, r io.Reader, w io.Writer, h Handler) error {
	dec := json.NewDecoder(r)
	enc := newEncoder(w)
	greeted := false
	for {
		var m message
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		reply := message{ID: m.ID}
		var res any
		var err error
		switch {
		case !greeted && m.Method != MethodHello:
			err = &Error{Code: CodeProtocol, Message: "say hello first"}
		case m.Method == MethodHello:
			var hi Hello
			if err = json.Unmarshal(m.Params, &hi); err == nil && hi.Protocol != Protocol {
				err = refuseHello(hi)
			}
			if err == nil {
				greeted = true
				res, err = h(ctx, m.Method, m.Params)
			}
		default:
			res, err = h(ctx, m.Method, m.Params)
		}
		if err != nil {
			var pe *Error
			if !errors.As(err, &pe) {
				pe = &Error{Code: CodeFailed, Message: err.Error()}
			}
			reply.Error = pe
		} else if res != nil {
			if reply.Result, err = json.Marshal(res); err != nil {
				reply.Error = &Error{Code: CodeFailed, Message: err.Error()}
			}
		}
		if err := enc.Encode(reply); err != nil {
			return err
		}
	}
}
