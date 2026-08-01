package headless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"flowroutine/internal/bridge"
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
