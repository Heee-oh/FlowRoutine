package engine

import (
	"math"
	"sync/atomic"
	"time"
)

const (
	maxRequestStepStatsShards           = 4
	requestStepStatsMetadataBytes       = 64
	requestStepStatsShardEstimatedBytes = (21+LatencyBucketCount+trackedHTTPStatusCount)*8 + 64
)

type requestStepDescriptor struct {
	id   string
	name string
}

type atomicRequestStepStats struct {
	id     string
	name   string
	shards []requestStepStatsShard
}

type requestStepStatsShard struct {
	totalRequests              atomic.Uint64
	successRequests            atomic.Uint64
	failedRequests             atomic.Uint64
	timeoutFailures            atomic.Uint64
	dnsFailures                atomic.Uint64
	tlsFailures                atomic.Uint64
	connRefused                atomic.Uint64
	otherFailures              atomic.Uint64
	assertionFailures          atomic.Uint64
	scenarioAssertionFailures  [assertionTypeCount]atomic.Uint64
	countOnlyAssertionFailures atomic.Uint64
	captureFailures            atomic.Uint64
	templateFailures           atomic.Uint64
	latencySamples             atomic.Uint64
	totalLatencyNano           atomic.Uint64
	minLatencyNano             atomic.Uint64
	maxLatencyNano             atomic.Uint64
	latencyBuckets             [LatencyBucketCount]atomic.Uint64
	statusCodes                [trackedHTTPStatusCount]atomic.Uint64
	_                          [64]byte
}

type StepStatusCodeCount struct {
	Code  int
	Count uint64
}

type RequestStepSnapshot struct {
	ID                      string
	Name                    string
	TotalRequests           uint64
	SuccessRequests         uint64
	FailedRequests          uint64
	TimeoutFailures         uint64
	DNSFailures             uint64
	TLSFailures             uint64
	ConnRefused             uint64
	OtherFailures           uint64
	AssertionFailures       uint64
	AssertionFailuresByType AssertionFailureCounts
	CaptureFailures         uint64
	TemplateFailures        uint64
	LatencySamples          uint64
	TotalLatencyNano        uint64
	MinLatencyNano          uint64
	MaxLatencyNano          uint64
	P95LatencyNano          uint64
	P99LatencyNano          uint64
	P999LatencyNano         uint64
	StatusCodes             []StepStatusCodeCount
}

func (s *AtomicStats) initRequestSteps(virtualUsers int, descriptors []requestStepDescriptor) {
	shards := requestStepStatsShardCount(virtualUsers)
	s.requestSteps = make([]atomicRequestStepStats, len(descriptors))
	for index, descriptor := range descriptors {
		s.requestSteps[index] = atomicRequestStepStats{
			id:     descriptor.id,
			name:   descriptor.name,
			shards: make([]requestStepStatsShard, shards),
		}
		s.requestSteps[index].reset()
	}
}

func requestStepStatsShardCount(virtualUsers int) int {
	if virtualUsers < 1 {
		return 1
	}
	return min(virtualUsers, maxRequestStepStatsShards)
}

func EstimateStepMetricsBytes(requestSteps int, virtualUsers int) uint64 {
	if requestSteps <= 0 {
		return 0
	}
	perStep := uint64(requestStepStatsMetadataBytes +
		requestStepStatsShardCount(virtualUsers)*requestStepStatsShardEstimatedBytes)
	return uint64(requestSteps) * perStep
}

func (s *AtomicStats) requestStepShard(stepIndex int, workerIndex int) *requestStepStatsShard {
	if stepIndex < 0 || stepIndex >= len(s.requestSteps) {
		return nil
	}
	step := &s.requestSteps[stepIndex]
	return &step.shards[workerIndex%len(step.shards)]
}

func (s *AtomicStats) RequestStepSnapshots() []RequestStepSnapshot {
	if len(s.requestSteps) == 0 {
		return nil
	}
	snapshots := make([]RequestStepSnapshot, len(s.requestSteps))
	for index := range s.requestSteps {
		step := &s.requestSteps[index]
		snapshot := RequestStepSnapshot{
			ID:             step.id,
			Name:           step.name,
			MinLatencyNano: math.MaxUint64,
		}
		var latencyBuckets [LatencyBucketCount]uint64
		var statusCodes [trackedHTTPStatusCount]uint64
		for shardIndex := range step.shards {
			shard := &step.shards[shardIndex]
			snapshot.TotalRequests += shard.totalRequests.Load()
			snapshot.SuccessRequests += shard.successRequests.Load()
			snapshot.FailedRequests += shard.failedRequests.Load()
			snapshot.TimeoutFailures += shard.timeoutFailures.Load()
			snapshot.DNSFailures += shard.dnsFailures.Load()
			snapshot.TLSFailures += shard.tlsFailures.Load()
			snapshot.ConnRefused += shard.connRefused.Load()
			snapshot.OtherFailures += shard.otherFailures.Load()
			snapshot.AssertionFailures += shard.assertionFailures.Load()
			addAssertionFailureCounts(&snapshot.AssertionFailuresByType, shard.scenarioAssertionFailureCounts())
			snapshot.CaptureFailures += shard.captureFailures.Load()
			snapshot.TemplateFailures += shard.templateFailures.Load()
			snapshot.LatencySamples += shard.latencySamples.Load()
			snapshot.TotalLatencyNano += shard.totalLatencyNano.Load()
			for bucket := range latencyBuckets {
				latencyBuckets[bucket] += shard.latencyBuckets[bucket].Load()
			}
			for code := range statusCodes {
				statusCodes[code] += shard.statusCodes[code].Load()
			}
			if minimum := shard.minLatencyNano.Load(); minimum < snapshot.MinLatencyNano {
				snapshot.MinLatencyNano = minimum
			}
			if maximum := shard.maxLatencyNano.Load(); maximum > snapshot.MaxLatencyNano {
				snapshot.MaxLatencyNano = maximum
			}
		}
		if snapshot.MinLatencyNano == math.MaxUint64 {
			snapshot.MinLatencyNano = 0
		}
		snapshot.P95LatencyNano = PercentileLatencyNano(&latencyBuckets, snapshot.LatencySamples, 0.95)
		snapshot.P99LatencyNano = PercentileLatencyNano(&latencyBuckets, snapshot.LatencySamples, 0.99)
		snapshot.P999LatencyNano = PercentileLatencyNano(&latencyBuckets, snapshot.LatencySamples, 0.999)
		for code, count := range statusCodes {
			if count > 0 {
				snapshot.StatusCodes = append(snapshot.StatusCodes, StepStatusCodeCount{
					Code:  code + firstTrackedHTTPStatus,
					Count: count,
				})
			}
		}
		snapshots[index] = snapshot
	}
	return snapshots
}

func (s *atomicRequestStepStats) reset() {
	for index := range s.shards {
		s.shards[index].reset()
	}
}

func (s *requestStepStatsShard) reset() {
	s.totalRequests.Store(0)
	s.successRequests.Store(0)
	s.failedRequests.Store(0)
	s.timeoutFailures.Store(0)
	s.dnsFailures.Store(0)
	s.tlsFailures.Store(0)
	s.connRefused.Store(0)
	s.otherFailures.Store(0)
	s.assertionFailures.Store(0)
	for index := range s.scenarioAssertionFailures {
		s.scenarioAssertionFailures[index].Store(0)
	}
	s.countOnlyAssertionFailures.Store(0)
	s.captureFailures.Store(0)
	s.templateFailures.Store(0)
	s.latencySamples.Store(0)
	s.totalLatencyNano.Store(0)
	s.minLatencyNano.Store(math.MaxUint64)
	s.maxLatencyNano.Store(0)
	for index := range s.latencyBuckets {
		s.latencyBuckets[index].Store(0)
	}
	for index := range s.statusCodes {
		s.statusCodes[index].Store(0)
	}
}

func (s *requestStepStatsShard) RecordHTTPSuccessSampled(
	latency time.Duration,
	sampleLatency bool,
	statusCode int,
) {
	s.recordRequest(latency, sampleLatency, statusCode, true, FailureOther)
}

func (s *requestStepStatsShard) RecordHTTPFailureSampled(
	latency time.Duration,
	sampleLatency bool,
	failure FailureKind,
	statusCode int,
) {
	s.recordRequest(latency, sampleLatency, statusCode, false, failure)
}

func (s *requestStepStatsShard) recordRequest(
	latency time.Duration,
	sampleLatency bool,
	statusCode int,
	success bool,
	failure FailureKind,
) {
	if latency < 0 {
		latency = 0
	}
	s.totalRequests.Add(1)
	if success {
		s.successRequests.Add(1)
	} else {
		s.failedRequests.Add(1)
		s.recordFailureKind(failure)
	}
	if statusCode >= firstTrackedHTTPStatus && statusCode < HTTPStatusCount {
		s.statusCodes[statusCode-firstTrackedHTTPStatus].Add(1)
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

func (s *requestStepStatsShard) recordFailureKind(failure FailureKind) {
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

func (s *requestStepStatsShard) RecordAssertionFailure() {
	s.assertionFailures.Add(1)
}

func (s *requestStepStatsShard) RecordScenarioAssertionFailure(typeName AssertionType, countOnly bool) {
	if index := assertionTypeIndex(typeName); index >= 0 {
		s.scenarioAssertionFailures[index].Add(1)
	}
	if countOnly {
		s.countOnlyAssertionFailures.Add(1)
		return
	}
	s.RecordAssertionFailure()
}

func (s *requestStepStatsShard) scenarioAssertionFailureCounts() AssertionFailureCounts {
	return AssertionFailureCounts{
		Status:          s.scenarioAssertionFailures[0].Load(),
		Header:          s.scenarioAssertionFailures[1].Load(),
		JSON:            s.scenarioAssertionFailures[2].Load(),
		ResponseLatency: s.scenarioAssertionFailures[3].Load(),
		StepLatency:     s.scenarioAssertionFailures[4].Load(),
		CountOnly:       s.countOnlyAssertionFailures.Load(),
	}
}

func (s *requestStepStatsShard) RecordCaptureFailure() {
	s.captureFailures.Add(1)
	s.RecordAssertionFailure()
}

func (s *requestStepStatsShard) RecordTemplateFailure() {
	s.templateFailures.Add(1)
	s.RecordAssertionFailure()
}

func (s *requestStepStatsShard) recordMinLatency(next uint64) {
	for {
		current := s.minLatencyNano.Load()
		if next >= current || s.minLatencyNano.CompareAndSwap(current, next) {
			return
		}
	}
}

func (s *requestStepStatsShard) recordMaxLatency(next uint64) {
	for {
		current := s.maxLatencyNano.Load()
		if next <= current || s.maxLatencyNano.CompareAndSwap(current, next) {
			return
		}
	}
}
