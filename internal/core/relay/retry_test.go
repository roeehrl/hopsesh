package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type retryRoundTrip func(*http.Request) (*http.Response, error)

func (f retryRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func retryResponse(status int, after, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {after}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestRetryAfterSanitizedBoundedAndTyped(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, test := range []struct {
		header string
		want   time.Duration
	}{
		{"120", 2 * time.Minute},
		{now.Add(time.Minute).Format(http.TimeFormat), time.Minute},
		{"4294967295", MaxLifetime},
		{"0", 0}, {"-2", 0}, {"secret-header", 0},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	} {
		err := withRetryAfter(ErrTrafficBudget, test.header, now)
		if !errors.Is(err, ErrTrafficBudget) || err.Error() != ErrTrafficBudget.Error() {
			t.Fatal("retry wrapper changed error identity or leaked a header", err)
		}
		var retry *retryAfterError
		if errors.As(err, &retry) {
			if got := retry.until.Sub(now); got != test.want {
				t.Fatal("wrong retry deadline", got, test.want)
			}
		} else if test.want != 0 {
			t.Fatal("server delay discarded")
		}
	}
}

func TestSimultaneousRelayFailuresRespectServerDelayAndSpreadRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const clients = 64
		starts := time.Now()
		var wg sync.WaitGroup
		retries := make(chan time.Duration, clients)
		for range clients {
			wg.Go(func() {
				ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
				defer cancel()
				polls := 0
				transport := Transport{Base: DefaultOrigin, Token: "fixture", Space: "storm-test-space", HTTP: &http.Client{Transport: retryRoundTrip(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/v1/notifications" {
						return retryResponse(503, "1", ""), nil
					}
					polls++
					if polls == 1 {
						return retryResponse(429, "10", `{}`), nil
					}
					retries <- time.Since(starts)
					return retryResponse(200, "", `{"messages":[],"cursor":0}`), nil
				})}}
				err := (Listener{Transport: transport, Notify: func(err error) {
					if err == nil {
						cancel()
					}
				}}).Run(ctx)
				if !errors.Is(err, context.Canceled) || polls != 2 {
					t.Errorf("retry did not stop and join after recovery: %v, %d polls", err, polls)
				}
			})
		}
		wg.Wait()
		close(retries)
		buckets := map[time.Duration]bool{}
		count := 0
		for d := range retries {
			count++
			if d < 10*time.Second || d >= 11*time.Second {
				t.Fatal("notification state bypassed server delay or backoff escaped bounds", d)
			}
			buckets[d.Truncate(time.Millisecond)] = true
		}
		if count != clients || len(buckets) < 8 {
			t.Fatal("clients did not recover across distributed retry times", count, len(buckets))
		}
	})
}

func TestHealthyIdleDoesNotAccumulateFailureBackoff(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				idleUntil := time.Now().Add(2 * time.Minute)
				var failure time.Time
				var recovery time.Duration
				polls, retries := 0, 0
				transport := Transport{Base: DefaultOrigin, Token: "fixture", Space: "idle-recovery-space", HTTP: &http.Client{Transport: retryRoundTrip(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/v1/notifications" {
						return retryResponse(501, "", ""), nil
					}
					polls++
					if time.Now().Before(idleUntil) {
						return retryResponse(200, "", `{"messages":[],"cursor":0}`), nil
					}
					if failure.IsZero() {
						failure = time.Now()
						after := ""
						if status == http.StatusTooManyRequests {
							after = "10"
						}
						return retryResponse(status, after, ""), nil
					}
					retries++
					recovery = time.Since(failure)
					cancel()
					return retryResponse(200, "", `{"messages":[],"cursor":0}`), nil
				})}}
				err := (Listener{Transport: transport}).Run(ctx)
				if !errors.Is(err, context.Canceled) || polls < 5 || retries != 1 {
					t.Fatalf("idle recovery did not finish: polls=%d retries=%d error=%v", polls, retries, err)
				}
				floor := 2 * time.Second
				if status == http.StatusTooManyRequests {
					floor = 10 * time.Second
				}
				if recovery < floor || recovery >= floor+time.Second {
					t.Fatalf("healthy idle changed first-failure backoff: recovery=%s want=[%s,%s)", recovery, floor, floor+time.Second)
				}
			})
		})
	}
}

func TestClaimRetryPreservesProofAndStopsAtIncarnationLease(t *testing.T) {
	for _, mode := range []string{"lost-reply", "lease", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				owner, ticket, leaf, scope := signedAdmission(t, DefaultOrigin)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if mode == "lease" {
					scope.Expires = time.Now().Add(3 * time.Second).Unix()
				}
				var original string
				calls := 0
				client := &http.Client{Transport: retryRoundTrip(func(r *http.Request) (*http.Response, error) {
					calls++
					body, _ := io.ReadAll(r.Body)
					if calls == 1 {
						original = string(body)
						if mode == "cancel" {
							cancel()
						}
						if mode != "lost-reply" {
							return retryResponse(503, "120", "proxy temporarily unavailable"), nil
						}
						return nil, io.ErrUnexpectedEOF
					}
					if string(body) != original {
						t.Error("lost reply retry changed signed claim or generated new keys")
					}
					out, _ := json.Marshal(map[string]any{"space": "retry-space-1234", "device": leaf.Public.ID, "token": "same-issued-token", "expires": scope.Expires, "kind": "cloud-session", "provisional": true})
					return retryResponse(200, "", string(out)), nil
				})}
				connection, err := ClaimAdmission(ctx, ticket, owner.Public.ID, leaf, scope, client)
				switch mode {
				case "lost-reply":
					if err != nil || calls != 2 || connection.Token != "same-issued-token" {
						t.Fatal("lost reply did not recover the original credential", calls, err)
					}
				case "lease":
					if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
						t.Fatal("claim continued past incarnation lease", calls, err)
					}
				case "cancel":
					if !errors.Is(err, context.Canceled) || calls != 1 {
						t.Fatal("canceled claim retried", calls, err)
					}
				}
			})
		})
	}
}
