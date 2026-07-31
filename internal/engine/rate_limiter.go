package engine

import (
	"context"
	"time"
)

type rateLimiter struct {
	interval         time.Duration
	permitsPerTick   int
	remainderPerTick int
	permitCapacity   int
	permits          chan struct{}
}

const maxPacerTicksPerSecond = 1_000

func newRateLimiter(rps int) *rateLimiter {
	if rps < 1 {
		rps = 1
	}
	limiter := &rateLimiter{
		interval:       time.Second / time.Duration(rps),
		permitsPerTick: 1,
		permitCapacity: 1,
	}
	if rps > maxPacerTicksPerSecond {
		limiter.interval = time.Second / maxPacerTicksPerSecond
		limiter.permitsPerTick = rps / maxPacerTicksPerSecond
		limiter.remainderPerTick = rps % maxPacerTicksPerSecond
		limiter.permitCapacity = limiter.permitsPerTick
		if limiter.remainderPerTick > 0 {
			limiter.permitCapacity++
		}
	}
	limiter.Reset()
	return limiter
}

func (l *rateLimiter) Reset() {
	l.permits = make(chan struct{}, l.permitCapacity)
	l.permits <- struct{}{}
}

func (l *rateLimiter) Wait(ctx context.Context) bool {
	permits := l.permits
	select {
	case <-ctx.Done():
		return false
	default:
	}
	select {
	case <-ctx.Done():
		return false
	case <-permits:
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
}

func (l *rateLimiter) Run(ctx context.Context) {
	permits := l.permits
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	remainder := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count := l.permitsPerTick
			remainder += l.remainderPerTick
			if remainder >= maxPacerTicksPerSecond {
				count++
				remainder -= maxPacerTicksPerSecond
			}
		sendPermits:
			for range count {
				select {
				case permits <- struct{}{}:
				default:
					// Keep at most one tick of unused capacity; missed permits are dropped.
					break sendPermits
				}
			}
		}
	}
}
