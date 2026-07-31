package engine

import (
	"strings"
	"testing"
)

func TestCompiledTemplateRendersExactBytesAndRejectsMissingIterationValue(t *testing.T) {
	template, names, err := compileTemplateBytes([]byte(
		"prefix/{{ token }}/{{token}}{{suffix}}/tail",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "token" || names[1] != "suffix" {
		t.Fatalf("unexpected template names: %v", names)
	}
	variables := newWorkerVariables()
	variables.iteration["token"] = "fresh"
	variables.iteration["suffix"] = "-value"

	rendered, err := variables.render(template)
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != "prefix/fresh/fresh-value/tail" {
		t.Fatalf("unexpected rendered template: %s", rendered)
	}
	variables.iteration["token"] = "x"
	variables.iteration["suffix"] = ""
	rendered, err = variables.render(template)
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != "prefix/x/x/tail" {
		t.Fatalf("render buffer retained stale bytes: %s", rendered)
	}

	variables.beginIteration()
	if _, err := variables.render(template); err == nil ||
		!strings.Contains(err.Error(), `template variable "token" is unavailable`) {
		t.Fatalf("expected missing iteration value, got %v", err)
	}
}

func TestCompileTemplateKeepsLiteralPathStatic(t *testing.T) {
	template, names, err := compileTemplateBytes([]byte("/items/%7B%7Btoken%7D%7D"))
	if err != nil {
		t.Fatal(err)
	}
	if template.dynamic() || len(names) != 0 {
		t.Fatalf("encoded literal braces must remain static: template=%+v names=%v", template, names)
	}
}

func TestAcquireRequestRendersAllTemplateFieldsWithoutAliasing(t *testing.T) {
	engine, err := New(Config{
		URL:             "http://example.com/session",
		MaxConnsPerHost: DefaultMaxConnsPerHost,
		ScenarioSteps: []ScenarioStep{
			{
				Kind:     StepRequest,
				URL:      "http://example.com/session",
				Captures: []VariableCapture{{Name: "token", Path: "token"}},
			},
			{
				Kind:   StepRequest,
				URL:    "http://example.com/items/{{token}}?next={{ token }}",
				Method: "POST",
				Headers: []Header{
					{Name: "Authorization", Value: "Bearer {{token}}"},
					{Name: "X-Token", Value: "prefix-{{ token }}-suffix"},
				},
				Body: []byte(`{"token":"{{token}}"}`),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	variables := newWorkerVariables()
	variables.iteration["token"] = "value-123"

	req, err := engine.acquireRequest(engine.cfg.steps[1].request, variables)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.releaseRequest(req)

	if got := string(req.RequestURI()); got != "/items/value-123?next=value-123" {
		t.Fatalf("unexpected request URI: %q", got)
	}
	if got := string(req.Header.Peek("Authorization")); got != "Bearer value-123" {
		t.Fatalf("unexpected authorization header: %q", got)
	}
	if got := string(req.Header.Peek("X-Token")); got != "prefix-value-123-suffix" {
		t.Fatalf("unexpected X-Token header: %q", got)
	}
	if got := string(req.Body()); got != `{"token":"value-123"}` {
		t.Fatalf("unexpected request body: %q", got)
	}
}

func TestReleaseRenderBufferDropsOversizedCapacity(t *testing.T) {
	variables := newWorkerVariables()
	variables.renderBuffer = make([]byte, 0, maxRetainedRenderBufferBytes+1)
	variables.releaseRenderBuffer()
	if variables.renderBuffer != nil {
		t.Fatal("oversized render buffer should be released")
	}

	variables.renderBuffer = make([]byte, 0, maxRetainedRenderBufferBytes)
	variables.releaseRenderBuffer()
	if cap(variables.renderBuffer) != maxRetainedRenderBufferBytes {
		t.Fatal("bounded render buffer should be retained")
	}
}
