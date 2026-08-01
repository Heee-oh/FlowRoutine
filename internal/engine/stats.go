package engine

import (
	"math"
	"runtime"
	"sync/atomic"
	"time"
)

const (
	LatencyBucketCount                 = 1024
	LatencyHistogramMinNano            = uint64(time.Microsecond)
	LatencyHistogramMaxNano            = uint64(5 * time.Minute)
	LatencyHistogramRelativeErrorBound = 0.02
	HTTPStatusCount                    = 600
	maxStatsShards                     = 256
	statsShardsPerProcessor            = 4
	firstTrackedHTTPStatus             = 100
	trackedHTTPStatusCount             = HTTPStatusCount - firstTrackedHTTPStatus
	latencyHistogramFiniteBucketCount  = LatencyBucketCount - 1
)

var latencyBucketUpperBoundsNano = buildLatencyBucketUpperBounds()

type AtomicStats struct {
	startedAtUnixNano atomic.Int64
	shards            []statsShard
}

type statsShard struct {
	totalRequests     atomic.Uint64
	successRequests   atomic.Uint64
	failedRequests    atomic.Uint64
	timeoutFailures   atomic.Uint64
	dnsFailures       atomic.Uint64
	tlsFailures       atomic.Uint64
	connRefused       atomic.Uint64
	otherFailures     atomic.Uint64
	assertionFailures atomic.Uint64
	captureFailures   atomic.Uint64
	templateFailures  atomic.Uint64
	latencySamples    atomic.Uint64
	totalLatencyNano  atomic.Uint64
	minLatencyNano    atomic.Uint64
	maxLatencyNano    atomic.Uint64
	latencyBuckets    [LatencyBucketCount]atomic.Uint64
	statusCodes       [trackedHTTPStatusCount]atomic.Uint64
	bytesRead         atomic.Uint64
	bytesWritten      atomic.Uint64
	_                 [64]byte
}

type Snapshot struct {
	StartedAt         time.Time
	At                time.Time
	TotalRequests     uint64
	SuccessRequests   uint64
	FailedRequests    uint64
	TimeoutFailures   uint64
	DNSFailures       uint64
	TLSFailures       uint64
	ConnRefused       uint64
	OtherFailures     uint64
	AssertionFailures uint64
	CaptureFailures   uint64
	TemplateFailures  uint64
	LatencySamples    uint64
	TotalLatencyNano  uint64
	MinLatencyNano    uint64
	MaxLatencyNano    uint64
	P95LatencyNano    uint64
	P99LatencyNano    uint64
	P999LatencyNano   uint64
	LatencyBuckets    [LatencyBucketCount]uint64
	StatusCodes       [HTTPStatusCount]uint64
	BytesRead         uint64
	BytesWritten      uint64
}

func (s *AtomicStats) Init(virtualUsers int) {
	shards := statsShardCount(virtualUsers)
	if len(s.shards) == shards {
		return
	}
	s.shards = make([]statsShard, shards)
}

func statsShardCount(virtualUsers int) int {
	return statsShardCountFor(virtualUsers, runtime.GOMAXPROCS(0))
}

func statsShardCountFor(virtualUsers int, processors int) int {
	if virtualUsers < 1 {
		return 1
	}
	if processors < 1 {
		processors = 1
	}
	if processors > maxStatsShards/statsShardsPerProcessor {
		processors = maxStatsShards / statsShardsPerProcessor
	}
	shards := processors * statsShardsPerProcessor
	if virtualUsers < shards {
		return virtualUsers
	}
	return shards
}

func (s *AtomicStats) Reset(startedAt time.Time) {
	if len(s.shards) == 0 {
		s.Init(1)
	}
	s.startedAtUnixNano.Store(startedAt.UnixNano())
	for i := range s.shards {
		s.shards[i].reset()
	}
}

func (s *AtomicStats) Shard(index int) *statsShard {
	return &s.shards[index%len(s.shards)]
}

func (s *AtomicStats) RecordSuccess(latency time.Duration, bytesRead int, bytesWritten int) {
	s.RecordSuccessSampled(latency, bytesRead, bytesWritten, true)
}

func (s *AtomicStats) RecordSuccessSampled(latency time.Duration, bytesRead int, bytesWritten int, sampleLatency bool) {
	if len(s.shards) == 0 {
		s.Init(1)
	}
	s.shards[0].RecordSuccessSampled(latency, bytesRead, bytesWritten, sampleLatency)
}

func (s *AtomicStats) RecordHTTPSuccessSampled(latency time.Duration, bytesRead int, bytesWritten int, sampleLatency bool, statusCode int) {
	if len(s.shards) == 0 {
		s.Init(1)
	}
	s.shards[0].RecordHTTPSuccessSampled(latency, bytesRead, bytesWritten, sampleLatency, statusCode)
}

func (s *AtomicStats) RecordFailure(latency time.Duration, bytesWritten int) {
	s.RecordFailureSampled(latency, bytesWritten, true, FailureOther)
}

func (s *AtomicStats) RecordFailureSampled(latency time.Duration, bytesWritten int, sampleLatency bool, failure FailureKind) {
	if len(s.shards) == 0 {
		s.Init(1)
	}
	s.shards[0].RecordFailureSampled(latency, bytesWritten, sampleLatency, failure)
}

func (s *AtomicStats) RecordHTTPFailureSampled(latency time.Duration, bytesWritten int, sampleLatency bool, failure FailureKind, statusCode int) {
	if len(s.shards) == 0 {
		s.Init(1)
	}
	s.shards[0].RecordHTTPFailureSampled(latency, bytesWritten, sampleLatency, failure, statusCode)
}

func (s *AtomicStats) Snapshot(now time.Time) Snapshot {
	snapshot := Snapshot{
		StartedAt:      time.Unix(0, s.startedAtUnixNano.Load()),
		At:             now,
		MinLatencyNano: math.MaxUint64,
	}

	for i := range s.shards {
		shard := &s.shards[i]
		snapshot.TotalRequests += shard.totalRequests.Load()
		snapshot.SuccessRequests += shard.successRequests.Load()
		snapshot.FailedRequests += shard.failedRequests.Load()
		snapshot.TimeoutFailures += shard.timeoutFailures.Load()
		snapshot.DNSFailures += shard.dnsFailures.Load()
		snapshot.TLSFailures += shard.tlsFailures.Load()
		snapshot.ConnRefused += shard.connRefused.Load()
		snapshot.OtherFailures += shard.otherFailures.Load()
		snapshot.AssertionFailures += shard.assertionFailures.Load()
		snapshot.CaptureFailures += shard.captureFailures.Load()
		snapshot.TemplateFailures += shard.templateFailures.Load()
		snapshot.LatencySamples += shard.latencySamples.Load()
		snapshot.TotalLatencyNano += shard.totalLatencyNano.Load()
		snapshot.BytesRead += shard.bytesRead.Load()
		snapshot.BytesWritten += shard.bytesWritten.Load()
		for bucket := range snapshot.LatencyBuckets {
			snapshot.LatencyBuckets[bucket] += shard.latencyBuckets[bucket].Load()
		}
		for code := range shard.statusCodes {
			snapshot.StatusCodes[code+firstTrackedHTTPStatus] += shard.statusCodes[code].Load()
		}

		minLatencyNano := shard.minLatencyNano.Load()
		if minLatencyNano < snapshot.MinLatencyNano {
			snapshot.MinLatencyNano = minLatencyNano
		}
		maxLatencyNano := shard.maxLatencyNano.Load()
		if maxLatencyNano > snapshot.MaxLatencyNano {
			snapshot.MaxLatencyNano = maxLatencyNano
		}
	}

	if snapshot.MinLatencyNano == math.MaxUint64 {
		snapshot.MinLatencyNano = 0
	}
	snapshot.P95LatencyNano = PercentileLatencyNano(&snapshot.LatencyBuckets, snapshot.LatencySamples, 0.95)
	snapshot.P99LatencyNano = PercentileLatencyNano(&snapshot.LatencyBuckets, snapshot.LatencySamples, 0.99)
	snapshot.P999LatencyNano = PercentileLatencyNano(&snapshot.LatencyBuckets, snapshot.LatencySamples, 0.999)
	return snapshot
}

func PercentileLatencyNano(buckets *[LatencyBucketCount]uint64, samples uint64, percentile float64) uint64 {
	if samples == 0 {
		return 0
	}
	threshold := uint64(math.Ceil(float64(samples) * percentile))
	if threshold == 0 {
		threshold = 1
	}
	var cumulative uint64
	for i, count := range buckets {
		cumulative += count
		if cumulative >= threshold {
			return latencyBucketUpperBoundsNano[i]
		}
	}
	return latencyBucketUpperBoundsNano[LatencyBucketCount-1]
}

func (s *statsShard) reset() {
	s.totalRequests.Store(0)
	s.successRequests.Store(0)
	s.failedRequests.Store(0)
	s.timeoutFailures.Store(0)
	s.dnsFailures.Store(0)
	s.tlsFailures.Store(0)
	s.connRefused.Store(0)
	s.otherFailures.Store(0)
	s.assertionFailures.Store(0)
	s.captureFailures.Store(0)
	s.templateFailures.Store(0)
	s.latencySamples.Store(0)
	s.totalLatencyNano.Store(0)
	s.minLatencyNano.Store(math.MaxUint64)
	s.maxLatencyNano.Store(0)
	for i := range s.latencyBuckets {
		s.latencyBuckets[i].Store(0)
	}
	for i := range s.statusCodes {
		s.statusCodes[i].Store(0)
	}
	s.bytesRead.Store(0)
	s.bytesWritten.Store(0)
}

func (s *statsShard) RecordSuccessSampled(latency time.Duration, bytesRead int, bytesWritten int, sampleLatency bool) {
	s.recordSuccessSampled(latency, bytesRead, bytesWritten, sampleLatency, 0)
}

func (s *statsShard) RecordHTTPSuccessSampled(latency time.Duration, bytesRead int, bytesWritten int, sampleLatency bool, statusCode int) {
	s.recordSuccessSampled(latency, bytesRead, bytesWritten, sampleLatency, statusCode)
}

func (s *statsShard) recordSuccessSampled(latency time.Duration, bytesRead int, bytesWritten int, sampleLatency bool, statusCode int) {
	if latency < 0 {
		latency = 0
	}
	s.totalRequests.Add(1)
	s.successRequests.Add(1)
	s.recordStatusCode(statusCode)
	if bytesRead > 0 {
		s.bytesRead.Add(uint64(bytesRead))
	}
	if bytesWritten > 0 {
		s.bytesWritten.Add(uint64(bytesWritten))
	}
	if !sampleLatency {
		return
	}
	latencyNano := uint64(latency.Nanoseconds())
	s.latencySamples.Add(1)
	s.totalLatencyNano.Add(latencyNano)
	s.latencyBuckets[latencyBucketIndex(latencyNano)].Add(1)
	s.recordMinLatency(latencyNano)
	s.recordMaxLatency(latencyNano)
}

func (s *statsShard) RecordFailureSampled(latency time.Duration, bytesWritten int, sampleLatency bool, failure FailureKind) {
	s.recordFailureSampled(latency, bytesWritten, sampleLatency, failure, 0)
}

func (s *statsShard) RecordHTTPFailureSampled(latency time.Duration, bytesWritten int, sampleLatency bool, failure FailureKind, statusCode int) {
	s.recordFailureSampled(latency, bytesWritten, sampleLatency, failure, statusCode)
}

func (s *statsShard) recordFailureSampled(latency time.Duration, bytesWritten int, sampleLatency bool, failure FailureKind, statusCode int) {
	if latency < 0 {
		latency = 0
	}
	s.totalRequests.Add(1)
	s.failedRequests.Add(1)
	s.recordFailureKind(failure)
	s.recordStatusCode(statusCode)
	if bytesWritten > 0 {
		s.bytesWritten.Add(uint64(bytesWritten))
	}
	if !sampleLatency {
		return
	}
	latencyNano := uint64(latency.Nanoseconds())
	s.latencySamples.Add(1)
	s.totalLatencyNano.Add(latencyNano)
	s.latencyBuckets[latencyBucketIndex(latencyNano)].Add(1)
	s.recordMinLatency(latencyNano)
	s.recordMaxLatency(latencyNano)
}

func (s *statsShard) recordStatusCode(statusCode int) {
	if statusCode >= firstTrackedHTTPStatus && statusCode < HTTPStatusCount {
		s.statusCodes[statusCode-firstTrackedHTTPStatus].Add(1)
	}
}

func (s *statsShard) recordFailureKind(failure FailureKind) {
	switch failure {
	case FailureTimeout:
		s.timeoutFailures.Add(1)
	case FailureDNS:
		s.dnsFailures.Add(1)
	case FailureTLS:
		s.tlsFailures.Add(1)
	case FailureConnectionRefused:
		s.connRefused.Add(1)
	default:
		s.otherFailures.Add(1)
	}
}

func (s *statsShard) RecordAssertionFailure() {
	s.assertionFailures.Add(1)
}

func (s *statsShard) RecordCaptureFailure() {
	s.captureFailures.Add(1)
	s.RecordAssertionFailure()
}

func (s *statsShard) RecordTemplateFailure() {
	s.templateFailures.Add(1)
	s.RecordAssertionFailure()
}

func latencyBucketIndex(latencyNano uint64) int {
	low, high := 0, LatencyBucketCount
	for low < high {
		middle := int(uint(low+high) >> 1)
		if latencyNano <= latencyBucketUpperBoundsNano[middle] {
			high = middle
		} else {
			low = middle + 1
		}
	}
	if low == LatencyBucketCount {
		return LatencyBucketCount - 1
	}
	return low
}

func buildLatencyBucketUpperBounds() [LatencyBucketCount]uint64 {
	var bounds [LatencyBucketCount]uint64
	bounds[0] = LatencyHistogramMinNano
	ratio := math.Pow(
		float64(LatencyHistogramMaxNano)/float64(LatencyHistogramMinNano),
		1/float64(latencyHistogramFiniteBucketCount-1),
	)
	for index := 1; index < latencyHistogramFiniteBucketCount-1; index++ {
		next := uint64(math.Ceil(float64(LatencyHistogramMinNano) * math.Pow(ratio, float64(index))))
		if next <= bounds[index-1] {
			next = bounds[index-1] + 1
		}
		bounds[index] = next
	}
	bounds[latencyHistogramFiniteBucketCount-1] = LatencyHistogramMaxNano
	bounds[LatencyBucketCount-1] = math.MaxUint64
	return bounds
}

func (s *statsShard) recordMinLatency(next uint64) {
	for {
		current := s.minLatencyNano.Load()
		if next >= current || s.minLatencyNano.CompareAndSwap(current, next) {
			return
		}
	}
}

func (s *statsShard) recordMaxLatency(next uint64) {
	for {
		current := s.maxLatencyNano.Load()
		if next <= current || s.maxLatencyNano.CompareAndSwap(current, next) {
			return
		}
	}
}
