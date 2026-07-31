package engine

import (
	"net"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

var benchOKBody = []byte("ok")

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
	variables := map[string]string{}
	for i := 0; i < b.N; i++ {
		req := engine.acquireRequest(engine.cfg.steps[0].request, variables)
		engine.releaseRequest(req)
	}
}

func BenchmarkStatsRecordSuccess(b *testing.B) {
	var stats AtomicStats
	stats.Reset(time.Now())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stats.RecordSuccess(time.Millisecond, 2, 32)
	}
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
	variables := map[string]string{}
	for i := 0; i < b.N; i++ {
		req := engine.acquireRequest(engine.cfg.steps[0].request, variables)
		resp := engine.acquireResponse()
		err := engine.client.DoTimeout(req, resp, time.Second)
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
		variables := map[string]string{}
		for pb.Next() {
			req := engine.acquireRequest(engine.cfg.steps[0].request, variables)
			resp := engine.acquireResponse()
			startedAt := time.Time{}
			if measureLatency {
				startedAt = time.Now()
			}
			err := engine.client.DoTimeout(req, resp, time.Second)
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
