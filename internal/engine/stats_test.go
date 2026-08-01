package engine

import (
	"math"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestStatsShardCountIsCPUScaledAndBounded(t *testing.T) {
	tests := []struct {
		name         string
		virtualUsers int
		processors   int
		want         int
	}{
		{name: "defaults invalid inputs", virtualUsers: 0, processors: 0, want: 1},
		{name: "does not exceed virtual users", virtualUsers: 3, processors: 8, want: 3},
		{name: "scales with processors", virtualUsers: 1_000, processors: 2, want: 8},
		{name: "caps large machines", virtualUsers: 100_000, processors: 1_000, want: maxStatsShards},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := statsShardCountFor(test.virtualUsers, test.processors); got != test.want {
				t.Fatalf("got %d shards, want %d", got, test.want)
			}
		})
	}
}

func TestAtomicStatsUsesBoundedSharedShardsWithoutLosingCounters(t *testing.T) {
	const (
		virtualUsers = 100_000
		workers      = 1_024
	)
	var stats AtomicStats
	stats.Init(virtualUsers)
	stats.Reset(time.Unix(1, 0))
	stats.RecordDroppedIterations(7)

	if len(stats.shards) > maxStatsShards {
		t.Fatalf("allocated %d stats shards, maximum is %d", len(stats.shards), maxStatsShards)
	}
	if stats.Shard(0) != stats.Shard(len(stats.shards)) {
		t.Fatal("worker indexes should wrap across the bounded shard set")
	}

	var group sync.WaitGroup
	group.Add(workers)
	for worker := range workers {
		go func() {
			defer group.Done()
			shard := stats.Shard(worker)
			latency := time.Duration(worker+1) * time.Microsecond
			if worker%2 == 0 {
				shard.RecordHTTPSuccessSampled(latency, 10, 5, true, 200)
				return
			}
			shard.RecordHTTPFailureSampled(latency, 5, true, FailureTimeout, 503)
		}()
	}
	group.Wait()

	snapshot := stats.Snapshot(time.Unix(2, 0))
	if snapshot.TotalRequests != workers {
		t.Fatalf("got %d total requests, want %d", snapshot.TotalRequests, workers)
	}
	if snapshot.SuccessRequests != workers/2 || snapshot.FailedRequests != workers/2 {
		t.Fatalf(
			"got success=%d failed=%d, want %d each",
			snapshot.SuccessRequests,
			snapshot.FailedRequests,
			workers/2,
		)
	}
	if snapshot.TimeoutFailures != workers/2 {
		t.Fatalf("got %d timeouts, want %d", snapshot.TimeoutFailures, workers/2)
	}
	if snapshot.StatusCodes[200] != workers/2 || snapshot.StatusCodes[503] != workers/2 {
		t.Fatalf(
			"got status 200=%d status 503=%d, want %d each",
			snapshot.StatusCodes[200],
			snapshot.StatusCodes[503],
			workers/2,
		)
	}
	if snapshot.LatencySamples != workers {
		t.Fatalf("got %d latency samples, want %d", snapshot.LatencySamples, workers)
	}
	if snapshot.BytesRead != (workers/2)*10 || snapshot.BytesWritten != workers*5 {
		t.Fatalf(
			"got bytes read=%d written=%d, want read=%d written=%d",
			snapshot.BytesRead,
			snapshot.BytesWritten,
			(workers/2)*10,
			workers*5,
		)
	}
	expectedTotalLatency := uint64(workers*(workers+1)/2) * uint64(time.Microsecond)
	if snapshot.TotalLatencyNano != expectedTotalLatency {
		t.Fatalf("got total latency %d, want %d", snapshot.TotalLatencyNano, expectedTotalLatency)
	}
	if snapshot.MinLatencyNano != uint64(time.Microsecond) {
		t.Fatalf("got minimum latency %d, want %d", snapshot.MinLatencyNano, time.Microsecond)
	}
	if snapshot.MaxLatencyNano != uint64(workers)*uint64(time.Microsecond) {
		t.Fatalf(
			"got maximum latency %d, want %d",
			snapshot.MaxLatencyNano,
			uint64(workers)*uint64(time.Microsecond),
		)
	}
	if snapshot.DroppedIterations != 7 {
		t.Fatalf("got %d dropped iterations, want 7", snapshot.DroppedIterations)
	}
	stats.Reset(time.Unix(3, 0))
	if reset := stats.Snapshot(time.Unix(4, 0)); reset.DroppedIterations != 0 {
		t.Fatalf("dropped iterations were not reset: %d", reset.DroppedIterations)
	}
}

func TestAtomicStatsTracksOnlyValidHTTPStatusRange(t *testing.T) {
	var stats AtomicStats
	stats.Init(1)
	stats.Reset(time.Now())
	shard := stats.Shard(0)
	for _, statusCode := range []int{99, 100, 599, 600} {
		shard.RecordHTTPSuccessSampled(time.Millisecond, 0, 0, false, statusCode)
	}

	snapshot := stats.Snapshot(time.Now())
	if snapshot.TotalRequests != 4 {
		t.Fatalf("got %d total requests, want 4", snapshot.TotalRequests)
	}
	if snapshot.StatusCodes[100] != 1 || snapshot.StatusCodes[599] != 1 {
		t.Fatalf(
			"got status 100=%d status 599=%d, want 1 each",
			snapshot.StatusCodes[100],
			snapshot.StatusCodes[599],
		)
	}
}

func TestAtomicStatsSnapshotCostStopsGrowingAboveShardCap(t *testing.T) {
	var thousandVUs AtomicStats
	thousandVUs.Init(1_000)
	var maximumVUs AtomicStats
	maximumVUs.Init(100_000)

	if len(thousandVUs.shards) != len(maximumVUs.shards) {
		t.Fatalf(
			"snapshot shard counts differ above cap: 1k=%d maximum=%d",
			len(thousandVUs.shards),
			len(maximumVUs.shards),
		)
	}
}

func TestLatencyHistogramBoundsPrecisionAndMemory(t *testing.T) {
	if latencyBucketUpperBoundsNano[0] != LatencyHistogramMinNano {
		t.Fatalf("got minimum bound %d, want %d", latencyBucketUpperBoundsNano[0], LatencyHistogramMinNano)
	}
	if latencyBucketUpperBoundsNano[LatencyBucketCount-2] != LatencyHistogramMaxNano {
		t.Fatalf(
			"got maximum finite bound %d, want %d",
			latencyBucketUpperBoundsNano[LatencyBucketCount-2],
			LatencyHistogramMaxNano,
		)
	}
	if latencyBucketUpperBoundsNano[LatencyBucketCount-1] != math.MaxUint64 {
		t.Fatal("final histogram bucket must capture overflow latencies")
	}

	for index := 1; index < LatencyBucketCount-1; index++ {
		lowestValue := latencyBucketUpperBoundsNano[index-1] + 1
		upperBound := latencyBucketUpperBoundsNano[index]
		if upperBound <= lowestValue {
			continue
		}
		relativeOverestimate := float64(upperBound-lowestValue) / float64(lowestValue)
		if relativeOverestimate >= LatencyHistogramRelativeErrorBound {
			t.Fatalf(
				"bucket %d relative overestimate %.6f exceeds bound %.6f",
				index,
				relativeOverestimate,
				LatencyHistogramRelativeErrorBound,
			)
		}
	}

	const maxStatsMemoryBytes = 4 << 20
	statsMemoryBytes := uintptr(maxStatsShards) * unsafe.Sizeof(statsShard{})
	if statsMemoryBytes > maxStatsMemoryBytes {
		t.Fatalf("bounded stats storage uses %d bytes, budget is %d", statsMemoryBytes, maxStatsMemoryBytes)
	}
}

func TestLatencyHistogramIndexesBoundariesAndBoundsPercentileError(t *testing.T) {
	if got := latencyBucketIndex(0); got != 0 {
		t.Fatalf("zero latency bucket = %d, want 0", got)
	}
	if got := latencyBucketIndex(LatencyHistogramMinNano); got != 0 {
		t.Fatalf("minimum latency bucket = %d, want 0", got)
	}
	if got := latencyBucketIndex(LatencyHistogramMaxNano); got != LatencyBucketCount-2 {
		t.Fatalf("maximum latency bucket = %d, want %d", got, LatencyBucketCount-2)
	}
	if got := latencyBucketIndex(LatencyHistogramMaxNano + 1); got != LatencyBucketCount-1 {
		t.Fatalf("overflow latency bucket = %d, want %d", got, LatencyBucketCount-1)
	}

	const latencyNano = uint64(123_456_789)
	var buckets [LatencyBucketCount]uint64
	buckets[latencyBucketIndex(latencyNano)] = 1
	estimate := PercentileLatencyNano(&buckets, 1, 0.99)
	if estimate < latencyNano {
		t.Fatalf("percentile estimate %d is below observed latency %d", estimate, latencyNano)
	}
	relativeOverestimate := float64(estimate-latencyNano) / float64(latencyNano)
	if relativeOverestimate >= LatencyHistogramRelativeErrorBound {
		t.Fatalf(
			"relative percentile overestimate %.6f exceeds bound %.6f",
			relativeOverestimate,
			LatencyHistogramRelativeErrorBound,
		)
	}
}
