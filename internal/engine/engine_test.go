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
			{Kind: StepRequest, URL: "http://unused"},
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
