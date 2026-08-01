package distributed

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"flowroutine/internal/engine"
)

type WorkerHealth struct {
	ID                 string      `json:"id"`
	Reachable          bool        `json:"reachable"`
	Stale              bool        `json:"stale"`
	State              WorkerState `json:"state"`
	LastError          string      `json:"lastError,omitempty"`
	LastSeenUnixNano   int64       `json:"lastSeenUnixNano,omitempty"`
	ClockOffsetNano    int64       `json:"clockOffsetNano"`
	ClockRoundTripNano int64       `json:"clockRoundTripNano"`
	ScheduledUnixNano  int64       `json:"scheduledUnixNano,omitempty"`
	StartedUnixNano    int64       `json:"startedUnixNano,omitempty"`
	StartSkewNano      int64       `json:"startSkewNano,omitempty"`
	StoppedUnixNano    int64       `json:"stoppedUnixNano,omitempty"`
}

type AggregateResult struct {
	RunID        string                       `json:"runId"`
	PlanID       string                       `json:"planId"`
	PlanRevision uint64                       `json:"planRevision"`
	Partial      bool                         `json:"partial"`
	AllTerminal  bool                         `json:"allTerminal"`
	Snapshot     engine.Snapshot              `json:"snapshot"`
	RequestSteps []engine.RequestStepSnapshot `json:"requestSteps,omitempty"`
	Workers      []WorkerHealth               `json:"workers"`
}

type Run struct {
	id        string
	planID    string
	revision  uint64
	startedAt time.Time
	now       func() time.Time
	workers   []runWorker

	snapshotMu sync.Mutex
	mu         sync.Mutex
	steps      []stepDescriptor
}

type runWorker struct {
	client      *WorkerClient
	clockOffset time.Duration
	roundTrip   time.Duration
	last        SnapshotResponse
	hasSnapshot bool
	health      WorkerHealth
}

type stepDescriptor struct {
	id   string
	name string
}

func (run *Run) ID() string {
	return run.id
}

func (run *Run) Snapshot(ctx context.Context) AggregateResult {
	if ctx == nil {
		ctx = context.Background()
	}
	run.snapshotMu.Lock()
	defer run.snapshotMu.Unlock()

	responses := make([]SnapshotResponse, len(run.workers))
	errorsByIndex := make([]error, len(run.workers))
	var wait sync.WaitGroup
	for index := range run.workers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			responses[index], errorsByIndex[index] = run.workers[index].client.Snapshot(ctx, run.id)
		}(index)
	}
	wait.Wait()

	now := run.now()
	run.mu.Lock()
	defer run.mu.Unlock()
	for index := range run.workers {
		worker := &run.workers[index]
		if errorsByIndex[index] != nil {
			worker.health.Reachable = false
			worker.health.Stale = true
			worker.health.LastError = errorsByIndex[index].Error()
			continue
		}
		if err := validateSnapshotResponse(responses[index]); err != nil {
			worker.health.Reachable = true
			worker.health.Stale = true
			worker.health.LastError = err.Error()
			worker.health.LastSeenUnixNano = now.UnixNano()
			continue
		}
		if worker.hasSnapshot {
			if err := validateMonotonicSnapshot(worker.last, responses[index]); err != nil {
				worker.health.Reachable = true
				worker.health.Stale = true
				worker.health.LastError = err.Error()
				worker.health.LastSeenUnixNano = now.UnixNano()
				continue
			}
		}
		if err := run.validateStepDescriptors(responses[index].RequestSteps); err != nil {
			worker.health.Reachable = true
			worker.health.Stale = true
			worker.health.LastError = err.Error()
			worker.health.LastSeenUnixNano = now.UnixNano()
			continue
		}
		worker.last = responses[index]
		worker.hasSnapshot = true
		worker.health.Reachable = true
		worker.health.Stale = false
		run.applyStatus(worker, responses[index].Status)
		worker.health.LastSeenUnixNano = now.UnixNano()
	}
	return run.resultLocked(now)
}

func (run *Run) Stop(ctx context.Context) (AggregateResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	errorsByIndex := make([]error, len(run.workers))
	statuses := make([]StatusResponse, len(run.workers))
	var wait sync.WaitGroup
	for index := range run.workers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			statuses[index], errorsByIndex[index] = run.workers[index].client.Stop(ctx, StopRequest{
				ProtocolVersion: ProtocolVersion,
				RunID:           run.id,
			})
		}(index)
	}
	wait.Wait()

	now := run.now()
	run.mu.Lock()
	for index := range run.workers {
		worker := &run.workers[index]
		if errorsByIndex[index] != nil {
			worker.health.Reachable = false
			worker.health.Stale = true
			worker.health.LastError = errorsByIndex[index].Error()
			continue
		}
		worker.health.Reachable = true
		run.applyStatus(worker, statuses[index])
		worker.health.LastSeenUnixNano = now.UnixNano()
	}
	run.mu.Unlock()

	result := run.Snapshot(ctx)
	var joined []error
	for index, err := range errorsByIndex {
		if err != nil {
			joined = append(joined, fmt.Errorf("worker %s: %w", run.workers[index].client.ID(), err))
		}
	}
	return result, errors.Join(joined...)
}

func (run *Run) applyStatus(worker *runWorker, status StatusResponse) {
	worker.health.State = status.State
	worker.health.LastError = status.Error
	worker.health.ScheduledUnixNano = run.startedAt.UnixNano()
	if status.StartedAtNano != 0 {
		startedAt := time.Unix(0, status.StartedAtNano).Add(-worker.clockOffset)
		worker.health.StartedUnixNano = startedAt.UnixNano()
		worker.health.StartSkewNano = startedAt.Sub(run.startedAt).Nanoseconds()
	}
	if status.StoppedAtNano != 0 {
		worker.health.StoppedUnixNano = time.Unix(0, status.StoppedAtNano).Add(-worker.clockOffset).UnixNano()
	}
}

func (run *Run) resultLocked(now time.Time) AggregateResult {
	responses := make([]SnapshotResponse, 0, len(run.workers))
	workers := make([]WorkerHealth, len(run.workers))
	partial := false
	allTerminal := true
	for index := range run.workers {
		worker := &run.workers[index]
		workers[index] = worker.health
		if worker.hasSnapshot {
			responses = append(responses, worker.last)
		}
		if !worker.health.Reachable || worker.health.Stale || worker.health.State == WorkerFailed {
			partial = true
		}
		if !worker.health.State.terminal() {
			allTerminal = false
		}
	}
	snapshot, steps := aggregateSnapshots(run.startedAt, now, responses)
	return AggregateResult{
		RunID:        run.id,
		PlanID:       run.planID,
		PlanRevision: run.revision,
		Partial:      partial,
		AllTerminal:  allTerminal,
		Snapshot:     snapshot,
		RequestSteps: steps,
		Workers:      workers,
	}
}

func (run *Run) validateStepDescriptors(steps []engine.RequestStepSnapshot) error {
	if run.steps == nil {
		run.steps = make([]stepDescriptor, len(steps))
		for index, step := range steps {
			run.steps[index] = stepDescriptor{id: step.ID, name: step.Name}
		}
		return nil
	}
	if len(run.steps) != len(steps) {
		return errors.New("worker returned mismatched request-step metrics")
	}
	for index, step := range steps {
		if run.steps[index].id != step.ID || run.steps[index].name != step.Name {
			return errors.New("worker returned mismatched request-step metrics")
		}
	}
	return nil
}

func validateSnapshotResponse(response SnapshotResponse) error {
	if !response.Status.State.valid() {
		return errors.New("worker returned an invalid state")
	}
	if response.Snapshot.SuccessRequests > response.Snapshot.TotalRequests ||
		response.Snapshot.FailedRequests > response.Snapshot.TotalRequests ||
		response.Snapshot.LatencySamples > response.Snapshot.TotalRequests {
		return errors.New("worker returned inconsistent cumulative metrics")
	}
	if bucketTotal(response.Snapshot.LatencyBuckets[:]) > response.Snapshot.LatencySamples {
		return errors.New("worker returned inconsistent latency buckets")
	}
	if bucketTotal(response.Snapshot.StatusCodes[:]) > response.Snapshot.TotalRequests {
		return errors.New("worker returned inconsistent status-code metrics")
	}
	for _, step := range response.RequestSteps {
		if step.SuccessRequests > step.TotalRequests || step.FailedRequests > step.TotalRequests ||
			step.LatencySamples > step.TotalRequests {
			return errors.New("worker returned inconsistent request-step metrics")
		}
		if bucketTotal(step.LatencyBuckets[:]) > step.LatencySamples {
			return errors.New("worker returned inconsistent request-step latency buckets")
		}
		var statusTotal uint64
		for _, status := range step.StatusCodes {
			if status.Code < 100 || status.Code >= engine.HTTPStatusCount || math.MaxUint64-statusTotal < status.Count {
				return errors.New("worker returned invalid request-step status metrics")
			}
			statusTotal += status.Count
		}
		if statusTotal > step.TotalRequests {
			return errors.New("worker returned inconsistent request-step status metrics")
		}
	}
	return nil
}

func validateMonotonicSnapshot(previous SnapshotResponse, current SnapshotResponse) error {
	if !snapshotScalarsMonotonic(previous.Snapshot, current.Snapshot) {
		return errors.New("worker cumulative metrics moved backwards")
	}
	for index := range current.Snapshot.LatencyBuckets {
		if current.Snapshot.LatencyBuckets[index] < previous.Snapshot.LatencyBuckets[index] {
			return errors.New("worker latency histogram moved backwards")
		}
	}
	for index := range current.Snapshot.StatusCodes {
		if current.Snapshot.StatusCodes[index] < previous.Snapshot.StatusCodes[index] {
			return errors.New("worker status metrics moved backwards")
		}
	}
	if len(previous.RequestSteps) != len(current.RequestSteps) {
		return errors.New("worker request-step metrics changed shape")
	}
	for index := range current.RequestSteps {
		if !stepSnapshotMonotonic(previous.RequestSteps[index], current.RequestSteps[index]) {
			return errors.New("worker request-step metrics moved backwards")
		}
	}
	return nil
}

func snapshotScalarsMonotonic(previous engine.Snapshot, current engine.Snapshot) bool {
	return current.TotalRequests >= previous.TotalRequests &&
		current.SuccessRequests >= previous.SuccessRequests &&
		current.FailedRequests >= previous.FailedRequests &&
		current.TimeoutFailures >= previous.TimeoutFailures &&
		current.DNSFailures >= previous.DNSFailures &&
		current.TLSFailures >= previous.TLSFailures &&
		current.ConnRefused >= previous.ConnRefused &&
		current.OtherFailures >= previous.OtherFailures &&
		current.AssertionFailures >= previous.AssertionFailures &&
		assertionsMonotonic(previous.AssertionFailuresByType, current.AssertionFailuresByType) &&
		current.CaptureFailures >= previous.CaptureFailures &&
		current.TemplateFailures >= previous.TemplateFailures &&
		current.DroppedIterations >= previous.DroppedIterations &&
		current.LatencySamples >= previous.LatencySamples &&
		current.TotalLatencyNano >= previous.TotalLatencyNano &&
		current.BytesRead >= previous.BytesRead &&
		current.BytesWritten >= previous.BytesWritten
}

func stepSnapshotMonotonic(previous engine.RequestStepSnapshot, current engine.RequestStepSnapshot) bool {
	if previous.ID != current.ID || previous.Name != current.Name ||
		current.TotalRequests < previous.TotalRequests ||
		current.SuccessRequests < previous.SuccessRequests ||
		current.FailedRequests < previous.FailedRequests ||
		current.TimeoutFailures < previous.TimeoutFailures ||
		current.DNSFailures < previous.DNSFailures ||
		current.TLSFailures < previous.TLSFailures ||
		current.ConnRefused < previous.ConnRefused ||
		current.OtherFailures < previous.OtherFailures ||
		current.AssertionFailures < previous.AssertionFailures ||
		!assertionsMonotonic(previous.AssertionFailuresByType, current.AssertionFailuresByType) ||
		current.CaptureFailures < previous.CaptureFailures ||
		current.TemplateFailures < previous.TemplateFailures ||
		current.LatencySamples < previous.LatencySamples ||
		current.TotalLatencyNano < previous.TotalLatencyNano {
		return false
	}
	for index := range current.LatencyBuckets {
		if current.LatencyBuckets[index] < previous.LatencyBuckets[index] {
			return false
		}
	}
	previousStatuses := make(map[int]uint64, len(previous.StatusCodes))
	for _, status := range previous.StatusCodes {
		previousStatuses[status.Code] = status.Count
	}
	for _, status := range current.StatusCodes {
		if status.Count < previousStatuses[status.Code] {
			return false
		}
		delete(previousStatuses, status.Code)
	}
	return len(previousStatuses) == 0
}

func assertionsMonotonic(previous engine.AssertionFailureCounts, current engine.AssertionFailureCounts) bool {
	return current.Status >= previous.Status &&
		current.Header >= previous.Header &&
		current.JSON >= previous.JSON &&
		current.ResponseLatency >= previous.ResponseLatency &&
		current.StepLatency >= previous.StepLatency &&
		current.CountOnly >= previous.CountOnly
}

func aggregateSnapshots(startedAt time.Time, now time.Time, responses []SnapshotResponse) (engine.Snapshot, []engine.RequestStepSnapshot) {
	aggregate := engine.Snapshot{StartedAt: startedAt, At: now, MinLatencyNano: math.MaxUint64}
	stepOrder := make([]string, 0)
	steps := make(map[string]*engine.RequestStepSnapshot)
	for _, response := range responses {
		addSnapshot(&aggregate, response.Snapshot)
		for _, next := range response.RequestSteps {
			current, exists := steps[next.ID]
			if !exists {
				current = &engine.RequestStepSnapshot{ID: next.ID, Name: next.Name, MinLatencyNano: math.MaxUint64}
				steps[next.ID] = current
				stepOrder = append(stepOrder, next.ID)
			}
			addStepSnapshot(current, next)
		}
	}
	if aggregate.MinLatencyNano == math.MaxUint64 {
		aggregate.MinLatencyNano = 0
	}
	aggregate.P95LatencyNano = engine.PercentileLatencyNano(&aggregate.LatencyBuckets, aggregate.LatencySamples, 0.95)
	aggregate.P99LatencyNano = engine.PercentileLatencyNano(&aggregate.LatencyBuckets, aggregate.LatencySamples, 0.99)
	aggregate.P999LatencyNano = engine.PercentileLatencyNano(&aggregate.LatencyBuckets, aggregate.LatencySamples, 0.999)

	stepSnapshots := make([]engine.RequestStepSnapshot, 0, len(stepOrder))
	for _, id := range stepOrder {
		step := steps[id]
		if step.MinLatencyNano == math.MaxUint64 {
			step.MinLatencyNano = 0
		}
		step.P95LatencyNano = engine.PercentileLatencyNano(&step.LatencyBuckets, step.LatencySamples, 0.95)
		step.P99LatencyNano = engine.PercentileLatencyNano(&step.LatencyBuckets, step.LatencySamples, 0.99)
		step.P999LatencyNano = engine.PercentileLatencyNano(&step.LatencyBuckets, step.LatencySamples, 0.999)
		stepSnapshots = append(stepSnapshots, *step)
	}
	return aggregate, stepSnapshots
}

func addSnapshot(target *engine.Snapshot, next engine.Snapshot) {
	target.TotalRequests += next.TotalRequests
	target.SuccessRequests += next.SuccessRequests
	target.FailedRequests += next.FailedRequests
	target.TimeoutFailures += next.TimeoutFailures
	target.DNSFailures += next.DNSFailures
	target.TLSFailures += next.TLSFailures
	target.ConnRefused += next.ConnRefused
	target.OtherFailures += next.OtherFailures
	target.AssertionFailures += next.AssertionFailures
	addAssertionCounts(&target.AssertionFailuresByType, next.AssertionFailuresByType)
	target.CaptureFailures += next.CaptureFailures
	target.TemplateFailures += next.TemplateFailures
	target.DroppedIterations += next.DroppedIterations
	target.LatencySamples += next.LatencySamples
	target.TotalLatencyNano += next.TotalLatencyNano
	target.BytesRead += next.BytesRead
	target.BytesWritten += next.BytesWritten
	if next.LatencySamples > 0 {
		target.MinLatencyNano = min(target.MinLatencyNano, next.MinLatencyNano)
		target.MaxLatencyNano = max(target.MaxLatencyNano, next.MaxLatencyNano)
	}
	for index := range target.LatencyBuckets {
		target.LatencyBuckets[index] += next.LatencyBuckets[index]
	}
	for index := range target.StatusCodes {
		target.StatusCodes[index] += next.StatusCodes[index]
	}
}

func addStepSnapshot(target *engine.RequestStepSnapshot, next engine.RequestStepSnapshot) {
	target.TotalRequests += next.TotalRequests
	target.SuccessRequests += next.SuccessRequests
	target.FailedRequests += next.FailedRequests
	target.TimeoutFailures += next.TimeoutFailures
	target.DNSFailures += next.DNSFailures
	target.TLSFailures += next.TLSFailures
	target.ConnRefused += next.ConnRefused
	target.OtherFailures += next.OtherFailures
	target.AssertionFailures += next.AssertionFailures
	addAssertionCounts(&target.AssertionFailuresByType, next.AssertionFailuresByType)
	target.CaptureFailures += next.CaptureFailures
	target.TemplateFailures += next.TemplateFailures
	target.LatencySamples += next.LatencySamples
	target.TotalLatencyNano += next.TotalLatencyNano
	if next.LatencySamples > 0 {
		target.MinLatencyNano = min(target.MinLatencyNano, next.MinLatencyNano)
		target.MaxLatencyNano = max(target.MaxLatencyNano, next.MaxLatencyNano)
	}
	for index := range target.LatencyBuckets {
		target.LatencyBuckets[index] += next.LatencyBuckets[index]
	}
	statusCounts := make(map[int]uint64, len(target.StatusCodes)+len(next.StatusCodes))
	for _, status := range target.StatusCodes {
		statusCounts[status.Code] = status.Count
	}
	for _, status := range next.StatusCodes {
		statusCounts[status.Code] += status.Count
	}
	codes := make([]int, 0, len(statusCounts))
	for code := range statusCounts {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	target.StatusCodes = target.StatusCodes[:0]
	for _, code := range codes {
		target.StatusCodes = append(target.StatusCodes, engine.StepStatusCodeCount{Code: code, Count: statusCounts[code]})
	}
}

func addAssertionCounts(target *engine.AssertionFailureCounts, next engine.AssertionFailureCounts) {
	target.Status += next.Status
	target.Header += next.Header
	target.JSON += next.JSON
	target.ResponseLatency += next.ResponseLatency
	target.StepLatency += next.StepLatency
	target.CountOnly += next.CountOnly
}

func bucketTotal(values []uint64) uint64 {
	var total uint64
	for _, value := range values {
		if math.MaxUint64-total < value {
			return math.MaxUint64
		}
		total += value
	}
	return total
}
