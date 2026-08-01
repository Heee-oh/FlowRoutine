package engine

import (
	"context"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func TestNewAllowsSmallConnectionPool(t *testing.T) {
	engine, err := New(Config{
		URL:             "http://127.0.0.1:8080",
		MaxConnsPerHost: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if engine.cfg.maxConnsPerHost != 1 {
		t.Fatalf("expected max conns per host 1, got %d", engine.cfg.maxConnsPerHost)
	}
}

func TestEngineLifecycle(t *testing.T) {
	engine, err := New(Config{
		URL:              "http://127.0.0.1:1",
		VirtualUsers:     1,
		Duration:         10 * time.Millisecond,
		RequestTimeout:   time.Millisecond,
		MaxConnsPerHost:  DefaultMaxConnsPerHost,
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if engine.Running() {
		t.Fatal("engine should stop after configured duration")
	}
	snapshot := engine.Snapshot()
	if snapshot.TotalRequests == 0 {
		t.Fatal("expected attempts against unavailable listener")
	}
	if snapshot.FailedRequests == 0 {
		t.Fatal("expected failed attempts against unavailable listener")
	}
	if snapshot.ConnRefused+snapshot.TimeoutFailures+snapshot.OtherFailures == 0 {
		t.Fatalf("expected classified failures, got %+v", snapshot)
	}
	if sumStatusCodes(snapshot) != 0 {
		t.Fatalf("transport failures should not record HTTP status codes: %+v", snapshot.StatusCodes)
	}
}

func TestEngineRateLimit(t *testing.T) {
	engine, err := New(Config{
		URL:              "http://127.0.0.1:1",
		VirtualUsers:     8,
		Duration:         300 * time.Millisecond,
		RequestTimeout:   time.Millisecond,
		MaxConnsPerHost:  DefaultMaxConnsPerHost,
		MaxResponseBytes: 1024,
		RateLimitRPS:     20,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-engine.Done()

	snapshot := engine.Snapshot()
	if snapshot.TotalRequests == 0 {
		t.Fatal("expected rate-limited attempts")
	}
	if snapshot.TotalRequests > 10 {
		t.Fatalf("rate limit allowed too many requests: got %d, want <= 10", snapshot.TotalRequests)
	}
}

func TestEngineStopCancelsManyRateLimitedWorkers(t *testing.T) {
	engine, err := New(Config{
		URL:              "http://127.0.0.1:1",
		VirtualUsers:     2_048,
		RequestTimeout:   10 * time.Millisecond,
		MaxConnsPerHost:  DefaultMaxConnsPerHost,
		MaxResponseBytes: 1024,
		RateLimitRPS:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	time.Sleep(20 * time.Millisecond)
	startedAt := time.Now()
	if err := engine.Stop(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("stopping rate-limited workers took %s", elapsed)
	}
	if engine.Running() {
		t.Fatal("engine should be stopped")
	}
}

func TestEngineStopDuringRampUp(t *testing.T) {
	engine, err := New(Config{
		URL:              "http://127.0.0.1:1",
		VirtualUsers:     64,
		RequestTimeout:   time.Millisecond,
		MaxConnsPerHost:  DefaultMaxConnsPerHost,
		MaxResponseBytes: 1024,
		RampUp:           5 * time.Second,
		RateLimitRPS:     100,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	startedAt := time.Now()
	if err := engine.Stop(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("stop during ramp-up took too long: %s", elapsed)
	}
	if engine.Running() {
		t.Fatal("engine should be stopped")
	}
}

func TestEngineRepeatedStartStopDoesNotLeakWorkers(t *testing.T) {
	before := runtime.NumGoroutine()
	engine, err := New(Config{
		URL:              "http://127.0.0.1:1",
		VirtualUsers:     8,
		RequestTimeout:   time.Millisecond,
		MaxConnsPerHost:  DefaultMaxConnsPerHost,
		MaxResponseBytes: 1024,
		RateLimitRPS:     100,
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := engine.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
		if err := engine.Stop(); err != nil {
			t.Fatal(err)
		}
	}

	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before+2 {
		t.Fatalf("unexpected goroutine growth: before=%d after=%d", before, after)
	}
}

func TestEngineRunsScenarioStepsAndRecordsAssertionFailures(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	server := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			ctx.SetStatusCode(fasthttp.StatusOK)
			ctx.Response.SetBodyRaw([]byte("ok"))
		},
	}
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Shutdown()

	engine, err := New(Config{
		URL:             "http://unused",
		VirtualUsers:    1,
		Duration:        30 * time.Millisecond,
		RequestTimeout:  time.Second,
		MaxConnsPerHost: DefaultMaxConnsPerHost,
		RateLimitRPS:    100,
		ScenarioSteps: []ScenarioStep{
			{ID: "request-home", Name: "GET home", Kind: StepRequest, URL: "http://unused"},
			{Kind: StepDelay, Delay: time.Millisecond},
			{Kind: StepAssertStatus, ExpectedStatus: "500"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.client.Dial = func(addr string) (net.Conn, error) {
		return ln.Dial()
	}

	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-engine.Done()

	snapshot := engine.Snapshot()
	if snapshot.SuccessRequests == 0 {
		t.Fatal("expected scenario request successes")
	}
	if snapshot.AssertionFailures == 0 {
		t.Fatal("expected assertion failures")
	}
	steps := engine.RequestStepSnapshots()
	if len(steps) != 1 || steps[0].ID != "request-home" || steps[0].Name != "GET home" {
		t.Fatalf("unexpected request-step identity: %+v", steps)
	}
	if steps[0].TotalRequests != snapshot.TotalRequests || steps[0].SuccessRequests != snapshot.SuccessRequests {
		t.Fatalf("request-step totals do not match aggregate: step=%+v aggregate=%+v", steps[0], snapshot)
	}
	if steps[0].AssertionFailures != snapshot.AssertionFailures {
		t.Fatalf("request-step assertions do not match aggregate: step=%d aggregate=%d", steps[0].AssertionFailures, snapshot.AssertionFailures)
	}
}

func TestEngineClassifiesRichAssertionsAndCountOnlyFailures(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	server := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			ctx.Response.Header.Set("Content-Type", "application/json")
			ctx.Response.Header.Set("X-Trace", "present")
			ctx.SetStatusCode(fasthttp.StatusOK)
			ctx.Response.SetBodyRaw([]byte(`{"id":42,"active":true}`))
		},
	}
	go func() { _ = server.Serve(ln) }()
	defer server.Shutdown()

	loadEngine, err := New(Config{
		URL:             "http://unused",
		VirtualUsers:    1,
		Duration:        30 * time.Millisecond,
		RequestTimeout:  time.Second,
		MaxConnsPerHost: DefaultMaxConnsPerHost,
		RateLimitRPS:    100,
		ScenarioSteps: []ScenarioStep{
			{ID: "request", Kind: StepRequest, URL: "http://unused"},
			{Kind: StepAssert, Assertion: Assertion{Type: AssertionStatus, Expected: "201"}},
			{Kind: StepAssert, Assertion: Assertion{Type: AssertionHeader, HeaderName: "x-trace", Operator: AssertionExists}},
			{
				Kind: StepAssert,
				Assertion: Assertion{
					Type:        AssertionHeader,
					HeaderName:  "Content-Type",
					Operator:    AssertionEquals,
					Expected:    "text/plain",
					FailureMode: AssertionCountOnly,
				},
			},
			{
				Kind: StepAssert,
				Assertion: Assertion{
					Type:      AssertionJSON,
					JSONPath:  "$.id",
					Operator:  AssertionEquals,
					ValueType: AssertionValueNumber,
					Expected:  "42",
				},
			},
			{
				Kind: StepAssert,
				Assertion: Assertion{
					Type:      AssertionJSON,
					JSONPath:  "$.active",
					Operator:  AssertionEquals,
					ValueType: AssertionValueBoolean,
					Expected:  "false",
				},
			},
			{Kind: StepAssert, Assertion: Assertion{Type: AssertionResponseLatency, MaxLatency: time.Nanosecond}},
			{Kind: StepAssert, Assertion: Assertion{Type: AssertionStepLatency, MaxLatency: time.Hour}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	loadEngine.client.Dial = func(addr string) (net.Conn, error) { return ln.Dial() }
	if err := loadEngine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-loadEngine.Done()

	snapshot := loadEngine.Snapshot()
	requests := snapshot.TotalRequests
	if requests == 0 {
		t.Fatal("expected assertion requests")
	}
	wantEnforced := requests * 3
	if snapshot.AssertionFailures != wantEnforced {
		t.Fatalf("unexpected enforced assertion failures: got %d want %d", snapshot.AssertionFailures, wantEnforced)
	}
	breakdown := snapshot.AssertionFailuresByType
	if breakdown.Status != requests || breakdown.Header != requests || breakdown.JSON != requests ||
		breakdown.ResponseLatency != requests || breakdown.StepLatency != 0 || breakdown.CountOnly != requests {
		t.Fatalf("unexpected assertion breakdown: %+v for %d requests", breakdown, requests)
	}
	steps := loadEngine.RequestStepSnapshots()
	if len(steps) != 1 || steps[0].AssertionFailuresByType != breakdown {
		t.Fatalf("request-step assertion breakdown does not match aggregate: steps=%+v aggregate=%+v", steps, breakdown)
	}
}

func TestEngineStopsOnlyCurrentIterationAfterAssertionFailure(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	server := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(fasthttp.StatusOK)
	}}
	go func() { _ = server.Serve(ln) }()
	defer server.Shutdown()

	loadEngine, err := New(Config{
		URL:             "http://unused/first",
		VirtualUsers:    1,
		Duration:        30 * time.Millisecond,
		RequestTimeout:  time.Second,
		MaxConnsPerHost: DefaultMaxConnsPerHost,
		RateLimitRPS:    100,
		ScenarioSteps: []ScenarioStep{
			{ID: "first", Kind: StepRequest, URL: "http://unused/first"},
			{
				Kind: StepAssert,
				Assertion: Assertion{
					Type:        AssertionStatus,
					Expected:    "500",
					FailureMode: AssertionStop,
				},
			},
			{ID: "second", Kind: StepRequest, URL: "http://unused/second"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	loadEngine.client.Dial = func(addr string) (net.Conn, error) { return ln.Dial() }
	if err := loadEngine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-loadEngine.Done()

	steps := loadEngine.RequestStepSnapshots()
	if len(steps) != 2 || steps[0].TotalRequests == 0 || steps[1].TotalRequests != 0 {
		t.Fatalf("stop mode should skip the remaining iteration only: %+v", steps)
	}
	if loadEngine.Snapshot().AssertionFailures == 0 {
		t.Fatal("expected the stop assertion failure to be counted")
	}
}

func TestValidateConfigRejectsInvalidAssertionsBeforeRun(t *testing.T) {
	base := Config{
		URL: "http://example.com",
		ScenarioSteps: []ScenarioStep{
			{Kind: StepRequest, URL: "http://example.com"},
		},
	}
	tests := []struct {
		name      string
		assertion Assertion
	}{
		{name: "invalid status", assertion: Assertion{Type: AssertionStatus, Expected: "999"}},
		{name: "missing header", assertion: Assertion{Type: AssertionHeader, Operator: AssertionExists}},
		{name: "invalid JSON path", assertion: Assertion{Type: AssertionJSON, JSONPath: "$.items[bad]", Operator: AssertionExists}},
		{name: "invalid typed value", assertion: Assertion{Type: AssertionJSON, JSONPath: "$.active", Operator: AssertionEquals, ValueType: AssertionValueBoolean, Expected: "yes"}},
		{name: "missing latency", assertion: Assertion{Type: AssertionStepLatency}},
		{name: "invalid failure mode", assertion: Assertion{Type: AssertionStatus, Expected: "2xx", FailureMode: "abort"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base
			config.ScenarioSteps = append([]ScenarioStep(nil), base.ScenarioSteps...)
			config.ScenarioSteps = append(config.ScenarioSteps, ScenarioStep{Kind: StepAssert, Assertion: test.assertion})
			if err := ValidateConfig(config); err == nil {
				t.Fatal("expected assertion validation error")
			}
		})
	}

	base.ScenarioSteps = []ScenarioStep{{
		Kind:      StepAssert,
		Assertion: Assertion{Type: AssertionStatus, Expected: "2xx"},
	}}
	if err := ValidateConfig(base); err == nil {
		t.Fatal("expected assertion-before-request validation error")
	}
}

func TestEngineCapturesJSONVariablesForLaterSteps(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	var authorized atomic.Uint64
	server := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			switch string(ctx.Path()) {
			case "/login":
				ctx.SetStatusCode(fasthttp.StatusOK)
				ctx.Response.SetBodyRaw([]byte(`{"data":{"token":"abc123"}}`))
			case "/secure":
				if string(ctx.Request.Header.Peek("Authorization")) == "Bearer abc123" {
					authorized.Add(1)
					ctx.SetStatusCode(fasthttp.StatusOK)
					ctx.Response.SetBodyRaw([]byte("ok"))
					return
				}
				ctx.SetStatusCode(fasthttp.StatusUnauthorized)
			default:
				ctx.SetStatusCode(fasthttp.StatusNotFound)
			}
		},
	}
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Shutdown()

	engine, err := New(Config{
		URL:               "http://unused/login",
		VirtualUsers:      1,
		Duration:          30 * time.Millisecond,
		RequestTimeout:    time.Second,
		MaxConnsPerHost:   DefaultMaxConnsPerHost,
		LatencySampleRate: 2,
		ScenarioSteps: []ScenarioStep{
			{
				ID:       "login",
				Kind:     StepRequest,
				URL:      "http://unused/login",
				Captures: []VariableCapture{{Name: "token", Path: "data.token"}},
			},
			{
				ID:   "secure",
				Kind: StepRequest,
				URL:  "http://unused/secure",
				Headers: []Header{{
					Name:  "Authorization",
					Value: "Bearer {{token}}",
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.client.Dial = func(addr string) (net.Conn, error) {
		return ln.Dial()
	}

	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-engine.Done()

	if authorized.Load() == 0 {
		t.Fatal("expected captured token to authorize a later request")
	}
	if snapshot := engine.Snapshot(); snapshot.AssertionFailures != 0 {
		t.Fatalf("expected no capture assertion failures, got %+v", snapshot)
	}
	steps := engine.RequestStepSnapshots()
	if len(steps) != 2 || steps[0].LatencySamples == 0 || steps[1].LatencySamples == 0 ||
		absoluteDifference(steps[0].LatencySamples, steps[1].LatencySamples) > 1 {
		t.Fatalf("iteration sampling was biased between request steps: %+v", steps)
	}
}

func absoluteDifference(left uint64, right uint64) uint64 {
	if left >= right {
		return left - right
	}
	return right - left
}

func TestEngineCountsHTTPErrorStatusAsFailure(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	server := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			ctx.SetStatusCode(fasthttp.StatusUnauthorized)
			ctx.Response.SetBodyRaw([]byte("unauthorized"))
		},
	}
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Shutdown()

	engine, err := New(Config{
		URL:             "http://unused",
		VirtualUsers:    1,
		Duration:        20 * time.Millisecond,
		RequestTimeout:  time.Second,
		MaxConnsPerHost: DefaultMaxConnsPerHost,
		RateLimitRPS:    100,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.client.Dial = func(addr string) (net.Conn, error) {
		return ln.Dial()
	}

	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-engine.Done()

	snapshot := engine.Snapshot()
	if snapshot.FailedRequests == 0 {
		t.Fatal("expected 401 responses to count as failed")
	}
	if snapshot.SuccessRequests != 0 {
		t.Fatalf("expected no successes for 401 responses, got %d", snapshot.SuccessRequests)
	}
	if snapshot.StatusCodes[fasthttp.StatusUnauthorized] == 0 {
		t.Fatal("expected 401 responses in status code histogram")
	}
}

func TestEngineRecordsHTTPStatusCodeHistogram(t *testing.T) {
	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	statuses := []int{fasthttp.StatusOK, fasthttp.StatusUnauthorized, fasthttp.StatusInternalServerError}
	var requestIndex atomic.Uint64
	server := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			index := requestIndex.Add(1) - 1
			ctx.SetStatusCode(statuses[int(index)%len(statuses)])
			ctx.Response.SetBodyRaw([]byte("ok"))
		},
	}
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Shutdown()

	engine, err := New(Config{
		URL:             "http://unused",
		VirtualUsers:    1,
		Duration:        50 * time.Millisecond,
		RequestTimeout:  time.Second,
		MaxConnsPerHost: DefaultMaxConnsPerHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine.client.Dial = func(addr string) (net.Conn, error) {
		return ln.Dial()
	}

	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-engine.Done()

	snapshot := engine.Snapshot()
	for _, status := range statuses {
		if snapshot.StatusCodes[status] == 0 {
			t.Fatalf("expected status %d in histogram, got %+v", status, snapshot.StatusCodes)
		}
	}
	if snapshot.SuccessRequests == 0 || snapshot.FailedRequests == 0 {
		t.Fatalf("expected mixed success and failure counts, got %+v", snapshot)
	}
}

func sumStatusCodes(snapshot Snapshot) uint64 {
	var total uint64
	for _, count := range snapshot.StatusCodes {
		total += count
	}
	return total
}
