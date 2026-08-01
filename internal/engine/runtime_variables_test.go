package engine

import (
	"strings"
	"testing"
)

func TestRuntimeVariablesSatisfyTemplatesAndAreCloned(t *testing.T) {
	runtimeVariables := map[string]string{"SECRET_TOKEN": "first"}
	compiled, err := compileConfig(Config{
		URL:              "http://example.com",
		RuntimeVariables: runtimeVariables,
		ScenarioSteps: []ScenarioStep{{
			Kind:    StepRequest,
			URL:     "http://example.com/items",
			Headers: []Header{{Name: "Authorization", Value: "Bearer {{SECRET_TOKEN}}"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtimeVariables["SECRET_TOKEN"] = "mutated"
	variables := newWorkerVariablesWithRuntime(compiled.runtimeVariables)
	rendered, err := variables.render(compiled.steps[0].request.headers[0].template)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(rendered); got != "Bearer first" {
		t.Fatalf("got %q, want cloned runtime value", got)
	}
}

func TestRuntimeVariableValidationRejectsUnsafeBindings(t *testing.T) {
	base := Config{URL: "http://example.com"}
	base.RuntimeVariables = map[string]string{"bad name": "value"}
	if err := ValidateConfig(base); err == nil || !strings.Contains(err.Error(), "runtime variable") {
		t.Fatalf("got %v, want invalid runtime variable error", err)
	}

	base.RuntimeVariables = map[string]string{"SECRET_VALUE": strings.Repeat("x", MaxRuntimeVariableBytes+1)}
	if err := ValidateConfig(base); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("got %v, want runtime value size error", err)
	}
}

func TestRuntimeVariablesCannotBeOverwrittenByCaptures(t *testing.T) {
	err := ValidateConfig(Config{
		URL:              "http://example.com",
		RuntimeVariables: map[string]string{"token": "runtime"},
		ScenarioSteps: []ScenarioStep{{
			Kind:     StepRequest,
			URL:      "http://example.com",
			Captures: []VariableCapture{{Name: "token", Path: "token"}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "changes scope") {
		t.Fatalf("got %v, want runtime binding collision error", err)
	}
}
