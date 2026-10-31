// Package clientapi is hopsesh's API for other programs on the same machine, run by the
// same user (a phone companion's host, an editor): what sessions there are, where, and
// moving them. It is versioned on its own, separately from the peer protocol hopsesh
// speaks to hopsesh on other machines, which stays internal.
//
// A connection carries JSON objects, one per line. The client sends requests; hopsesh
// answers each one with a response carrying the request's id, and sends events (a watched
// catalog's changes, the steps of a move) that name the request they belong to. Requests
// run concurrently, so responses can come in any order. The first request is hello.
//
// docs/client-api.md is the contract.
package clientapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// API is the version this hopsesh speaks; MinAPI the oldest it still answers.
const (
	API    = 1
	MinAPI = 1
)

// Features are what this hopsesh answers beyond API 1's required methods. A client checks
// for a feature instead of comparing versions.
const (
	FeatureCatalogWatch = "catalog.watch" // catalog changes as events
	FeaturePush         = "plan.push"     // plan and apply a move from here to another machine
	FeatureOwners       = "owners"        // reads other programs' owner records (owners/<name>/<pid>.json)
	FeatureTerminal     = "terminal.open" // runs a session in a terminal hopsesh owns (needs hopsesh serve)
)

// Methods.
const (
	MethodHello          = "hello"
	MethodCatalog        = "catalog"         // the sessions and machines, once
	MethodCatalogWatch   = "catalog.watch"   // the same, then catalog.changed events
	MethodCatalogUnwatch = "catalog.unwatch" // stops a watch
	MethodMachines       = "machines"        // the machines and what hopsesh can do on each
	MethodPlan           = "plan"            // plan a move or continuation; nothing changes
	MethodApply          = "apply"           // carry out a plan; step events, then a receipt
	MethodCancel         = "cancel"          // drop a plan that was not applied
	MethodUndo           = "undo"            // reverse an applied plan by its journal id
	MethodTerminalOpen   = "terminal.open"   // run a session here in a terminal hopsesh owns
)

// Events.
const (
	EventCatalogChanged = "catalog.changed"
	EventStep           = "apply.step"
)

// Error codes.
const (
	CodeAPI         = "api"         // no API version both ends speak
	CodeHello       = "hello"       // a request before hello
	CodeUnknown     = "unknown"     // a method this hopsesh does not know
	CodeBadRequest  = "bad-request" // the request's params could not be read
	CodeNotFound    = "not-found"   // no such session, machine, plan or journal
	CodeAmbiguous   = "ambiguous"   // several sessions match
	CodeBlocked     = "blocked"     // the plan has blockers; it cannot be applied
	CodeOwned       = "owned"       // another program runs the session; it must stop it first
	CodeStale       = "stale"       // what the plan was made from changed; plan again
	CodeNoLogin     = "nologin"     // needs hopsesh serve in the login session
	CodeUnsupported = "unsupported" // this hopsesh cannot do it here
	CodeFailed      = "failed"      // anything else
)

// Request is one call.
type Request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response answers a request: Result or Error.
type Response struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Event is something hopsesh says on its own: For is the id of the request it belongs to.
type Event struct {
	Event string          `json:"event"`
	For   json.RawMessage `json:"for,omitempty"`
	Data  any             `json:"data"`
}

// Error is a failed request. Fix, when there is one, is what the user can do about it.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
	// Journal, on a failed apply, records what it did before it failed: undo reverses it.
	Journal string `json:"journal,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Errorf makes an Error with a code.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// HelloParams is the client's hello.
type HelloParams struct {
	API    int    `json:"api"`
	MinAPI int    `json:"minApi,omitempty"`
	Client Client `json:"client"`
}

// Client names the program calling.
type Client struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// HelloResult describes this hopsesh.
type HelloResult struct {
	API      int      `json:"api"`
	MinAPI   int      `json:"minApi"`
	Version  string   `json:"version"`
	Endpoint string   `json:"endpoint,omitempty"` // this machine's persistent identity
	Machine  string   `json:"machine"`
	OS       string   `json:"os"`
	Login    bool     `json:"login"` // hopsesh serve answers (the login session)
	Features []string `json:"features"`
}

// Agree is the API both ends speak (0 when none).
func Agree(p HelloParams) int {
	lo := p.MinAPI
	if lo == 0 {
		lo = p.API
	}
	v := min(p.API, API)
	if v < lo || v < MinAPI {
		return 0
	}
	return v
}

// Conn is a connection's sending side, safe for concurrent use.
type Conn struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// Event sends an event for the request id.
func (c *Conn) Event(id json.RawMessage, name string, data any) error {
	return c.send(Event{Event: name, For: id, Data: data})
}

func (c *Conn) send(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(v)
}

// Handler answers one request. It may send events on c until it returns; a long-lived
// subscription keeps its own goroutine and stops when ctx ends.
type Handler func(ctx context.Context, c *Conn, id json.RawMessage, method string, params json.RawMessage) (any, error)

// maxLine bounds one request.
const maxLine = 1 << 20

// Serve answers requests on r until r ends or ctx is done. Before hello succeeds, only
// hello is answered.
func Serve(ctx context.Context, r io.Reader, w io.Writer, h Handler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := &Conn{enc: json.NewEncoder(w)}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		agreed int
	)
	defer wg.Wait()
	for sc.Scan() {
		var req Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil || req.Method == "" {
			_ = c.send(Response{ID: req.ID, Error: Errorf(CodeBadRequest, "not a request: one JSON object per line, with an id and a method")})
			continue
		}
		if req.Method == MethodHello {
			res, err := hello(ctx, c, req, h)
			if err == nil {
				mu.Lock()
				agreed = res
				mu.Unlock()
			}
			continue
		}
		mu.Lock()
		ok := agreed > 0
		mu.Unlock()
		if !ok {
			_ = c.send(Response{ID: req.ID, Error: Errorf(CodeHello, "say hello first")})
			continue
		}
		wg.Add(1)
		go func(req Request) {
			defer wg.Done()
			res, err := h(ctx, c, req.ID, req.Method, req.Params)
			_ = c.send(response(req.ID, res, err))
		}(req)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// hello answers hello synchronously, so the requests after it see the agreed API.
func hello(ctx context.Context, c *Conn, req Request, h Handler) (int, error) {
	var p HelloParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		e := Errorf(CodeBadRequest, "hello: %v", err)
		_ = c.send(Response{ID: req.ID, Error: e})
		return 0, e
	}
	v := Agree(p)
	if v == 0 {
		e := Errorf(CodeAPI, "this hopsesh speaks client API %d to %d; the client asked for %d", MinAPI, API, p.API)
		e.Fix = "update hopsesh, or the program calling it"
		_ = c.send(Response{ID: req.ID, Error: e})
		return 0, e
	}
	res, err := h(ctx, c, req.ID, MethodHello, req.Params)
	_ = c.send(response(req.ID, res, err))
	return v, err
}

func response(id json.RawMessage, res any, err error) Response {
	if err == nil {
		if res == nil {
			res = struct{}{}
		}
		return Response{ID: id, Result: res}
	}
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: CodeFailed, Message: err.Error()}
	}
	return Response{ID: id, Error: e}
}
