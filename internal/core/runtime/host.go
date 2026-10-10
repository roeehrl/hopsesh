package runtime

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/observe"
	"github.com/roeehrl/hopsesh/internal/localstate"
)

const requestLimit = 32 << 20
const responseLimit = 32 << 20

type Status struct {
	Protocol  int       `json:"protocol"`
	Namespace string    `json:"namespace"`
	Epoch     string    `json:"epoch"`
	Mode      string    `json:"mode"`
	Version   string    `json:"version"`
	PID       int       `json:"pid"`
	Started   time.Time `json:"started"`
}
type Request struct {
	Protocol  int             `json:"protocol"`
	Namespace string          `json:"namespace"`
	Epoch     string          `json:"epoch"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
}
type Response struct {
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}
type Handler func(context.Context, string, json.RawMessage) (any, error)

// Host is the sole process owner. StopGuard prevents stopping while irreversible
// operations or GUI-owned PTYs still need the owner. It is never called remotely.
type Host struct {
	Namespace Namespace
	Engine    *observe.Engine
	Handler   Handler
	StopGuard func() error
	status    Status
	cancel    context.CancelFunc
	done      chan struct{}
	once      sync.Once
	tasks     []func(context.Context)
	listener  net.Listener
	lock      *os.File
}

func Start(ctx context.Context, n Namespace, e *observe.Engine, mode, version string, h Handler, guard func() error, tasks ...func(context.Context)) (*Host, error) {
	if e == nil {
		return nil, errors.New("runtime needs an observation engine")
	}
	if mode != "desktop" && mode != "headless" {
		return nil, errors.New("runtime mode must be desktop or headless")
	}
	if err := n.prepare(); err != nil {
		return nil, err
	}
	lock, err := localstate.TryLock(filepath.Join(n.Directory, "owner.lock"))
	if errors.Is(err, localstate.ErrBusy) {
		return nil, ErrOwned
	}
	if err != nil {
		return nil, err
	}
	l, err := listen(n)
	if err != nil {
		lock.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	host := &Host{tasks: tasks, Namespace: n, Engine: e, Handler: h, StopGuard: guard, listener: l, lock: lock, cancel: cancel, done: make(chan struct{}), status: Status{Protocol: Protocol, Namespace: n.ID, Epoch: e.Latest().Epoch, Mode: mode, Version: version, PID: os.Getpid(), Started: time.Now().UTC()}}
	go host.run(ctx)
	return host, nil
}
func (h *Host) Status() Status        { return h.status }
func (h *Host) Close()                { h.once.Do(h.cancel); <-h.done }
func (h *Host) Done() <-chan struct{} { return h.done }
func (h *Host) run(ctx context.Context) {
	defer close(h.done)
	defer h.lock.Close()
	var wg sync.WaitGroup
	wg.Go(func() { _ = h.Engine.Run(ctx) })
	for _, task := range h.tasks {
		wg.Go(func() { task(ctx) })
	}
	wg.Go(func() { <-ctx.Done(); h.listener.Close() })
	slots := make(chan struct{}, 32)
	for {
		c, err := h.listener.Accept()
		if err != nil {
			break
		}
		select {
		case slots <- struct{}{}:
			wg.Go(func() { defer func() { <-slots }(); h.serve(ctx, c) })
		default:
			c.Close()
		}
	}
	h.cancel()
	wg.Wait()
}
func (h *Host) serve(ctx context.Context, c net.Conn) {
	defer c.Close()
	if err := sameUser(c); err != nil {
		return
	}
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			c.Close()
		case <-stopped:
		}
	}()
	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return
	}
	if write(c, h.status, responseLimit) != nil {
		return
	}
	var req Request
	if err := read(c, &req, requestLimit); err != nil {
		return
	}
	reply := func(value any, err error) error {
		var r Response
		if err != nil {
			r.Error = err.Error()
		} else {
			r.Data, err = json.Marshal(value)
			if err != nil {
				r.Error = err.Error()
			}
		}
		if err := c.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		return write(c, r, responseLimit)
	}
	if req.Protocol != Protocol || req.Namespace != h.Namespace.ID || req.Epoch != h.status.Epoch {
		_ = reply(nil, errors.New("runtime protocol, namespace or incarnation changed; reconnect"))
		return
	}
	if err := c.SetReadDeadline(time.Time{}); err != nil {
		return
	}
	switch req.Method {
	case "status":
		_ = reply(h.status, nil)
	case "snapshot":
		_ = reply(h.Engine.Latest(), nil)
	case "metrics":
		_ = reply(h.Engine.Metrics(), nil)
	case "resources":
		_ = reply(readResources(), nil)
	case "pause", "resume":
		h.Engine.Pause(req.Method == "pause")
		_ = reply(h.Engine.Latest(), nil)
	case "refresh":
		h.Engine.Notify()
		_ = reply(h.Engine.Latest(), nil)
	case "watch":
		updates, cancel := h.Engine.Subscribe()
		defer cancel()
		// A disconnected watcher cancels without waiting for another filesystem event.
		disconnected := make(chan struct{})
		go func() { var b [1]byte; _, _ = c.Read(b[:]); close(disconnected) }()
		for {
			select {
			case <-ctx.Done():
				return
			case <-disconnected:
				return
			case s, ok := <-updates:
				if !ok || reply(s, nil) != nil {
					return
				}
			}
		}
	case "stop":
		if h.StopGuard == nil {
			_ = reply(nil, errors.New("runtime owner does not permit IPC shutdown"))
			return
		}
		if err := h.StopGuard(); err != nil {
			_ = reply(nil, err)
			return
		}
		if reply(true, nil) == nil {
			h.cancel()
		}
	default:
		if h.Handler == nil {
			_ = reply(nil, fmt.Errorf("unknown runtime method %q", req.Method))
			return
		}
		data, err := h.Handler(ctx, req.Method, req.Params)
		_ = reply(data, err)
	}
}
func write(w io.Writer, v any, limit uint32) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > int(limit) {
		return errors.New("runtime frame exceeds size limit")
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	if count, e := w.Write(n[:]); e != nil || count != len(n) {
		err = e
		if err == nil {
			err = io.ErrShortWrite
		}
		return err
	}
	count, err := w.Write(b)
	if err == nil && count != len(b) {
		err = io.ErrShortWrite
	}
	return err
}
func read(r io.Reader, v any, limit uint32) error {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(n[:])
	if size == 0 || size > limit {
		return errors.New("runtime frame exceeds size limit")
	}
	b := make([]byte, size)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

type Client struct{ Namespace Namespace }

func (c Client) connect(ctx context.Context, method string, params any) (net.Conn, func(), error) {
	conn, err := dial(ctx, c.Namespace)
	if err != nil {
		return nil, nil, err
	}
	cancel := context.AfterFunc(ctx, func() { conn.Close() })
	cleanup := func() { cancel(); conn.Close() }
	if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		cleanup()
		return nil, nil, err
	}
	var status Status
	if err = read(conn, &status, responseLimit); err == nil && (status.Protocol != Protocol || status.Namespace != c.Namespace.ID) {
		err = errors.New("incompatible runtime protocol or settings/state namespace")
	}
	if err == nil {
		var p []byte
		p, err = json.Marshal(params)
		if err == nil {
			err = write(conn, Request{Protocol: Protocol, Namespace: c.Namespace.ID, Epoch: status.Epoch, Method: method, Params: p}, requestLimit)
		}
	}
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		cleanup()
		return nil, nil, err
	}
	return conn, cleanup, nil
}
func (c Client) Call(ctx context.Context, method string, params, out any) error {
	conn, close, err := c.connect(ctx, method, params)
	if err != nil {
		return err
	}
	defer close()
	timeout := 30 * time.Second
	if method == "relay.call" {
		timeout = 30 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetReadDeadline(deadline); err != nil {
		return err
	}
	var r Response
	if err = read(conn, &r, responseLimit); err != nil {
		return err
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(r.Data, out)
}
func (c Client) Watch(ctx context.Context, receive func(observe.Snapshot) error) error {
	conn, close, err := c.connect(ctx, "watch", nil)
	if err != nil {
		return err
	}
	defer close()
	for {
		var r Response
		if err = read(conn, &r, responseLimit); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if r.Error != "" {
			return errors.New(r.Error)
		}
		var s observe.Snapshot
		if err = json.Unmarshal(r.Data, &s); err != nil {
			return err
		}
		if err = receive(s); err != nil {
			return err
		}
	}
}
