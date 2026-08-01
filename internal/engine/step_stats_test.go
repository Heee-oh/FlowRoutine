package engine

import (
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestRequestStepStatsAggregateToGlobalRequestTotals(t *testing.T) {
	var stats AtomicStats
	stats.Init(8)
	stats.initRequestSteps(8, []requestStepDescriptor{
		{id: "first", name: "GET first"},
		{id: "second", name: "GET second"},
	})
	stats.Reset(time.Unix(1, 0))

	for worker := range 8 {
		global := stats.Shard(worker)
		stepIndex := worker % 2
		step := stats.requestStepShard(stepIndex, worker)
		latency := time.Duration(worker+1) * time.Millisecond
		if worker%2 == 0 {
			global.RecordHTTPSuccessSampled(latency, 0, 0, true, 200)
			step.RecordHTTPSuccessSampled(latency, true, 200)
			continue
		}
		global.RecordHTTPFailureSampled(latency, 0, true, FailureTimeout, 504)
		step.RecordHTTPFailureSampled(latency, true, FailureTimeout, 504)
	}

	aggregate := stats.Snapshot(time.Unix(2, 0))
	steps := stats.RequestStepSnapshots()
	if len(steps) != 2 {
		t.Fatalf("got %d request steps, want 2", len(steps))
	}
	var total, success, failed uint64
	for _, step := range steps {
		total += step.TotalRequests
		success += step.SuccessRequests
		failed += step.FailedRequests
		if step.LatencySamples == 0 || step.P99LatencyNano == 0 {
			t.Fatalf("request step did not retain its latency histogram: %+v", step)
		}
		var bucketSamples uint64
		for _, count := range step.LatencyBuckets {
			bucketSamples += count
		}
		if bucketSamples != step.LatencySamples {
			t.Fatalf("request step exposed %d histogram samples, want %d", bucketSamples, step.LatencySamples)
		}
	}
	if total != aggregate.TotalRequests || success != aggregate.SuccessRequests || failed != aggregate.FailedRequests {
		t.Fatalf("step totals total=%d success=%d failed=%d do not match aggregate %+v", total, success, failed, aggregate)
	}
	if len(steps[0].StatusCodes) != 1 || steps[0].StatusCodes[0] != (StepStatusCodeCount{Code: 200, Count: 4}) {
		t.Fatalf("unexpected first-step status codes: %+v", steps[0].StatusCodes)
	}
	if len(steps[1].StatusCodes) != 1 || steps[1].StatusCodes[0] != (StepStatusCodeCount{Code: 504, Count: 4}) || steps[1].TimeoutFailures != 4 {
		t.Fatalf("unexpected second-step diagnostics: %+v", steps[1])
	}

	stats.Reset(time.Unix(3, 0))
	for _, step := range stats.RequestStepSnapshots() {
		if step.TotalRequests != 0 || step.LatencySamples != 0 || len(step.StatusCodes) != 0 {
			t.Fatalf("request-step stats were not reset: %+v", step)
		}
	}
}

func TestRequestStepStatsMemoryAndShardsAreBounded(t *testing.T) {
	const maximumExpectedBytes = 25 << 20
	if actual := uint64(unsafe.Sizeof(requestStepStatsShard{})); actual > requestStepStatsShardEstimatedBytes {
		t.Fatalf("request-step shard estimate %d is below actual size %d", requestStepStatsShardEstimatedBytes, actual)
	}
	if got := requestStepStatsShardCount(100_000); got != maxRequestStepStatsShards {
		t.Fatalf("got %d request-step shards, want cap %d", got, maxRequestStepStatsShards)
	}
	if estimated := EstimateStepMetricsBytes(MaxScenarioSteps, 100_000); estimated > maximumExpectedBytes {
		t.Fatalf("estimated request-step metrics memory %d exceeds %d", estimated, maximumExpectedBytes)
	}
}

func TestScenarioStepIdentityValidation(t *testing.T) {
	base := Config{
		URL: "http://example.com",
		ScenarioSteps: []ScenarioStep{
			{ID: "duplicate", Kind: StepRequest, URL: "http://example.com/one"},
			{ID: "duplicate", Kind: StepRequest, URL: "http://example.com/two"},
		},
	}
	if err := ValidateConfig(base); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("got %v, want duplicate step id error", err)
	}

	base.ScenarioSteps = make([]ScenarioStep, MaxScenarioSteps+1)
	if err := ValidateConfig(base); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("got %v, want maximum step count error", err)
	}
}
