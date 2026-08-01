package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExecutionPlanRunsDeterministicWeightedBranchesAndReconcilesMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	config := Config{
		URL: server.URL,
		ScenarioSteps: []ScenarioStep{
			{ID: "entry", Kind: StepRequest, URL: server.URL + "/entry"},
			{ID: "route-a", Kind: StepRequest, URL: server.URL + "/a"},
			{ID: "route-b", Kind: StepRequest, URL: server.URL + "/b"},
		},
		ExecutionPlan: &ExecutionPlan{
			SchemaVersion: ExecutionPlanSchemaVersion,
			EntryStepID:   "entry",
			Steps: []ExecutionPlanStep{
				{ID: "entry", Kind: ExecutionStepScenario, NextStepID: "branch"},
				{
					ID: "branch", Kind: ExecutionStepBranch, JoinStepID: "join",
					Routes: []ExecutionRoute{
						{ID: "a", Name: "Route A", TargetStepID: "route-a", Weight: 1},
						{ID: "b", Name: "Route B", TargetStepID: "route-b", Weight: 3},
					},
				},
				{ID: "route-a", Kind: ExecutionStepScenario, NextStepID: "join"},
				{ID: "route-b", Kind: ExecutionStepScenario, NextStepID: "join"},
				{ID: "join", Kind: ExecutionStepJoin},
			},
		},
	}
	loadEngine, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	loadEngine.stats.Reset(time.Now())
	runtime := loadEngine.newWorkerRuntime(0)
	for range 100 {
		if !loadEngine.runIteration(context.Background(), &runtime) {
			t.Fatal("iteration was canceled")
		}
	}

	snapshot := loadEngine.Snapshot()
	routes := loadEngine.BranchRouteSnapshots()
	if snapshot.TotalRequests != 200 {
		t.Fatalf("got %d total requests, want 200", snapshot.TotalRequests)
	}
	if len(routes) != 2 {
		t.Fatalf("got %d route snapshots, want 2", len(routes))
	}
	var selections, total, success, failed uint64
	for _, route := range routes {
		selections += route.Selections
		total += route.Total
		success += route.Success
		failed += route.Failed
	}
	if selections != 100 || total != 100 || success != 100 || failed != 0 {
		t.Fatalf("branch metrics did not reconcile: %+v", routes)
	}

	compiled, err := compileConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	first := selectExecutionRoute(&compiled.executionPlan.steps[1], 7, 42).id
	second := selectExecutionRoute(&compiled.executionPlan.steps[1], 7, 42).id
	if first != second {
		t.Fatalf("deterministic selection changed from %q to %q", first, second)
	}
}

func TestExecutionPlanRunsBoundedLoopAndRejectsUnboundedCycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	config := Config{
		URL: server.URL,
		ScenarioSteps: []ScenarioStep{
			{ID: "entry", Kind: StepRequest, URL: server.URL + "/entry"},
			{ID: "body", Kind: StepRequest, URL: server.URL + "/body"},
		},
		ExecutionPlan: &ExecutionPlan{
			SchemaVersion: ExecutionPlanSchemaVersion,
			EntryStepID:   "entry",
			Steps: []ExecutionPlanStep{
				{ID: "entry", Kind: ExecutionStepScenario, NextStepID: "loop"},
				{ID: "loop", Kind: ExecutionStepLoop, BodyStepID: "body", MaxIterations: 3},
				{ID: "body", Kind: ExecutionStepScenario, NextStepID: "loop"},
			},
		},
	}
	loadEngine, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	loadEngine.stats.Reset(time.Now())
	runtime := loadEngine.newWorkerRuntime(0)
	if !loadEngine.runIteration(context.Background(), &runtime) {
		t.Fatal("iteration was canceled")
	}
	if total := loadEngine.Snapshot().TotalRequests; total != 4 {
		t.Fatalf("got %d requests, want entry plus three loop requests", total)
	}

	config.ExecutionPlan = &ExecutionPlan{
		SchemaVersion: ExecutionPlanSchemaVersion,
		EntryStepID:   "entry",
		Steps: []ExecutionPlanStep{
			{ID: "entry", Kind: ExecutionStepScenario, NextStepID: "body"},
			{ID: "body", Kind: ExecutionStepScenario, NextStepID: "entry"},
		},
	}
	if err := ValidateConfig(config); err == nil || !strings.Contains(err.Error(), "unbounded cycle") {
		t.Fatalf("got %v, want unbounded cycle error", err)
	}
}

func TestExecutionPlanRejectsAssertionWithoutDominatingRequest(t *testing.T) {
	config := Config{
		URL: "http://example.com",
		ScenarioSteps: []ScenarioStep{
			{ID: "entry", Kind: StepRequest, URL: "http://example.com/entry"},
			{ID: "a", Kind: StepRequest, URL: "http://example.com/a"},
			{ID: "b", Kind: StepRequest, URL: "http://example.com/b"},
			{ID: "assert", Kind: StepAssertStatus, ExpectedStatus: "2xx"},
		},
		ExecutionPlan: &ExecutionPlan{
			SchemaVersion: ExecutionPlanSchemaVersion,
			EntryStepID:   "entry",
			Steps: []ExecutionPlanStep{
				{ID: "entry", Kind: ExecutionStepScenario, NextStepID: "branch"},
				{ID: "branch", Kind: ExecutionStepBranch, JoinStepID: "join", Routes: []ExecutionRoute{
					{ID: "a", TargetStepID: "a", Weight: 1},
					{ID: "b", TargetStepID: "b", Weight: 1},
				}},
				{ID: "a", Kind: ExecutionStepScenario, NextStepID: "join"},
				{ID: "b", Kind: ExecutionStepScenario, NextStepID: "join"},
				{ID: "join", Kind: ExecutionStepJoin, NextStepID: "assert"},
				{ID: "assert", Kind: ExecutionStepScenario, RequestStepID: "a"},
			},
		},
	}
	if err := ValidateConfig(config); err == nil || !strings.Contains(err.Error(), "every path") {
		t.Fatalf("got %v, want dominating request error", err)
	}
}

func TestBranchVariablesAreRouteLocalAndRunScoped(t *testing.T) {
	variables := newWorkerVariables()
	capture := compiledVariableCapture{name: "token", scope: VariableScopeRun}
	variables.pushBranch("branch:a")
	variables.set(capture, "a-token")
	variables.popBranch()
	if _, exists := variables.value("token"); exists {
		t.Fatal("branch variable leaked after join")
	}
	variables.pushBranch("branch:b")
	if _, exists := variables.value("token"); exists {
		t.Fatal("branch variable leaked into another route")
	}
	variables.popBranch()
	variables.pushBranch("branch:a")
	if value, exists := variables.value("token"); !exists || value != "a-token" {
		t.Fatalf("route run variable was not retained: %q, %v", value, exists)
	}
}
