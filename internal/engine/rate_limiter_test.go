package engine

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRateLimiterSaturatedThroughputWithinTolerance(t *testing.T) {
	const rps = 50
	limiter := newRateLimiter(rps)
	limiter.Reset()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pacerDone := make(chan struct{})
	go func() {
		limiter.Run(ctx)
		close(pacerDone)
	}()

	permits := 0
	for limiter.Wait(ctx) {
		permits++
	}
	<-pacerDone

	const expected = rps + 1 // One immediate permit, then one per interval.
	const tolerancePercent = 20
	minimum := expected * (100 - tolerancePercent) / 100
	maximum := expected * (100 + tolerancePercent) / 100
	if permits < minimum || permits > maximum {
		t.Fatalf("got %d permits in one second, want %d..%d", permits, minimum, maximum)
	}
}

func TestRateLimiterRejectsReadyPermitAfterCancellation(t *testing.T) {
	limiter := newRateLimiter(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if limiter.Wait(ctx) {
		t.Fatal("cancelled context must not consume a ready permit")
	}
}

func TestRateLimiterBatchesHighRatesToOneMillisecondOfCapacity(t *testing.T) {
	limiter := newRateLimiter(10_000_000)
	if limiter.interval != time.Millisecond {
		t.Fatalf("got pacer interval %s, want 1ms", limiter.interval)
	}
	if limiter.permitsPerTick != 10_000 || limiter.permitCapacity != 10_000 {
		t.Fatalf(
			"unexpected high-rate batch: permits=%d capacity=%d",
			limiter.permitsPerTick,
			limiter.permitCapacity,
		)
	}
	if cap(limiter.permits) != limiter.permitCapacity {
		t.Fatalf("got permit channel capacity %d, want %d", cap(limiter.permits), limiter.permitCapacity)
	}
}

func TestRateLimiterCancelsManyWaitersPromptly(t *testing.T) {
	const waiters = 2_048
	limiter := newRateLimiter(1)
	limiter.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	pacerDone := make(chan struct{})
	go func() {
		limiter.Run(ctx)
		close(pacerDone)
	}()
	if !limiter.Wait(ctx) {
		t.Fatal("expected the initial permit")
	}

	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(waiters)
	done.Add(waiters)
	for range waiters {
		go func() {
			defer done.Done()
			ready.Done()
			limiter.Wait(ctx)
		}()
	}
	ready.Wait()

	startedAt := time.Now()
	cancel()
	waitDone := make(chan struct{})
	go func() {
		done.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("rate-limited waiters did not cancel within one second")
	}
	select {
	case <-pacerDone:
	case <-time.After(time.Second):
		t.Fatal("central pacer did not stop within one second")
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("cancelling rate-limited waiters took %s", elapsed)
	}
}

func TestNewRejectsRateLimitAboveMaximum(t *testing.T) {
	_, err := New(Config{
		URL:             "http://example.com",
		MaxConnsPerHost: DefaultMaxConnsPerHost,
		RateLimitRPS:    MaxRateLimitRPS + 1,
	})
	if err == nil {
		t.Fatal("expected rate limit above maximum to be rejected")
	}
}
