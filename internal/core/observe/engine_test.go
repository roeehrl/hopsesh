package observe

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func running(t *testing.T, collect Collector) (*Engine, func()) {
	t.Helper()
	e, err := New(Defaults(), collect)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	return e, func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}

func TestSubscribersShareCollectionAndCannotRenewFreshness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		e, stop := running(t, func(context.Context) (json.RawMessage, error) { calls.Add(1); return json.RawMessage(`{"v":1}`), nil })
		defer stop()
		synctest.Wait()
		original := e.Latest()
		for range 20 {
			ch, cancel := e.Subscribe()
			replay := <-ch
			replay.Data[0] = 'X'
			cancel()
		}
		if calls.Load() != 1 || string(e.Latest().Data) != `{"v":1}` {
			t.Fatal("subscriptions collected or mutated shared data")
		}
		metrics := e.Metrics()
		if metrics.Collections != 1 || metrics.Subscribers != 0 || metrics.Notifications != 0 {
			t.Fatal("subscribers or diagnostics triggered collection", metrics)
		}
		time.Sleep(10 * time.Second)
		ch, cancel := e.Subscribe()
		defer cancel()
		replay := <-ch
		if !replay.ObservedAt.Equal(original.ObservedAt) || !replay.ExpiresAt.Equal(original.ExpiresAt) {
			t.Fatal("replay renewed freshness")
		}
	})
}

func TestForegroundRefreshCannotAcceptAnOlderInFlightAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		e, stop := running(t, func(ctx context.Context) (json.RawMessage, error) {
			if calls.Add(1) == 1 {
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return json.RawMessage(`{"old":true}`), nil
			}
			return json.RawMessage(`{"new":true}`), nil
		})
		defer stop()
		synctest.Wait()
		// The first attempt and request deliberately share a clock timestamp.
		results := make(chan Snapshot, 20)
		for range 20 {
			go func() {
				s, err := e.Refresh(t.Context())
				if err != nil {
					t.Error(err)
				}
				results <- s
			}()
		}
		synctest.Wait()
		close(release)
		synctest.Wait()
		if len(results) != 0 {
			t.Fatal("older in-flight collection satisfied explicit freshness")
		}
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if len(results) != 20 || calls.Load() != 2 {
			t.Fatal("refresh clients did not share one subsequent attempt", len(results), calls.Load())
		}
		for range 20 {
			if string((<-results).Data) != `{"new":true}` {
				t.Fatal("cached evidence returned")
			}
		}
		if e.Metrics().Subscribers != 0 {
			t.Fatal("refresh subscription leaked")
		}
	})
}

func TestForegroundRefreshReportsFailurePauseAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, stop := running(t, func(context.Context) (json.RawMessage, error) { return nil, errors.New("private source error") })
		defer stop()
		synctest.Wait()
		result := make(chan error, 1)
		go func() { _, err := e.Refresh(t.Context()); result <- err }()
		synctest.Wait()
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if err := <-result; err == nil || err.Error() != "fresh observation failed" {
			t.Fatal("failed explicit attempt reused cached success", err)
		}
		e.Pause(true)
		if _, err := e.Refresh(t.Context()); err == nil {
			t.Fatal("paused owner claimed fresh evidence")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := e.Refresh(ctx); err == nil {
			t.Fatal("cancelled refresh succeeded")
		}
		if e.Metrics().Subscribers != 0 {
			t.Fatal("cancelled subscriptions leaked")
		}
	})
}

func TestBurstAndContinuousChangesAreBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		e, stop := running(t, func(context.Context) (json.RawMessage, error) { calls.Add(1); return json.RawMessage(`{}`), nil })
		defer stop()
		synctest.Wait()
		for range 1000 {
			e.Notify()
		}
		synctest.Wait()
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("burst caused %d collections", calls.Load())
		}
		metrics := e.Metrics()
		if metrics.Collections != 2 || metrics.Notifications != 1000 || metrics.Coalesced == 0 {
			t.Fatal("coalescing metrics do not match actual scheduling", metrics)
		}
		for range 21 {
			e.Notify()
			synctest.Wait()
			time.Sleep(100 * time.Millisecond)
		}
		synctest.Wait()
		if calls.Load() < 3 || calls.Load() > 4 {
			t.Fatalf("continuous events starved or flooded: %d", calls.Load())
		}
	})
}

func TestSingleFlightCoalescesChangesDuringCollection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		e, stop := running(t, func(ctx context.Context) (json.RawMessage, error) {
			if calls.Add(1) == 1 {
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return json.RawMessage(`{}`), nil
		})
		defer stop()
		synctest.Wait()
		for range 1000 {
			e.Notify()
		}
		synctest.Wait()
		time.Sleep(time.Second)
		if calls.Load() != 1 {
			t.Fatal("concurrent collection")
		}
		close(release)
		synctest.Wait()
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatalf("expected one follow-up: %d", calls.Load())
		}
	})
}

func TestSuspendResumeDiscardsOldResultAndDoesNotPollWhilePaused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		e, stop := running(t, func(context.Context) (json.RawMessage, error) {
			if calls.Add(1) == 1 {
				<-release
				return json.RawMessage(`{"old":true}`), nil
			}
			return json.RawMessage(`{"new":true}`), nil
		})
		defer stop()
		synctest.Wait()
		e.Pause(true)
		synctest.Wait()
		close(release)
		synctest.Wait()
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if calls.Load() != 1 || e.Latest().Fresh(time.Now()) {
			t.Fatal("paused observer collected or was fresh")
		}
		e.Pause(false)
		synctest.Wait()
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if string(e.Latest().Data) != `{"new":true}` || !e.Latest().Fresh(time.Now()) {
			t.Fatalf("resume: %+v", e.Latest())
		}
	})
}

func TestFailureRetainsDataWithoutRenewingObservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		e, stop := running(t, func(context.Context) (json.RawMessage, error) {
			if calls.Add(1) > 1 {
				return nil, errors.New("vendor unavailable")
			}
			return json.RawMessage(`{"retained":true}`), nil
		})
		defer stop()
		synctest.Wait()
		old := e.Latest()
		time.Sleep(time.Minute)
		synctest.Wait()
		latest := e.Latest()
		if latest.Error != "vendor unavailable" || latest.Fresh(time.Now()) || !latest.ObservedAt.Equal(old.ObservedAt) || string(latest.Data) != string(old.Data) {
			t.Fatalf("failure erased data or renewed lease: %+v", latest)
		}
	})
}

func TestTimeoutCancellationAndSlowSubscriber(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, stop := running(t, func(ctx context.Context) (json.RawMessage, error) { <-ctx.Done(); return nil, ctx.Err() })
		ch, unsubscribe := e.Subscribe()
		defer unsubscribe()
		synctest.Wait()
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if e.Latest().Error != context.DeadlineExceeded.Error() {
			t.Fatal(e.Latest())
		}
		e.Pause(true)
		e.Pause(false)
		snapshot := <-ch
		if snapshot.Sequence != e.Latest().Sequence {
			t.Fatal("slow subscriber did not get newest state")
		}
		stop()
		if _, ok := <-ch; ok {
			t.Fatal("subscription not closed")
		}
	})
}

func TestReconfigureChangesDeadlineWithoutRefreshingCachedEvidence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		e, stop := running(t, func(context.Context) (json.RawMessage, error) { calls.Add(1); return json.RawMessage(`{}`), nil })
		defer stop()
		synctest.Wait()
		first := e.Latest()
		opts := e.Options()
		opts.Reconcile = 5 * time.Second
		if err := e.UpdateOptions(opts); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if !e.Latest().ObservedAt.Equal(first.ObservedAt) || calls.Load() != 1 {
			t.Fatal("configuration renewed evidence")
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("deadline did not change", calls.Load())
		}
	})
}
