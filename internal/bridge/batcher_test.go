package bridge

import (
	"context"
	"sync"
	"testing"
	"time"

	"flowroutine/internal/engine"
)

type captureEmitter struct {
	mu     sync.Mutex
	events []string
	data   []any
}

func (e *captureEmitter) Emit(ctx context.Context, eventName string, data any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, eventName)
	e.data = append(e.data, data)
}

func (e *captureEmitter) len() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.events)
}

func (e *captureEmitter) lastBatch() MetricsBatch {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.data) == 0 {
		return MetricsBatch{}
	}
	batch, _ := e.data[len(e.data)-1].(MetricsBatch)
	return batch
}

func TestNormalizeInterval(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{name: "default", in: 0, want: DefaultInterval},
		{name: "min", in: time.Millisecond, want: MinInterval},
		{name: "max", in: time.Second, want: MaxInterval},
		{name: "keep", in: 125 * time.Millisecond, want: 125 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeInterval(tt.in); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestBuildMetricsBatchUsesSnapshotDelta(t *testing.T) {
	previousAt := time.Unix(10, 0)
	currentAt := previousAt.Add(100 * time.Millisecond)

	batch := buildMetricsBatch(
		engine.Snapshot{At: previousAt, TotalRequests: 10, LatencySamples: 10, TotalLatencyNano: uint64(10 * time.Millisecond)},
		engine.Snapshot{
			At:               currentAt,
			TotalRequests:    30,
			SuccessRequests:  25,
			FailedRequests:   5,
			TimeoutFailures:  1,
			DNSFailures:      2,
			TLSFailures:      1,
			ConnRefused:      1,
			LatencySamples:   30,
			TotalLatencyNano: uint64(50 * time.Millisecond),
			MinLatencyNano:   uint64(time.Millisecond),
			MaxLatencyNano:   uint64(5 * time.Millisecond),
			BytesRead:        200,
			BytesWritten:     300,
			StatusCodes:      statusCodesSnapshot(map[int]uint64{401: 2, 200: 20, 500: 3}),
		},
		true,
		currentAt,
	)

	if batch.RPS != 200 {
		t.Fatalf("got rps %f, want 200", batch.RPS)
	}
	if batch.AvgLatencyMs != 2 {
		t.Fatalf("got avg latency %f, want 2", batch.AvgLatencyMs)
	}
	if !batch.Running || batch.Total != 30 || batch.Success != 25 || batch.Failed != 5 {
		t.Fatalf("unexpected batch: %+v", batch)
	}
	if batch.Timeout != 1 || batch.DNS != 2 || batch.TLS != 1 || batch.ConnRefused != 1 {
		t.Fatalf("unexpected failure breakdown: %+v", batch)
	}
	if len(batch.StatusCodes) != 3 ||
		batch.StatusCodes[0] != (StatusCodeCount{Code: 200, Count: 20}) ||
		batch.StatusCodes[1] != (StatusCodeCount{Code: 401, Count: 2}) ||
		batch.StatusCodes[2] != (StatusCodeCount{Code: 500, Count: 3}) {
		t.Fatalf("unexpected status codes: %+v", batch.StatusCodes)
	}
}

func statusCodesSnapshot(values map[int]uint64) [engine.HTTPStatusCount]uint64 {
	var statusCodes [engine.HTTPStatusCount]uint64
	for code, count := range values {
		statusCodes[code] = count
	}
	return statusCodes
}

func TestBatcherEmitsOnTick(t *testing.T) {
	e, err := engine.New(engine.Config{
		URL:             "http://127.0.0.1:1",
		Duration:        250 * time.Millisecond,
		RequestTimeout:  time.Millisecond,
		MaxConnsPerHost: engine.DefaultMaxConnsPerHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e.Running() {
			_ = e.Stop()
		}
	}()

	emitter := &captureEmitter{}
	batcher := NewBatcher(e, emitter, MinInterval)
	if err := batcher.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(220 * time.Millisecond)
	batcher.Stop()

	if emitter.len() < 2 {
		t.Fatalf("expected at least two batched events, got %d", emitter.len())
	}
}

func TestBatcherEmitsFinalStoppedBatch(t *testing.T) {
	e, err := engine.New(engine.Config{
		URL:             "http://127.0.0.1:1",
		Duration:        10 * time.Millisecond,
		RequestTimeout:  time.Millisecond,
		MaxConnsPerHost: engine.DefaultMaxConnsPerHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	emitter := &captureEmitter{}
	batcher := NewBatcher(e, emitter, MinInterval)
	if err := batcher.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	<-e.Done()
	batcher.Stop()

	if emitter.len() == 0 {
		t.Fatal("expected final stopped batch")
	}
	if emitter.lastBatch().Running {
		t.Fatalf("expected final batch to report stopped, got %+v", emitter.lastBatch())
	}
}

func TestLoadConfigConvertsMillisecondsForEngine(t *testing.T) {
	cfg := LoadConfig{
		URL:              "http://127.0.0.1:8080",
		Method:           "POST",
		Headers:          []Header{{Name: "Content-Type", Value: "application/json"}},
		Body:             `{"ok":true}`,
		VirtualUsers:     8,
		DurationMS:       1000,
		RequestTimeoutMS: 250,
		MaxConnsPerHost:  engine.DefaultMaxConnsPerHost,
		RateLimitRPS:     500,
		RampUpMS:         2000,
	}.toEngineConfig()

	if cfg.Duration != time.Second {
		t.Fatalf("got duration %s, want 1s", cfg.Duration)
	}
	if cfg.RequestTimeout != 250*time.Millisecond {
		t.Fatalf("got timeout %s, want 250ms", cfg.RequestTimeout)
	}
	if cfg.RateLimitRPS != 500 {
		t.Fatalf("got rate limit %d, want 500", cfg.RateLimitRPS)
	}
	if cfg.RampUp != 2*time.Second {
		t.Fatalf("got ramp up %s, want 2s", cfg.RampUp)
	}
	if string(cfg.Body) != `{"ok":true}` || len(cfg.Headers) != 1 {
		t.Fatalf("unexpected config conversion: %+v", cfg)
	}
}

func TestLoadConfigValidateRejectsUnsafeDurations(t *testing.T) {
	cfg := LoadConfig{
		URL:              "http://127.0.0.1:8080",
		VirtualUsers:     1,
		DurationMS:       0,
		RequestTimeoutMS: 250,
		MaxConnsPerHost:  engine.DefaultMaxConnsPerHost,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected zero duration to be rejected")
	}

	cfg.DurationMS = int64((MaxDuration + time.Millisecond) / time.Millisecond)
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected excessive duration to be rejected")
	}
}

func TestLoadConfigValidateAcceptsSafeRun(t *testing.T) {
	cfg := LoadConfig{
		URL:              "http://127.0.0.1:8080",
		VirtualUsers:     8,
		DurationMS:       1000,
		RequestTimeoutMS: 250,
		MaxConnsPerHost:  engine.DefaultMaxConnsPerHost,
		RateLimitRPS:     1000,
		RampUpMS:         100,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
