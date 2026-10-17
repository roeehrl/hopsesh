package relay

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryAfterError retains only a parsed deadline, never proxy headers or bodies.
type retryAfterError struct {
	error
	until time.Time
}

func (e *retryAfterError) Unwrap() error { return e.error }

func withRetryAfter(err error, header string, now time.Time) error {
	header = strings.TrimSpace(header)
	var until time.Time
	if seconds, parseErr := strconv.ParseUint(header, 10, 32); parseErr == nil {
		until = now.Add(time.Duration(seconds) * time.Second)
	} else if date, parseErr := http.ParseTime(header); parseErr == nil {
		until = date
	}
	if !until.After(now) {
		return err
	}
	// A routing lease cannot exceed 24 hours. Longer server delays outlive the
	// operation; callers still cancel at the original lease/login deadline.
	until = minTime(until, now.Add(MaxLifetime))
	return &retryAfterError{error: err, until: until}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func retryFloor(err error) time.Duration {
	var retry *retryAfterError
	if errors.As(err, &retry) {
		return max(0, time.Until(retry.until))
	}
	return 0
}

// Add jitter above the existing backoff, never below it. Healthy reconciliation
// does not use this function, so it gains neither polling nor extra wakeups.
// Even a common Retry-After deadline is spread over the backoff's jitter window.
func retryDelay(base time.Duration, err error) time.Duration {
	return max(base, retryFloor(err)) + time.Duration(rand.Int64N(max(1, int64(base/2))))
}
