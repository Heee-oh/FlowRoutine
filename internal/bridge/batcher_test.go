package bridge

import (
	"context"
	"math"
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
		{name: "max", in: 10 * time.Second, want: MaxInterval},
		{name: "keep", in: 125 * time.Millisecond, want: 125 * time.Millisecond},
		{name: "keep long", in: time.Second, want: time.Second},
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
			At:                currentAt,
			TotalRequests:     30,
			SuccessRequests:   25,
			FailedRequests:    5,
			TimeoutFailures:   1,
			DNSFailures:       2,
			TLSFailures:       1,
			ConnRefused:       1,
			AssertionFailures: 7,
			AssertionFailuresByType: engine.AssertionFailureCounts{
				Status:          1,
				Header:          2,
				JSON:            3,
				ResponseLatency: 4,
				StepLatency:     5,
				CountOnly:       2,
			},
			CaptureFailures:   2,
			TemplateFailures:  3,
			DroppedIterations: 9,
			LatencySamples:    30,
			TotalLatencyNano:  uint64(50 * time.Millisecond),
			MinLatencyNano:    uint64(time.Millisecond),
			MaxLatencyNano:    uint64(5 * time.Millisecond),
			BytesRead:         200,
			BytesWritten:      300,
			StatusCodes:       statusCodesSnapshot(map[int]uint64{401: 2, 200: 20, 500: 3}),
		},
		true,
		currentAt,
	)

	if batch.RPS != 200 {
		t.Fatalf("got rps %f, want 200", batch.RPS)
	}
	if batch.IntervalLatency.AvgMs != 2 {
		t.Fatalf("got interval avg latency %f, want 2", batch.IntervalLatency.AvgMs)
	}
	if math.Abs(batch.RunLatency.AvgMs-(5.0/3.0)) > 0.0001 {
		t.Fatalf("got run avg latency %f, want %f", batch.RunLatency.AvgMs, 5.0/3.0)
	}
	if !batch.Running || batch.Total != 30 || batch.Success != 25 || batch.Failed != 5 {
		t.Fatalf("unexpected batch: %+v", batch)
	}
	if batch.Timeout != 1 || batch.DNS != 2 || batch.TLS != 1 || batch.ConnRefused != 1 {
		t.Fatalf("unexpected failure breakdown: %+v", batch)
	}
	if batch.AssertionsFailed != 7 || batch.CaptureFailures != 2 || batch.TemplateFailures != 3 {
		t.Fatalf("unexpected assertion breakdown: %+v", batch)
	}
	if batch.AssertionFailuresByType != (AssertionFailureCounts{
		Status: 1, Header: 2, JSON: 3, ResponseLatency: 4, StepLatency: 5, CountOnly: 2,
	}) {
		t.Fatalf("unexpected typed assertion breakdown: %+v", batch.AssertionFailuresByType)
	}
	if batch.DroppedIterations != 9 {
		t.Fatalf("unexpected dropped iterations: %+v", batch)
	}
	if batch.LatencyPercentileErrorBoundPct != engine.LatencyHistogramRelativeErrorBound*100 {
		t.Fatalf("unexpected latency percentile error bound: %f", batch.LatencyPercentileErrorBoundPct)
	}
	if len(batch.StatusCodes) != 3 ||
		batch.StatusCodes[0] != (StatusCodeCount{Code: 200, Count: 20}) ||
		batch.StatusCodes[1] != (StatusCodeCount{Code: 401, Count: 2}) ||
		batch.StatusCodes[2] != (StatusCodeCount{Code: 500, Count: 3}) {
		t.Fatalf("unexpected status codes: %+v", batch.StatusCodes)
	}
}

func TestBuildMetricsBatchSeparatesIntervalAndRunLatency(t *testing.T) {
	previousAt := time.Unix(10, 0)
	currentAt := previousAt.Add(time.Second)
	var stats engine.AtomicStats
	stats.Init(1)
	stats.Reset(previousAt)
	for range 90 {
		stats.RecordSuccess(time.Second, 0, 0)
	}
	previous := stats.Snapshot(previousAt)
	for range 10 {
		stats.RecordSuccess(time.Millisecond, 0, 0)
	}
	current := stats.Snapshot(currentAt)

	batch := buildMetricsBatch(
		previous,
		current,
		false,
		currentAt,
	)

	if batch.IntervalLatency.Samples != 10 ||
		batch.IntervalLatency.P95Ms < 1 ||
		batch.IntervalLatency.P95Ms >= 1.02 {
		t.Fatalf("unexpected interval latency: %+v", batch.IntervalLatency)
	}
	if batch.RunLatency.Samples != 100 ||
		batch.RunLatency.P95Ms < 1000 ||
		batch.RunLatency.P95Ms >= 1020 {
		t.Fatalf("unexpected run latency: %+v", batch.RunLatency)
	}
	if math.Abs(batch.RunLatency.AvgMs-900.1) > 0.0001 {
		t.Fatalf("got run avg latency %f, want 900.1", batch.RunLatency.AvgMs)
	}
}

func TestBuildMetricsBatchIncludesCompactRequestStepDiagnostics(t *testing.T) {
	now := time.Unix(20, 0)
	batch := buildMetricsBatchWithSteps(
		engine.Snapshot{At: now.Add(-time.Second)},
		engine.Snapshot{At: now, TotalRequests: 7, SuccessRequests: 5, FailedRequests: 2},
		true,
		now,
		[]engine.RequestStepSnapshot{{
			ID:                "request-items",
			Name:              "GET example.com/items",
			TotalRequests:     7,
			SuccessRequests:   5,
			FailedRequests:    2,
			TimeoutFailures:   1,
			AssertionFailures: 1,
			AssertionFailuresByType: engine.AssertionFailureCounts{
				Header:    2,
				CountOnly: 1,
			},
			LatencySamples:   7,
			TotalLatencyNano: uint64(70 * time.Millisecond),
			MinLatencyNano:   uint64(time.Millisecond),
			MaxLatencyNano:   uint64(20 * time.Millisecond),
			P95LatencyNano:   uint64(19 * time.Millisecond),
			P99LatencyNano:   uint64(20 * time.Millisecond),
			P999LatencyNano:  uint64(20 * time.Millisecond),
			StatusCodes: []engine.StepStatusCodeCount{
				{Code: 200, Count: 5},
				{Code: 500, Count: 2},
			},
		}},
	)

	if len(batch.StepMetrics) != 1 {
		t.Fatalf("got %d step metrics, want 1", len(batch.StepMetrics))
	}
	step := batch.StepMetrics[0]
	if step.ID != "request-items" || step.Total != batch.Total || step.Failed != batch.Failed {
		t.Fatalf("unexpected request-step totals: %+v", step)
	}
	if step.RunLatency.AvgMs != 10 || step.RunLatency.P99Ms != 20 {
		t.Fatalf("unexpected request-step latency: %+v", step.RunLatency)
	}
	if step.AssertionFailuresByType.Header != 2 || step.AssertionFailuresByType.CountOnly != 1 {
		t.Fatalf("unexpected request-step assertion types: %+v", step.AssertionFailuresByType)
	}
	if len(step.StatusCodes) != 2 || step.StatusCodes[1] != (StatusCodeCount{Code: 500, Count: 2}) {
		t.Fatalf("unexpected request-step status codes: %+v", step.StatusCodes)
	}
}

func TestBuildMetricsBatchIncludesBranchRouteDiagnostics(t *testing.T) {
	now := time.Unix(21, 0)
	batch := buildMetricsBatchWithDiagnostics(
		engine.Snapshot{At: now.Add(-time.Second)},
		engine.Snapshot{At: now, TotalRequests: 5, SuccessRequests: 4, FailedRequests: 1},
		false,
		now,
		nil,
		[]engine.BranchRouteSnapshot{{
			BranchID: "branch", RouteID: "route-a", Name: "Route A",
			Selections: 3, Total: 5, Success: 4, Failed: 1,
		}},
	)
	if len(batch.BranchMetrics) != 1 {
		t.Fatalf("got %d branch metrics, want 1", len(batch.BranchMetrics))
	}
	metric := batch.BranchMetrics[0]
	if metric.BranchID != "branch" || metric.RouteID != "route-a" ||
		metric.Selections != 3 || metric.Total != 5 || metric.Success != 4 || metric.Failed != 1 {
		t.Fatalf("unexpected branch metrics: %+v", metric)
	}
}

func TestStepMetricsEmissionIsThrottledAndForcedAtCompletion(t *testing.T) {
	startedAt := time.Unix(1, 0)
	if !shouldIncludeStepMetrics(time.Time{}, startedAt, false, true) {
		t.Fatal("first step-metrics snapshot should be included")
	}
	if shouldIncludeStepMetrics(startedAt, startedAt.Add(StepMetricsInterval-time.Millisecond), false, true) {
		t.Fatal("step metrics should be omitted inside the throttle interval")
	}
	if !shouldIncludeStepMetrics(startedAt, startedAt.Add(StepMetricsInterval), false, true) {
		t.Fatal("step metrics should be included after the throttle interval")
	}
	if !shouldIncludeStepMetrics(startedAt, startedAt.Add(time.Millisecond), true, true) {
		t.Fatal("final step metrics should always be included")
	}
	if !shouldIncludeStepMetrics(startedAt, startedAt.Add(time.Millisecond), false, false) {
		t.Fatal("terminal batches should always include diagnostic metrics")
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
		ScenarioSteps: []engine.ScenarioStep{
			{ID: "first", Kind: engine.StepRequest, URL: "http://127.0.0.1:1/first"},
			{ID: "second", Kind: engine.StepRequest, URL: "http://127.0.0.1:1/second"},
		},
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
	final := emitter.lastBatch()
	if len(final.StepMetrics) != 2 {
		t.Fatalf("expected final request-step metrics, got %+v", emitter.lastBatch())
	}
	var total, success, failed uint64
	for _, step := range final.StepMetrics {
		total += step.Total
		success += step.Success
		failed += step.Failed
	}
	if total != final.Total || success != final.Success || failed != final.Failed {
		t.Fatalf("final aggregate does not equal request-step totals: batch=%+v steps=%+v", final, final.StepMetrics)
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
