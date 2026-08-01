package headless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"flowroutine/internal/bridge"
	"flowroutine/internal/engine"
)

func TestRunUsesSharedEngineAndDoesNotReportRuntimeSecrets(t *testing.T) {
	const secret = "do-not-serialize-this-token"
	var authorized atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") == "Bearer "+secret {
			authorized.Store(true)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	file := validScenarioFile(server.URL)
	file.Scenario.Config.Headers = []bridge.Header{{Name: "Authorization", Value: "Bearer {{SECRET_TOKEN}}"}}
	file.Scenario.RequiredRuntimeBindings = []string{"SECRET_TOKEN"}
	report, err := Run(context.Background(), file, map[string]string{"SECRET_TOKEN": secret})
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	if report.Summary.TotalRequests == 0 || !authorized.Load() {
		t.Fatalf("expected an authorized request, report=%+v authorized=%t", report.Summary, authorized.Load())
	}
	if report.QualityGate.Status != QualityGatePass {
		t.Fatalf("expected passing disabled gates, got %+v", report.QualityGate)
	}
	encoded, err := MarshalJSONReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatal("JSON report exposed a runtime secret")
	}
	junit, err := MarshalJUnitReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(junit), "scenario_execution") || strings.Contains(string(junit), secret) {
		t.Fatalf("unexpected JUnit report: %s", junit)
	}
}

func TestRunExecutesBranchPlanAndReportsReconciledRoutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	file := validScenarioFile(server.URL)
	file.Scenario.Config.DurationMS = 200
	file.Scenario.Config.RateLimitRPS = 100
	file.Scenario.Config.ScenarioSteps = []bridge.ScenarioStep{
		{ID: "entry", Name: "Entry", Kind: "request", URL: server.URL + "/entry"},
		{ID: "a", Name: "Route A request", Kind: "request", URL: server.URL + "/a"},
		{ID: "b", Name: "Route B request", Kind: "request", URL: server.URL + "/b"},
	}
	file.Scenario.Config.ExecutionPlan = &bridge.ExecutionPlan{
		SchemaVersion: engine.ExecutionPlanSchemaVersion,
		EntryStepID:   "entry",
		Steps: []bridge.ExecutionPlanStep{
			{ID: "entry", Kind: "step", NextStepID: "branch"},
			{ID: "branch", Kind: "branch", JoinStepID: "join", Routes: []bridge.ExecutionRoute{
				{ID: "a", Name: "Route A", TargetStepID: "a", Weight: 1},
				{ID: "b", Name: "Route B", TargetStepID: "b", Weight: 1},
			}},
			{ID: "a", Kind: "step", NextStepID: "join"},
			{ID: "b", Kind: "step", NextStepID: "join"},
			{ID: "join", Kind: "join"},
		},
	}

	report, err := Run(context.Background(), file, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != ReportSchemaVersion || len(report.Summary.BranchRoutes) != 2 {
		t.Fatalf("missing branch report: %+v", report.Summary.BranchRoutes)
	}
	var selections, total, success, failed uint64
	for _, route := range report.Summary.BranchRoutes {
		selections += route.Selections
		total += route.Total
		success += route.Success
		failed += route.Failed
	}
	var routedRequests uint64
	for _, step := range report.Summary.RequestSteps {
		if step.ID == "a" || step.ID == "b" {
			routedRequests += step.Total
		}
	}
	if selections == 0 || selections < total || total != routedRequests || success != total || failed != 0 {
		t.Fatalf("branch report did not reconcile: %+v", report.Summary.BranchRoutes)
	}
}

func TestRunClassifiesValidationAndCancellation(t *testing.T) {
	invalid := validScenarioFile("not-a-url")
	if _, err := Run(context.Background(), invalid, nil); !IsValidationError(err) {
		t.Fatalf("expected validation error, got %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(cancelled, validScenarioFile("http://127.0.0.1:8080"), nil); !IsExecutionError(err) {
		t.Fatalf("expected execution error, got %v", err)
	}
}
