package engine

import (
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

var benchOKBody = []byte("ok")
var benchmarkStatsSnapshot Snapshot

var statsBenchmarkVirtualUsers = [...]int{1, 1_000, 10_000, 100_000}

func BenchmarkAcquireReleaseRequest(b *testing.B) {
	engine, err := New(Config{
		URL:             "http://127.0.0.1:8080",
		Method:          "POST",
		Headers:         []Header{{Name: "Content-Type", Value: "application/json"}},
		Body:            []byte(`{"ok":true}`),
		MaxConnsPerHost: DefaultMaxConnsPerHost,
	})
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	variables := newWorkerVariables()
	for i := 0; i < b.N; i++ {
		req, err := engine.acquireRequest(engine.cfg.steps[0].request, variables)
		if err != nil {
			b.Fatal(err)
		}
		engine.releaseRequest(req)
	}
}

func BenchmarkAcquireReleaseTemplatedRequest(b *testing.B) {
	engine, err := New(Config{
		URL:             "http://127.0.0.1:8080",
		MaxConnsPerHost: DefaultMaxConnsPerHost,
		ScenarioSteps: []ScenarioStep{
			{
				Kind:     StepRequest,
				URL:      "http://127.0.0.1:8080/session",
				Captures: []VariableCapture{{Name: "token", Path: "token"}},
			},
			{
				Kind:   StepRequest,
				URL:    "http://127.0.0.1:8080/items/{{token}}",
				Method: "POST",
				Headers: []Header{{
					Name:  "Authorization",
					Value: "Bearer {{token}}",
				}},
				Body: []byte(`{"token":"{{token}}"}`),
			},
		},
	})
	if err != nil {
		b.Fatal(err)
	}

	variables := newWorkerVariables()
	variables.iteration["token"] = "benchmark-token"
	step := engine.cfg.steps[1].request
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		req, err := engine.acquireRequest(step, variables)
		if err != nil {
			b.Fatal(err)
		}
		engine.releaseRequest(req)
		variables.releaseRenderBuffer()
	}
}

func BenchmarkCaptureVariables(b *testing.B) {
	captures, err := compileVariableCaptures([]VariableCapture{
		{Name: "token", Path: "$.data.token"},
		{Name: "userID", Path: "$.data.user.id"},
	})
	if err != nil {
		b.Fatal(err)
	}
	body := []byte(`{"data":{"token":"benchmark-token","user":{"id":42}}}`)
	variables := newWorkerVariables()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		variables.beginIteration()
		if err := captureVariables(body, 200, captures, variables); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStatsRecordSuccess(b *testing.B) {
	for _, virtualUsers := range statsBenchmarkVirtualUsers {
		b.Run("VUs="+strconv.Itoa(virtualUsers), func(b *testing.B) {
			var stats AtomicStats
			stats.Init(virtualUsers)
			stats.Reset(time.Now())

			var nextShard atomic.Uint64
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				shard := stats.Shard(int(nextShard.Add(1) - 1))
				for pb.Next() {
					shard.RecordHTTPSuccessSampled(time.Millisecond, 2, 32, true, 200)
				}
			})
			b.StopTimer()
			reportStatsBenchmarkMetrics(b, &stats)
		})
	}
}

func BenchmarkStatsSnapshot(b *testing.B) {
	for _, virtualUsers := range statsBenchmarkVirtualUsers {
		b.Run("VUs="+strconv.Itoa(virtualUsers), func(b *testing.B) {
			var stats AtomicStats
			stats.Init(virtualUsers)
			stats.Reset(time.Now())
			stats.Shard(virtualUsers-1).RecordHTTPSuccessSampled(time.Millisecond, 2, 32, true, 200)

			now := time.Now()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkStatsSnapshot = stats.Snapshot(now)
			}
			b.StopTimer()
			reportStatsBenchmarkMetrics(b, &stats)
		})
	}
}

func reportStatsBenchmarkMetrics(b *testing.B, stats *AtomicStats) {
	b.Helper()
	b.ReportMetric(float64(len(stats.shards)), "shards")
	b.ReportMetric(float64(len(stats.shards))*float64(unsafe.Sizeof(statsShard{})), "B/stats")
}

func BenchmarkFasthttpClientLoopback(b *testing.B) {
	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	server := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			ctx.SetStatusCode(fasthttp.StatusOK)
			ctx.Response.SetBodyRaw(benchOKBody)
		},
	}
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Shutdown()

	engine, err := New(Config{
		URL:             "http://unused",
		VirtualUsers:    1,
		MaxConnsPerHost: DefaultMaxConnsPerHost,
	})
	if err != nil {
		b.Fatal(err)
	}
	engine.client.Dial = func(addr string) (net.Conn, error) {
		return ln.Dial()
	}

	b.ReportAllocs()
	b.ResetTimer()
	variables := newWorkerVariables()
	for i := 0; i < b.N; i++ {
		req, err := engine.acquireRequest(engine.cfg.steps[0].request, variables)
		if err != nil {
			b.Fatal(err)
		}
		resp := engine.acquireResponse()
		err = engine.client.DoTimeout(req, resp, time.Second)
		if err != nil {
			b.Fatal(err)
		}
		engine.releaseResponse(resp)
		engine.releaseRequest(req)
	}
}

func BenchmarkFasthttpClientLoopbackParallel(b *testing.B) {
	benchmarkFasthttpClientLoopbackParallel(b, false)
}

func BenchmarkFasthttpClientLoopbackParallelWithLatency(b *testing.B) {
	benchmarkFasthttpClientLoopbackParallel(b, true)
}

func benchmarkFasthttpClientLoopbackParallel(b *testing.B, measureLatency bool) {
	ln := fasthttputil.NewInmemoryListener()
	defer ln.Close()

	server := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			ctx.SetStatusCode(fasthttp.StatusOK)
			ctx.Response.SetBodyRaw(benchOKBody)
		},
	}
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Shutdown()

	engine, err := New(Config{
		URL:             "http://unused",
		VirtualUsers:    1,
		MaxConnsPerHost: DefaultMaxConnsPerHost,
	})
	if err != nil {
		b.Fatal(err)
	}
	engine.stats.Reset(time.Now())
	engine.client.Dial = func(addr string) (net.Conn, error) {
		return ln.Dial()
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		variables := newWorkerVariables()
		for pb.Next() {
			req, err := engine.acquireRequest(engine.cfg.steps[0].request, variables)
			if err != nil {
				b.Fatal(err)
			}
			resp := engine.acquireResponse()
			startedAt := time.Time{}
			if measureLatency {
				startedAt = time.Now()
			}
			err = engine.client.DoTimeout(req, resp, time.Second)
			if err != nil {
				b.Fatal(err)
			}
			latency := time.Microsecond
			if measureLatency {
				latency = time.Since(startedAt)
			}
			engine.stats.RecordSuccess(latency, len(resp.Body()), engine.cfg.requestBytes)
			engine.releaseResponse(resp)
			engine.releaseRequest(req)
		}
	})
}
