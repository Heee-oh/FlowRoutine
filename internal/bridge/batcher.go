package bridge

import (
	"context"
	"errors"
	"sync"
	"time"

	"flowroutine/internal/engine"
)

const (
	MetricsBatchEvent = "metrics:batch"
	DefaultInterval   = 150 * time.Millisecond
	MinInterval       = 100 * time.Millisecond
	MaxInterval       = 200 * time.Millisecond
)

var ErrBatcherRunning = errors.New("metrics batcher is already running")

type Emitter interface {
	Emit(ctx context.Context, eventName string, data any)
}

type MetricsBatch struct {
	TimestampUnixMs  int64             `json:"timestampUnixMs"`
	Running          bool              `json:"running"`
	RPS              float64           `json:"rps"`
	Total            uint64            `json:"total"`
	Success          uint64            `json:"success"`
	Failed           uint64            `json:"failed"`
	Timeout          uint64            `json:"timeout"`
	DNS              uint64            `json:"dns"`
	TLS              uint64            `json:"tls"`
	ConnRefused      uint64            `json:"connRefused"`
	OtherErrors      uint64            `json:"otherErrors"`
	AssertionsFailed uint64            `json:"assertionsFailed"`
	AvgLatencyMs     float64           `json:"avgLatencyMs"`
	MinLatencyMs     float64           `json:"minLatencyMs"`
	MaxLatencyMs     float64           `json:"maxLatencyMs"`
	P95LatencyMs     float64           `json:"p95LatencyMs"`
	P99LatencyMs     float64           `json:"p99LatencyMs"`
	P999LatencyMs    float64           `json:"p999LatencyMs"`
	BytesRead        uint64            `json:"bytesRead"`
	BytesWritten     uint64            `json:"bytesWritten"`
	StatusCodes      []StatusCodeCount `json:"statusCodes"`
}

type StatusCodeCount struct {
	Code  int    `json:"code"`
	Count uint64 `json:"count"`
}

type Batcher struct {
	engine   *engine.Engine
	emitter  Emitter
	interval time.Duration

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
	done    chan struct{}
}

func NewBatcher(e *engine.Engine, emitter Emitter, interval time.Duration) *Batcher {
	return &Batcher{
		engine:   e,
		emitter:  emitter,
		interval: normalizeInterval(interval),
	}
}

func (b *Batcher) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return ErrBatcherRunning
	}
	ctx, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	b.running = true
	b.done = make(chan struct{})
	done := b.done
	b.mu.Unlock()

	go b.run(ctx, done)
	return nil
}

func (b *Batcher) Stop() {
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return
	}
	cancel := b.cancel
	done := b.done
	b.mu.Unlock()

	cancel()
	<-done
}

func (b *Batcher) run(ctx context.Context, done chan struct{}) {
	previous := b.engine.Snapshot()
	defer func() {
		current := b.engine.Snapshot()
		b.emitter.Emit(ctx, MetricsBatchEvent, buildMetricsBatch(previous, current, b.engine.Running(), time.Now()))

		b.mu.Lock()
		b.running = false
		b.cancel = nil
		b.done = nil
		b.mu.Unlock()
		close(done)
	}()

	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			current := b.engine.Snapshot()
			b.emitter.Emit(ctx, MetricsBatchEvent, buildMetricsBatch(previous, current, b.engine.Running(), now))
			previous = current
		}
	}
}

func buildMetricsBatch(previous engine.Snapshot, current engine.Snapshot, running bool, now time.Time) MetricsBatch {
	elapsed := current.At.Sub(previous.At).Seconds()
	totalDelta := current.TotalRequests - previous.TotalRequests
	latencyDelta := current.TotalLatencyNano - previous.TotalLatencyNano
	latencySampleDelta := current.LatencySamples - previous.LatencySamples
	latencyBucketDelta := subtractLatencyBuckets(previous.LatencyBuckets, current.LatencyBuckets)

	var rps float64
	var avgLatencyMs float64
	if elapsed > 0 {
		rps = float64(totalDelta) / elapsed
	}
	if latencySampleDelta > 0 {
		avgLatencyMs = float64(latencyDelta) / float64(latencySampleDelta) / float64(time.Millisecond)
	}

	return MetricsBatch{
		TimestampUnixMs:  now.UnixMilli(),
		Running:          running,
		RPS:              rps,
		Total:            current.TotalRequests,
		Success:          current.SuccessRequests,
		Failed:           current.FailedRequests,
		Timeout:          current.TimeoutFailures,
		DNS:              current.DNSFailures,
		TLS:              current.TLSFailures,
		ConnRefused:      current.ConnRefused,
		OtherErrors:      current.OtherFailures,
		AssertionsFailed: current.AssertionFailures,
		AvgLatencyMs:     avgLatencyMs,
		MinLatencyMs:     float64(current.MinLatencyNano) / float64(time.Millisecond),
		MaxLatencyMs:     float64(current.MaxLatencyNano) / float64(time.Millisecond),
		P95LatencyMs:     float64(engine.PercentileLatencyNano(latencyBucketDelta, latencySampleDelta, 0.95)) / float64(time.Millisecond),
		P99LatencyMs:     float64(engine.PercentileLatencyNano(latencyBucketDelta, latencySampleDelta, 0.99)) / float64(time.Millisecond),
		P999LatencyMs:    float64(engine.PercentileLatencyNano(latencyBucketDelta, latencySampleDelta, 0.999)) / float64(time.Millisecond),
		BytesRead:        current.BytesRead,
		BytesWritten:     current.BytesWritten,
		StatusCodes:      buildStatusCodeCounts(current.StatusCodes),
	}
}

func buildStatusCodeCounts(statusCodes [engine.HTTPStatusCount]uint64) []StatusCodeCount {
	counts := make([]StatusCodeCount, 0)
	for code, count := range statusCodes {
		if count > 0 {
			counts = append(counts, StatusCodeCount{Code: code, Count: count})
		}
	}
	return counts
}

func subtractLatencyBuckets(previous [engine.LatencyBucketCount]uint64, current [engine.LatencyBucketCount]uint64) [engine.LatencyBucketCount]uint64 {
	var delta [engine.LatencyBucketCount]uint64
	for i := range delta {
		if current[i] >= previous[i] {
			delta[i] = current[i] - previous[i]
		}
	}
	return delta
}

func normalizeInterval(interval time.Duration) time.Duration {
	if interval == 0 {
		return DefaultInterval
	}
	if interval < MinInterval {
		return MinInterval
	}
	if interval > MaxInterval {
		return MaxInterval
	}
	return interval
}
