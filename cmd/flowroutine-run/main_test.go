package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flowroutine/internal/bridge"
	"flowroutine/internal/headless"
)

func TestRunCLIProducesJSONAndJUnitReports(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	scenarioPath := writeScenarioFile(t, cliScenario(server.URL))
	junitPath := filepath.Join(t.TempDir(), "report.xml")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(
		context.Background(),
		[]string{"-junit-report", junitPath, scenarioPath},
		&stdout,
		&stderr,
		os.LookupEnv,
	)
	if code != exitSuccess {
		t.Fatalf("got exit %d, stderr=%s", code, stderr.String())
	}
	var report headless.RunReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode JSON report: %v\n%s", err, stdout.String())
	}
	if report.Summary.TotalRequests == 0 || report.QualityGate.Status != headless.QualityGatePass {
		t.Fatalf("unexpected report: %+v", report)
	}
	junit, err := os.ReadFile(junitPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(junit), "scenario_execution") {
		t.Fatalf("unexpected JUnit report: %s", junit)
	}
}

func TestRunCLIUsesEnvironmentBindingsWithoutLeakingValues(t *testing.T) {
	const secret = "cli-secret-must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+secret {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	file := cliScenario(server.URL)
	file.Scenario.Config.Headers = []bridge.Header{{Name: "Authorization", Value: "Bearer {{SECRET_TOKEN}}"}}
	file.Scenario.RequiredRuntimeBindings = []string{"SECRET_TOKEN"}
	scenarioPath := writeScenarioFile(t, file)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(
		context.Background(),
		[]string{"-bind-env", "SECRET_TOKEN=FLOWROUTINE_TEST_TOKEN", scenarioPath},
		&stdout,
		&stderr,
		func(name string) (string, bool) {
			if name == "FLOWROUTINE_TEST_TOKEN" {
				return secret, true
			}
			return "", false
		},
	)
	if code != exitSuccess {
		t.Fatalf("got exit %d, stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatal("CLI output exposed a runtime binding value")
	}
}

func TestRunCLIUsesDistinctExitCodes(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(path, []byte(`{"schemaVersion":99}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := runCLI(context.Background(), []string{path}, &stdout, &stderr, os.LookupEnv)
		if code != exitValidation {
			t.Fatalf("got exit %d, want %d", code, exitValidation)
		}
	})

	t.Run("slo", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		file := cliScenario(server.URL)
		minimumRPS := 1_000_000_000.0
		file.Scenario.QualityGate.MinRPS = &minimumRPS
		path := writeScenarioFile(t, file)
		junitPath := filepath.Join(t.TempDir(), "failed.xml")
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := runCLI(
			context.Background(),
			[]string{"-junit-report", junitPath, path},
			&stdout,
			&stderr,
			os.LookupEnv,
		)
		if code != exitSLO {
			t.Fatalf("got exit %d, want %d; stderr=%s", code, exitSLO, stderr.String())
		}
		junit, err := os.ReadFile(junitPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(junit), "failure") || !strings.Contains(stderr.String(), "SLO failed") {
			t.Fatalf("missing SLO failure output: %s / %s", junit, stderr.String())
		}
	})

	t.Run("execution", func(t *testing.T) {
		file := cliScenario("http://127.0.0.1:8080")
		path := writeScenarioFile(t, file)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := runCLI(ctx, []string{path}, &stdout, &stderr, os.LookupEnv)
		if code != exitExecution {
			t.Fatalf("got exit %d, want %d; stderr=%s", code, exitExecution, stderr.String())
		}
	})
}

func TestResolveFileBindingStripsOneLineEnding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("value\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bindings, err := resolveBindings(nil, []string{"SECRET_TOKEN=" + path}, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	if bindings["SECRET_TOKEN"] != "value" {
		t.Fatalf("got binding %q", bindings["SECRET_TOKEN"])
	}
}

func TestRunCLIRefusesToOverwriteScenario(t *testing.T) {
	path := writeScenarioFile(t, cliScenario("http://127.0.0.1:8080"))
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI(
		context.Background(),
		[]string{"-json-report", path, path},
		&stdout,
		&stderr,
		os.LookupEnv,
	)
	if code != exitValidation || !strings.Contains(stderr.String(), "must not overwrite") {
		t.Fatalf("got exit %d, stderr=%s", code, stderr.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatal("scenario file changed")
	}
}

func cliScenario(target string) *headless.ScenarioFile {
	zero := 0.0
	return &headless.ScenarioFile{
		SchemaVersion: headless.ScenarioSchemaVersion,
		Scenario: &headless.Scenario{
			ID:       "cli-smoke",
			Name:     "CLI smoke",
			Revision: 1,
			Config: bridge.LoadConfig{
				URL:               target,
				Method:            http.MethodGet,
				VirtualUsers:      1,
				DurationMS:        int64((75 * time.Millisecond) / time.Millisecond),
				RequestTimeoutMS:  int64(time.Second / time.Millisecond),
				MaxConnsPerHost:   2,
				ReadBufferSize:    bridge.DefaultReadBufferBytes,
				WriteBufferSize:   bridge.DefaultWriteBufferBytes,
				MaxResponseBytes:  bridge.DefaultResponseLimit,
				LatencySampleRate: 1,
				RateLimitRPS:      20,
			},
			BatchIntervalMS: 100,
			QualityGate: &headless.QualityGateConfig{
				MaxFailureRatePct: &zero,
				MaxP95LatencyMS:   &zero,
				MaxP99LatencyMS:   &zero,
				MinRPS:            &zero,
			},
			RequiredRuntimeBindings: []string{},
		},
	}
}

func writeScenarioFile(t *testing.T, file *headless.ScenarioFile) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.json")
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
