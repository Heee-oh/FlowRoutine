package engine

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func TestNewRejectsInvalidTemplateAndCaptureConfigurations(t *testing.T) {
	tests := []struct {
		name     string
		steps    []ScenarioStep
		contains string
	}{
		{
			name: "unknown variable",
			steps: []ScenarioStep{{
				Kind: StepRequest,
				URL:  "http://example.com/items/{{missing}}",
			}},
			contains: `template variable "missing" is not defined by an earlier capture`,
		},
		{
			name: "unclosed template",
			steps: []ScenarioStep{{
				Kind:    StepRequest,
				URL:     "http://example.com",
				Headers: []Header{{Name: "X-Test", Value: "{{missing"}},
			}},
			contains: "contains an unclosed template",
		},
		{
			name: "invalid array path",
			steps: []ScenarioStep{{
				Kind:     StepRequest,
				URL:      "http://example.com",
				Captures: []VariableCapture{{Name: "token", Path: "items[nope].token"}},
			}},
			contains: `array index "nope" must be a non-negative integer`,
		},
		{
			name: "invalid root path segment",
			steps: []ScenarioStep{{
				Kind:     StepRequest,
				URL:      "http://example.com",
				Captures: []VariableCapture{{Name: "token", Path: "$.[0].token"}},
			}},
			contains: "path contains an empty segment",
		},
		{
			name: "unexpected closing bracket",
			steps: []ScenarioStep{{
				Kind:     StepRequest,
				URL:      "http://example.com",
				Captures: []VariableCapture{{Name: "token", Path: "data].token"}},
			}},
			contains: "unexpected closing bracket",
		},
		{
			name: "reserved variable",
			steps: []ScenarioStep{{
				Kind:     StepRequest,
				URL:      "http://example.com",
				Captures: []VariableCapture{{Name: "SECRET_TOKEN", Path: "token"}},
			}},
			contains: "SECRET_ prefix is reserved",
		},
		{
			name: "unsupported status policy",
			steps: []ScenarioStep{{
				Kind: StepRequest,
				URL:  "http://example.com",
				Captures: []VariableCapture{{
					Name:     "token",
					Path:     "token",
					OnStatus: "banana",
				}},
			}},
			contains: "status policy must be",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(Config{
				URL:             "http://example.com",
				MaxConnsPerHost: DefaultMaxConnsPerHost,
				ScenarioSteps:   test.steps,
			})
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("expected error containing %q, got %v", test.contains, err)
			}
		})
	}
}

func TestCaptureVariablesSupportsArraysScopesAndStatusPolicies(t *testing.T) {
	iterationCapture := mustCompileCapture(t, VariableCapture{
		Name:  "token",
		Path:  "$.items[1].token",
		Scope: VariableScopeIteration,
	})
	variables := newWorkerVariables()
	variables.iteration["token"] = "stale"

	if err := captureVariables(
		[]byte(`{"items":[{"token":"first"},{"token":"second"}]}`),
		fasthttp.StatusOK,
		[]compiledVariableCapture{iterationCapture},
		variables,
	); err != nil {
		t.Fatal(err)
	}
	if value, ok := variables.value("token"); !ok || value != "second" {
		t.Fatalf("expected array capture, got value=%q ok=%v", value, ok)
	}

	if err := captureVariables(
		[]byte(`{"items":[{"token":"unauthorized"}]}`),
		fasthttp.StatusUnauthorized,
		[]compiledVariableCapture{iterationCapture},
		variables,
	); err != nil {
		t.Fatal(err)
	}
	if _, ok := variables.value("token"); ok {
		t.Fatal("default success policy must invalidate an iteration capture on non-success status")
	}

	errorCapture := mustCompileCapture(t, VariableCapture{
		Name:     "error",
		Path:     "error.code",
		OnStatus: "4xx",
	})
	if err := captureVariables(
		[]byte(`{"error":{"code":"denied"}}`),
		fasthttp.StatusUnauthorized,
		[]compiledVariableCapture{errorCapture},
		variables,
	); err != nil {
		t.Fatal(err)
	}
	if value, ok := variables.value("error"); !ok || value != "denied" {
		t.Fatalf("expected explicit 4xx capture, got value=%q ok=%v", value, ok)
	}

	runCapture := mustCompileCapture(t, VariableCapture{
		Name:  "session",
		Path:  "session",
		Scope: VariableScopeRun,
	})
	if err := captureVariables(
		[]byte(`{"session":"first-session"}`),
		fasthttp.StatusOK,
		[]compiledVariableCapture{runCapture},
		variables,
	); err != nil {
		t.Fatal(err)
	}
	if err := captureVariables(
		[]byte(`not-json`),
		fasthttp.StatusOK,
		[]compiledVariableCapture{runCapture},
		variables,
	); err != nil {
		t.Fatalf("a resolved run capture must not be recaptured: %v", err)
	}
	if value, ok := variables.value("session"); !ok || value != "first-session" {
		t.Fatalf("expected run capture to persist, got value=%q ok=%v", value, ok)
	}
}

func TestCaptureVariablesInvalidatesFailedIterationAtomically(t *testing.T) {
	captures, err := compileVariableCaptures([]VariableCapture{
		{Name: "token", Path: "data.token"},
		{Name: "user", Path: "data.user.id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	variables := newWorkerVariables()
	variables.iteration["token"] = "stale-token"
	variables.iteration["user"] = "stale-user"

	err = captureVariables(
		[]byte(`{"data":{"token":"fresh-token"}}`),
		fasthttp.StatusOK,
		captures,
		variables,
	)
	if err == nil || !strings.Contains(err.Error(), `capture "user"`) {
		t.Fatalf("expected missing-path diagnostic, got %v", err)
	}
	if _, ok := variables.value("token"); ok {
		t.Fatal("a partially successful capture set must not commit")
	}
	if _, ok := variables.value("user"); ok {
		t.Fatal("a failed capture must invalidate its previous iteration value")
	}

	if err := captureVariables([]byte(`not-json`), fasthttp.StatusOK, captures, variables); err == nil ||
		!strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected invalid JSON diagnostic, got %v", err)
	}
}

func TestCaptureFailureMetricsAreClassifiedAndReset(t *testing.T) {
	var stats AtomicStats
	stats.Init(2)
	stats.Reset(time.Now())
	stats.Shard(0).RecordCaptureFailure()
	stats.Shard(1).RecordTemplateFailure()

	snapshot := stats.Snapshot(time.Now())
	if snapshot.CaptureFailures != 1 ||
		snapshot.TemplateFailures != 1 ||
		snapshot.AssertionFailures != 2 {
		t.Fatalf("unexpected classified failures: %+v", snapshot)
	}

	stats.Reset(time.Now())
	snapshot = stats.Snapshot(time.Now())
	if snapshot.CaptureFailures != 0 ||
		snapshot.TemplateFailures != 0 ||
		snapshot.AssertionFailures != 0 {
		t.Fatalf("classified failures were not reset: %+v", snapshot)
	}
}

func TestRequestURIPreservesTemplatesWithoutLosingURLPathEscaping(t *testing.T) {
	rawURL := "http://example.com/a path/{{ token }}?cursor={{nextPage}}#ignored"
	got, err := requestURI(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/a%20path/{{ token }}?cursor={{nextPage}}"; got != want {
		t.Fatalf("unexpected request URI: got %q want %q", got, want)
	}

	literalURL := "http://example.com/%7B%7Bliteral%7D%7D"
	got, err = requestURI(literalURL)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/%7B%7Bliteral%7D%7D"; got != want {
		t.Fatalf("encoded literal braces must not become a template: got %q want %q", got, want)
	}

	mixedURL := "http://example.com/%7B%7Btoken%7D%7D?cursor={{token}}#{{token}}"
	got, err = requestURI(mixedURL)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/%7B%7Btoken%7D%7D?cursor={{token}}"; got != want {
		t.Fatalf("only actual request templates must be restored: got %q want %q", got, want)
	}
}

func TestEngineDoesNotReuseFailedIterationCapture(t *testing.T) {
	listener := fasthttputil.NewInmemoryListener()
	defer listener.Close()

	var loginRequests atomic.Uint64
	var secureRequests atomic.Uint64
	var unauthorizedRequests atomic.Uint64
	server := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			switch string(ctx.Path()) {
			case "/login":
				current := loginRequests.Add(1)
				ctx.SetStatusCode(fasthttp.StatusOK)
				if current%2 == 0 {
					ctx.Response.SetBodyRaw([]byte(`not-json`))
					return
				}
				ctx.Response.SetBodyRaw([]byte(`{"items":[{"token":"fresh-token"}]}`))
			case "/secure/fresh-token":
				secureRequests.Add(1)
				ctx.SetStatusCode(fasthttp.StatusOK)
			default:
				unauthorizedRequests.Add(1)
				ctx.SetStatusCode(fasthttp.StatusUnauthorized)
			}
		},
	}
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Shutdown()

	loadEngine, err := New(Config{
		URL:             "http://unused/login",
		VirtualUsers:    1,
		Duration:        120 * time.Millisecond,
		RequestTimeout:  time.Second,
		MaxConnsPerHost: DefaultMaxConnsPerHost,
		RateLimitRPS:    200,
		ScenarioSteps: []ScenarioStep{
			{
				Kind: StepRequest,
				URL:  "http://unused/login",
				Captures: []VariableCapture{{
					Name:  "token",
					Path:  "items[0].token",
					Scope: VariableScopeIteration,
				}},
			},
			{
				Kind: StepRequest,
				URL:  "http://unused/secure/{{token}}",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	loadEngine.client.Dial = func(addr string) (net.Conn, error) {
		return listener.Dial()
	}

	if err := loadEngine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-loadEngine.Done()

	if loginRequests.Load() < 2 {
		t.Fatalf("expected repeated iterations, got %d login requests", loginRequests.Load())
	}
	if secureRequests.Load() == 0 || secureRequests.Load() >= loginRequests.Load() {
		t.Fatalf(
			"expected secure requests only after successful captures, login=%d secure=%d",
			loginRequests.Load(),
			secureRequests.Load(),
		)
	}
	if unauthorizedRequests.Load() != 0 {
		t.Fatalf("stale or literal templates reached the server %d times", unauthorizedRequests.Load())
	}
	snapshot := loadEngine.Snapshot()
	if snapshot.CaptureFailures == 0 || snapshot.TemplateFailures == 0 {
		t.Fatalf("expected classified capture and template failures, got %+v", snapshot)
	}
	if snapshot.AssertionFailures != snapshot.CaptureFailures+snapshot.TemplateFailures {
		t.Fatalf("expected classified failures to contribute to assertions, got %+v", snapshot)
	}
}

func mustCompileCapture(t *testing.T, capture VariableCapture) compiledVariableCapture {
	t.Helper()
	compiled, err := compileVariableCaptures([]VariableCapture{capture})
	if err != nil {
		t.Fatal(err)
	}
	return compiled[0]
}
