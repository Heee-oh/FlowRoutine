package engine

import (
	"context"
	"sync/atomic"
	"time"
)

type rateLimiter struct {
	intervalNano int64
	nextNano     atomic.Int64
}

func newRateLimiter(rps int) *rateLimiter {
	interval := int64(time.Second) / int64(rps)
	if interval < 1 {
		interval = 1
	}
	limiter := &rateLimiter{intervalNano: interval}
	limiter.Reset(time.Now())
	return limiter
}

func (l *rateLimiter) Reset(now time.Time) {
	l.nextNano.Store(now.UnixNano())
}

func (l *rateLimiter) Wait(ctx context.Context) bool {
	for {
		now := time.Now().UnixNano()
		loaded := l.nextNano.Load()
		reserved := loaded
		if reserved < now {
			reserved = now
		}
		if l.nextNano.CompareAndSwap(loaded, reserved+l.intervalNano) {
			delay := time.Duration(reserved - now)
			if delay <= 0 {
				return true
			}
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return false
			case <-timer.C:
				return true
			}
		}
	}
}
