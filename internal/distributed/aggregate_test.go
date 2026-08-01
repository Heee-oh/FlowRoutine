package distributed

import (
	"net/http"
	"testing"
	"time"

	"flowroutine/internal/engine"
)

func TestAggregateSnapshotsMergesExactHistogramsAndStepMetrics(t *testing.T) {
	first := snapshotFixture(2, 200, 10)
	second := snapshotFixture(3, 500, 20)
	startedAt := time.Unix(10, 0)
	now := startedAt.Add(time.Second)
	aggregate, steps, branches := aggregateSnapshots(startedAt, now, []SnapshotResponse{first, second})

	if aggregate.TotalRequests != 5 || aggregate.SuccessRequests != 5 || aggregate.LatencySamples != 5 {
		t.Fatalf("unexpected aggregate counters: %+v", aggregate)
	}
	if aggregate.LatencyBuckets[10] != 2 || aggregate.LatencyBuckets[20] != 3 {
		t.Fatalf("histogram was not merged exactly: %+v", aggregate.LatencyBuckets)
	}
	if aggregate.P95LatencyNano != engine.PercentileLatencyNano(&aggregate.LatencyBuckets, 5, 0.95) {
		t.Fatal("aggregate percentile was not recomputed from merged buckets")
	}
	if len(steps) != 1 || steps[0].TotalRequests != 5 || steps[0].LatencyBuckets[20] != 3 {
		t.Fatalf("request-step metrics were not merged: %+v", steps)
	}
	if len(steps[0].StatusCodes) != 2 || steps[0].StatusCodes[0].Count != 2 || steps[0].StatusCodes[1].Count != 3 {
		t.Fatalf("request-step statuses were not merged: %+v", steps[0].StatusCodes)
	}
	if len(branches) != 1 || branches[0].Selections != 5 || branches[0].Total != 5 {
		t.Fatalf("branch-route metrics were not merged: %+v", branches)
	}
}

func TestValidateMonotonicSnapshotRejectsCounterRegression(t *testing.T) {
	previous := snapshotFixture(3, 200, 10)
	current := snapshotFixture(2, 200, 10)
	if err := validateMonotonicSnapshot(previous, current); err == nil {
		t.Fatal("counter regression should be rejected")
	}
}

func TestValidateSnapshotResponseRejectsInconsistentBranchRouteTotals(t *testing.T) {
	response := snapshotFixture(2, http.StatusOK, 20)
	response.BranchRoutes[0].Failed = 1
	if err := validateSnapshotResponse(response); err == nil {
		t.Fatal("expected inconsistent branch-route metrics to be rejected")
	}
}

func snapshotFixture(requests uint64, statusCode int, bucket int) SnapshotResponse {
	var snapshot engine.Snapshot
	snapshot.TotalRequests = requests
	snapshot.SuccessRequests = requests
	snapshot.LatencySamples = requests
	snapshot.TotalLatencyNano = requests * uint64(time.Millisecond)
	snapshot.MinLatencyNano = uint64(time.Millisecond)
	snapshot.MaxLatencyNano = uint64(time.Millisecond)
	snapshot.LatencyBuckets[bucket] = requests
	snapshot.StatusCodes[statusCode] = requests
	step := engine.RequestStepSnapshot{
		ID:               "request",
		Name:             "request",
		TotalRequests:    requests,
		SuccessRequests:  requests,
		LatencySamples:   requests,
		TotalLatencyNano: requests * uint64(time.Millisecond),
		MinLatencyNano:   uint64(time.Millisecond),
		MaxLatencyNano:   uint64(time.Millisecond),
		StatusCodes:      []engine.StepStatusCodeCount{{Code: statusCode, Count: requests}},
	}
	step.LatencyBuckets[bucket] = requests
	return SnapshotResponse{
		Status:       StatusResponse{ProtocolVersion: ProtocolVersion, WorkerID: "worker", RunID: "run", State: WorkerRunning},
		Snapshot:     snapshot,
		RequestSteps: []engine.RequestStepSnapshot{step},
		BranchRoutes: []engine.BranchRouteSnapshot{{
			BranchID: "branch", RouteID: "route", Name: "Route",
			Selections: requests, Total: requests, Success: requests,
		}},
	}
}
