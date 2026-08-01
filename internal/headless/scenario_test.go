package headless

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"flowroutine/internal/bridge"
)

func TestDecodeScenarioStrictAndSizeBounded(t *testing.T) {
	encoded := marshalScenario(t, validScenarioFile("http://127.0.0.1:8080"))
	decoded, err := DecodeScenario(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode valid scenario: %v", err)
	}
	if decoded.Scenario.ID != "smoke-test" {
		t.Fatalf("got scenario id %q", decoded.Scenario.ID)
	}

	unknown := bytes.Replace(encoded, []byte(`"revision":1`), []byte(`"revision":1,"unexpected":true`), 1)
	if _, err := DecodeScenario(bytes.NewReader(unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
	if _, err := DecodeScenario(strings.NewReader(string(encoded) + `{}`)); err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("expected trailing-value error, got %v", err)
	}
	if _, err := DecodeScenario(bytes.NewReader(make([]byte, MaxScenarioFileBytes+1))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size-limit error, got %v", err)
	}
}

func TestScenarioRequiresDeclaredSecretPlaceholders(t *testing.T) {
	file := validScenarioFile("http://127.0.0.1:8080?token={{SECRET_TOKEN}}")
	file.Scenario.RequiredRuntimeBindings = []string{"SECRET_TOKEN"}
	file.Scenario.Config.Headers = []bridge.Header{{Name: "Authorization", Value: "Bearer {{SECRET_TOKEN}}"}}
	file.Scenario.Config.Body = `{"password":"{{SECRET_PASSWORD}}"}`
	file.Scenario.RequiredRuntimeBindings = append(file.Scenario.RequiredRuntimeBindings, "SECRET_PASSWORD")
	if err := file.ValidateDefinition(); err != nil {
		t.Fatalf("validate secret placeholders: %v", err)
	}
	if err := file.Scenario.ValidateRuntimeBindings(map[string]string{
		"SECRET_TOKEN":    "token-value",
		"SECRET_PASSWORD": "password-value",
	}); err != nil {
		t.Fatalf("validate bindings: %v", err)
	}

	deleteBinding := map[string]string{"SECRET_TOKEN": "token-value"}
	if err := file.Scenario.ValidateRuntimeBindings(deleteBinding); err == nil || !strings.Contains(err.Error(), "SECRET_PASSWORD") {
		t.Fatalf("expected missing binding error, got %v", err)
	}
	if err := file.Scenario.ValidateRuntimeBindings(map[string]string{
		"SECRET_TOKEN":    "token-value",
		"SECRET_PASSWORD": "password-value",
		"SECRET_EXTRA":    "extra",
	}); err == nil || !strings.Contains(err.Error(), "not required") {
		t.Fatalf("expected extra binding error, got %v", err)
	}
}

func TestScenarioRejectsSensitiveLiterals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ScenarioFile)
	}{
		{
			name: "header",
			mutate: func(file *ScenarioFile) {
				file.Scenario.Config.Headers = []bridge.Header{{Name: "Authorization", Value: "Bearer literal-token"}}
			},
		},
		{
			name: "query",
			mutate: func(file *ScenarioFile) {
				file.Scenario.Config.URL += "?api_key=literal-token"
			},
		},
		{
			name: "userinfo",
			mutate: func(file *ScenarioFile) {
				file.Scenario.Config.URL = "http://user:password@127.0.0.1:8080"
			},
		},
		{
			name: "json body",
			mutate: func(file *ScenarioFile) {
				file.Scenario.Config.Body = `{"client_secret":"literal-token"}`
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := validScenarioFile("http://127.0.0.1:8080")
			test.mutate(file)
			if err := file.ValidateDefinition(); err == nil ||
				!strings.Contains(err.Error(), "sensitive literal") &&
					!strings.Contains(err.Error(), "literal username") &&
					!strings.Contains(err.Error(), "literal password") {
				t.Fatalf("expected sensitive-literal error, got %v", err)
			}
		})
	}
}

func TestScenarioRejectsUndeclaredAndUnusedBindings(t *testing.T) {
	file := validScenarioFile("http://127.0.0.1:8080?token={{SECRET_TOKEN}}")
	if err := file.ValidateDefinition(); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("expected undeclared binding error, got %v", err)
	}

	file = validScenarioFile("http://127.0.0.1:8080")
	file.Scenario.RequiredRuntimeBindings = []string{"SECRET_UNUSED"}
	if err := file.ValidateDefinition(); err == nil || !strings.Contains(err.Error(), "not used") {
		t.Fatalf("expected unused binding error, got %v", err)
	}
}

func validScenarioFile(target string) *ScenarioFile {
	zero := 0.0
	return &ScenarioFile{
		SchemaVersion: ScenarioSchemaVersion,
		Scenario: &Scenario{
			ID:       "smoke-test",
			Name:     "Smoke test",
			Revision: 1,
			Config: bridge.LoadConfig{
				URL:               target,
				Method:            "GET",
				VirtualUsers:      1,
				DurationMS:        int64((50 * time.Millisecond) / time.Millisecond),
				RequestTimeoutMS:  int64(time.Second / time.Millisecond),
				MaxConnsPerHost:   2,
				ReadBufferSize:    bridge.DefaultReadBufferBytes,
				WriteBufferSize:   bridge.DefaultWriteBufferBytes,
				MaxResponseBytes:  bridge.DefaultResponseLimit,
				LatencySampleRate: 1,
				RateLimitRPS:      20,
			},
			BatchIntervalMS: 100,
			QualityGate: &QualityGateConfig{
				MaxFailureRatePct: &zero,
				MaxP95LatencyMS:   &zero,
				MaxP99LatencyMS:   &zero,
				MinRPS:            &zero,
			},
			RequiredRuntimeBindings: []string{},
		},
	}
}

func marshalScenario(t *testing.T, file *ScenarioFile) []byte {
	t.Helper()
	encoded, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
