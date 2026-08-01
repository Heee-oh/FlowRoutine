package distributed

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"flowroutine/internal/engine"
)

func TestExecutionPlanRoundTripsConfigWithoutPersistingRuntimeBindings(t *testing.T) {
	cfg := engine.Config{
		URL:               "http://example.com",
		Method:            "POST",
		Headers:           []engine.Header{{Name: "X-Test", Value: "value"}},
		Body:              []byte(`{"ok":true}`),
		VirtualUsers:      7,
		Duration:          2 * time.Second,
		RequestTimeout:    300 * time.Millisecond,
		MaxConnsPerHost:   20,
		ReadBufferSize:    1024,
		WriteBufferSize:   2048,
		MaxResponseBytes:  4096,
		LatencySampleRate: 3,
		RateLimitRPS:      99,
		RampUp:            time.Second,
		RuntimeVariables:  map[string]string{"SECRET_TOKEN": "must-not-persist"},
		Profile: &engine.LoadProfile{
			Mode:         engine.LoadModeRampingVUs,
			StartTarget:  1,
			GracefulStop: time.Second,
			Stages:       []engine.LoadStage{{Duration: time.Second, Target: 7}},
		},
		ScenarioSteps: []engine.ScenarioStep{{
			ID:      "request",
			Name:    "request",
			Kind:    engine.StepRequest,
			URL:     "http://example.com/items",
			Method:  "POST",
			Headers: []engine.Header{{Name: "Authorization", Value: "Bearer {{SECRET_TOKEN}}"}},
			Body:    []byte(`{"token":"{{SECRET_TOKEN}}"}`),
		}},
	}
	plan := NewExecutionPlan("scenario", 3, cfg)
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("must-not-persist")) {
		t.Fatal("execution plan persisted a runtime binding")
	}
	if err := plan.validate(map[string]string{"SECRET_TOKEN": "runtime"}); err != nil {
		t.Fatal(err)
	}
	roundTrip := plan.Config.EngineConfig(map[string]string{"SECRET_TOKEN": "runtime"})
	if !reflect.DeepEqual(roundTrip.ScenarioSteps, cfg.ScenarioSteps) ||
		!reflect.DeepEqual(roundTrip.Profile, cfg.Profile) ||
		roundTrip.Duration != cfg.Duration || roundTrip.RampUp != cfg.RampUp {
		t.Fatalf("config changed during protocol conversion: %+v", roundTrip)
	}
	if roundTrip.RuntimeVariables["SECRET_TOKEN"] != "runtime" {
		t.Fatalf("runtime binding was not attached at execution: %+v", roundTrip.RuntimeVariables)
	}
}

func TestExecutionPlanRequiresVersionRevisionAndRuntimeBindings(t *testing.T) {
	plan := NewExecutionPlan("scenario", 1, engine.Config{
		URL: "http://example.com",
		ScenarioSteps: []engine.ScenarioStep{{
			Kind:    engine.StepRequest,
			URL:     "http://example.com",
			Headers: []engine.Header{{Name: "Authorization", Value: "{{SECRET_TOKEN}}"}},
		}},
	})
	if err := plan.validate(nil); err == nil {
		t.Fatal("missing runtime binding should fail plan validation")
	}
	plan.SchemaVersion++
	if err := plan.validate(map[string]string{"SECRET_TOKEN": "value"}); err == nil {
		t.Fatal("unsupported plan schema should fail validation")
	}
}
