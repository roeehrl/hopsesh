//go:build darwin && cgo

package lnp

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Network
#include <Network/Network.h>
#include <dispatch/dispatch.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <errno.h>

enum { HS_WAITING_DENIED = 1, HS_WAITING_OTHER = 2, HS_READY = 3, HS_FAILED = 4 };

typedef struct {
	_Atomic int state;
	_Atomic int err;               // POSIX error code of the last waiting/failed state
	dispatch_semaphore_t settled;  // signalled on ready or failed
	nw_connection_t conn;
} hs_probe;

// hs_probe_start starts a TCP connection to host:port and returns a handle. Its state
// changes are recorded in the handle; hs_probe_finish cancels and later frees it.
static hs_probe *hs_probe_start(const char *host, const char *port) {
	hs_probe *p = calloc(1, sizeof(hs_probe));
	p->settled = dispatch_semaphore_create(0);
	nw_endpoint_t ep = nw_endpoint_create_host(host, port);
	nw_parameters_t params = nw_parameters_create_secure_tcp(NW_PARAMETERS_DISABLE_PROTOCOL,
		NW_PARAMETERS_DEFAULT_CONFIGURATION);
	p->conn = nw_connection_create(ep, params);
	nw_release(ep);
	nw_release(params);
	dispatch_queue_t q = dispatch_queue_create("io.github.roeehrl.hopsesh.lnp", DISPATCH_QUEUE_SERIAL);
	nw_connection_set_queue(p->conn, q);
	dispatch_release(q);
	nw_connection_set_state_changed_handler(p->conn, ^(nw_connection_state_t st, nw_error_t err) {
		switch (st) {
		case nw_connection_state_waiting: {
			int s = HS_WAITING_OTHER;
			if (__builtin_available(macOS 14.0, *)) {
				nw_path_t path = nw_connection_copy_current_path(p->conn);
				if (path) {
					if (nw_path_get_unsatisfied_reason(path) == nw_path_unsatisfied_reason_local_network_denied)
						s = HS_WAITING_DENIED;
					nw_release(path);
				}
			}
			if (err && nw_error_get_error_domain(err) == nw_error_domain_posix)
				atomic_store(&p->err, nw_error_get_error_code(err));
			atomic_store(&p->state, s);
			break;
		}
		case nw_connection_state_ready:
			atomic_store(&p->state, HS_READY);
			dispatch_semaphore_signal(p->settled);
			break;
		case nw_connection_state_failed:
			if (err && nw_error_get_error_domain(err) == nw_error_domain_posix)
				atomic_store(&p->err, nw_error_get_error_code(err));
			atomic_store(&p->state, HS_FAILED);
			dispatch_semaphore_signal(p->settled);
			break;
		case nw_connection_state_cancelled:
			// Last callback: free everything. The Go side is done with the handle.
			nw_release(p->conn);
			dispatch_release(p->settled);
			free(p);
			break;
		default:
			break;
		}
	});
	nw_connection_start(p->conn);
	return p;
}

// hs_probe_wait waits up to ms for the connection to become ready or fail, and returns
// the last state seen.
static int hs_probe_wait(hs_probe *p, long ms) {
	dispatch_semaphore_wait(p->settled, dispatch_time(DISPATCH_TIME_NOW, ms * NSEC_PER_MSEC));
	return atomic_load(&p->state);
}

static int hs_probe_err(hs_probe *p) { return atomic_load(&p->err); }

static void hs_probe_finish(hs_probe *p) { nw_connection_cancel(p->conn); }
*/
import "C"

import (
	"context"
	"strconv"
	"time"
	"unsafe"
)

// CanProbe reports whether this build can run Probe (macOS builds with cgo).
const CanProbe = true

func probe(ctx context.Context, host string, port int, wait time.Duration) State {
	ch := C.CString(host)
	cp := C.CString(strconv.Itoa(port))
	defer C.free(unsafe.Pointer(ch))
	defer C.free(unsafe.Pointer(cp))
	p := C.hs_probe_start(ch, cp)
	defer C.hs_probe_finish(p)
	// Wait the full time only while the connection is held back for local network access:
	// that is the state while the macOS prompt is open (TN3179). Anything else (a .local
	// name that does not answer, a slow DNS lookup, a firewall) is not about permission,
	// so give up on it quickly and let ssh report the real problem.
	start := time.Now()
	deadline := start.Add(wait)
	const otherGrace = 4 * time.Second
	var st C.int
	for {
		st = C.hs_probe_wait(p, C.long(250))
		// A refused or reset connection still proves packets reach the machine, so
		// access is allowed even though nothing listens on that port.
		if e := C.hs_probe_err(p); e == C.ECONNREFUSED || e == C.ECONNRESET {
			return Allowed
		}
		if st == C.HS_READY || st == C.HS_FAILED || time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		if st != C.HS_WAITING_DENIED && time.Since(start) > otherGrace {
			break
		}
	}
	switch st {
	case C.HS_READY:
		return Allowed
	case C.HS_WAITING_DENIED:
		return Denied
	}
	return Unknown
}
