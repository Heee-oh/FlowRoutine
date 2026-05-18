package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"

	"flowroutine/internal/engine"
	"flowroutine/internal/system"

	"github.com/valyala/fasthttp"
)

const minFileDescriptorLimit uint64 = 100_000

var okBody = []byte("ok")

type result struct {
	URL               string  `json:"url"`
	VirtualUsers      int     `json:"virtualUsers"`
	DurationMS        int64   `json:"durationMs"`
	RateLimitRPS      int     `json:"rateLimitRps"`
	RampUpMS          int64   `json:"rampUpMs"`
	TotalRequests     uint64  `json:"totalRequests"`
	SuccessRequests   uint64  `json:"successRequests"`
	FailedRequests    uint64  `json:"failedRequests"`
	TimeoutFailures   uint64  `json:"timeoutFailures"`
	DNSFailures       uint64  `json:"dnsFailures"`
	TLSFailures       uint64  `json:"tlsFailures"`
	ConnRefused       uint64  `json:"connRefused"`
	OtherFailures     uint64  `json:"otherFailures"`
	AssertionFailures uint64  `json:"assertionFailures"`
	LatencySamples    uint64  `json:"latencySamples"`
	RPS               float64 `json:"rps"`
	AvgLatencyMS      float64 `json:"avgLatencyMs"`
	MinLatencyMS      float64 `json:"minLatencyMs"`
	MaxLatencyMS      float64 `json:"maxLatencyMs"`
	P95LatencyMS      float64 `json:"p95LatencyMs"`
	P99LatencyMS      float64 `json:"p99LatencyMs"`
	P999LatencyMS     float64 `json:"p999LatencyMs"`
	BytesRead         uint64  `json:"bytesRead"`
	BytesWritten      uint64  `json:"bytesWritten"`
	HeapAllocBefore   uint64  `json:"heapAllocBefore"`
	HeapAllocAfterRun uint64  `json:"heapAllocAfterRun"`
	HeapAllocAfter    uint64  `json:"heapAllocAfter"`
	HeapAllocDelta    int64   `json:"heapAllocDelta"`
	TotalAllocDelta   uint64  `json:"totalAllocDelta"`
	NumGCBefore       uint32  `json:"numGcBefore"`
	NumGCAfter        uint32  `json:"numGcAfter"`
	NumGCDelta        uint32  `json:"numGcDelta"`
	GoroutinesBefore  int     `json:"goroutinesBefore"`
	GoroutinesAfter   int     `json:"goroutinesAfter"`
	GoroutineDelta    int     `json:"goroutineDelta"`
	FDCountBefore     int     `json:"fdCountBefore"`
	FDCountAfter      int     `json:"fdCountAfter"`
	FDCountDelta      int     `json:"fdCountDelta"`
	FDCountError      string  `json:"fdCountError,omitempty"`
	FileLimitCurrent  uint64  `json:"fileLimitCurrent"`
	FileLimitMax      uint64  `json:"fileLimitMax"`
}

func main() {
	var (
		targetURL         = flag.String("url", "", "target URL; empty starts a local fasthttp loopback server")
		listen            = flag.String("listen", "", "server-only mode listen address, for example 127.0.0.1:18080")
		vus               = flag.Int("vus", runtime.GOMAXPROCS(0)*64, "virtual users")
		duration          = flag.Duration("duration", 5*time.Second, "benchmark duration")
		warmup            = flag.Duration("warmup", 500*time.Millisecond, "connection and pool warmup duration")
		timeout           = flag.Duration("timeout", time.Second, "request timeout")
		conns             = flag.Int("conns", engine.DefaultMaxConnsPerHost, "fasthttp MaxConnsPerHost")
		latencySampleRate = flag.Int("latency-sample-rate", engine.DefaultLatencySampleRate, "record latency every N requests")
		rateLimitRPS      = flag.Int("rate-limit-rps", 0, "global RPS limit; 0 means unlimited")
		rampUp            = flag.Duration("ramp-up", 0, "duration for gradually starting virtual users")
		cpuProfile        = flag.String("cpuprofile", "", "write CPU profile to file")
		heapProfile       = flag.String("heapprofile", "", "write heap profile to file after benchmark")
	)
	flag.Parse()

	limit, err := system.MaximizeFileDescriptorLimit(minFileDescriptorLimit)
	if err != nil {
		log.Fatalf("file descriptor limit check failed: %v", err)
	}

	if *listen != "" {
		if err := serveOnly(*listen); err != nil {
			log.Fatal(err)
		}
		return
	}

	shutdown := func() {}
	url := *targetURL
	if url == "" {
		url, shutdown, err = startLoopbackServer("127.0.0.1:0")
		if err != nil {
			log.Fatalf("start loopback server: %v", err)
		}
	}
	defer shutdown()

	loadEngine, err := newEngine(url, *vus, *timeout, *conns, *latencySampleRate, *rateLimitRPS, *rampUp)
	if err != nil {
		log.Fatal(err)
	}
	if *warmup > 0 {
		if err := runFor(loadEngine, *warmup); err != nil {
			log.Fatal(err)
		}
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	goroutinesBefore := runtime.NumGoroutine()
	fdCountBefore, fdCountBeforeErr := system.OpenFileDescriptorCount()
	startedAt := time.Now()

	stopCPUProfile, err := startCPUProfile(*cpuProfile)
	if err != nil {
		log.Fatal(err)
	}
	if err := runFor(loadEngine, *duration); err != nil {
		stopCPUProfile()
		log.Fatal(err)
	}
	stopCPUProfile()

	elapsed := time.Since(startedAt)
	snapshot := loadEngine.Snapshot()
	var afterRun runtime.MemStats
	runtime.ReadMemStats(&afterRun)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	goroutinesAfter := runtime.NumGoroutine()
	fdCountAfter, fdCountAfterErr := system.OpenFileDescriptorCount()
	fdCountErr := firstErrorString(fdCountBeforeErr, fdCountAfterErr)

	output := result{
		URL:               url,
		VirtualUsers:      *vus,
		DurationMS:        elapsed.Milliseconds(),
		RateLimitRPS:      *rateLimitRPS,
		RampUpMS:          rampUp.Milliseconds(),
		TotalRequests:     snapshot.TotalRequests,
		SuccessRequests:   snapshot.SuccessRequests,
		FailedRequests:    snapshot.FailedRequests,
		TimeoutFailures:   snapshot.TimeoutFailures,
		DNSFailures:       snapshot.DNSFailures,
		TLSFailures:       snapshot.TLSFailures,
		ConnRefused:       snapshot.ConnRefused,
		OtherFailures:     snapshot.OtherFailures,
		AssertionFailures: snapshot.AssertionFailures,
		LatencySamples:    snapshot.LatencySamples,
		RPS:               float64(snapshot.TotalRequests) / elapsed.Seconds(),
		AvgLatencyMS:      avgLatencyMS(snapshot.TotalLatencyNano, snapshot.LatencySamples),
		MinLatencyMS:      float64(snapshot.MinLatencyNano) / float64(time.Millisecond),
		MaxLatencyMS:      float64(snapshot.MaxLatencyNano) / float64(time.Millisecond),
		P95LatencyMS:      float64(snapshot.P95LatencyNano) / float64(time.Millisecond),
		P99LatencyMS:      float64(snapshot.P99LatencyNano) / float64(time.Millisecond),
		P999LatencyMS:     float64(snapshot.P999LatencyNano) / float64(time.Millisecond),
		BytesRead:         snapshot.BytesRead,
		BytesWritten:      snapshot.BytesWritten,
		HeapAllocBefore:   before.HeapAlloc,
		HeapAllocAfterRun: afterRun.HeapAlloc,
		HeapAllocAfter:    after.HeapAlloc,
		HeapAllocDelta:    uint64Delta(after.HeapAlloc, before.HeapAlloc),
		TotalAllocDelta:   afterRun.TotalAlloc - before.TotalAlloc,
		NumGCBefore:       before.NumGC,
		NumGCAfter:        afterRun.NumGC,
		NumGCDelta:        afterRun.NumGC - before.NumGC,
		GoroutinesBefore:  goroutinesBefore,
		GoroutinesAfter:   goroutinesAfter,
		GoroutineDelta:    goroutinesAfter - goroutinesBefore,
		FDCountBefore:     fdCountBefore,
		FDCountAfter:      fdCountAfter,
		FDCountDelta:      fdCountAfter - fdCountBefore,
		FDCountError:      fdCountErr,
		FileLimitCurrent:  limit.Current,
		FileLimitMax:      limit.Maximum,
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		log.Fatal(err)
	}
	if err := writeHeapProfile(*heapProfile); err != nil {
		log.Fatal(err)
	}
}

func newEngine(url string, vus int, timeout time.Duration, conns int, latencySampleRate int, rateLimitRPS int, rampUp time.Duration) (*engine.Engine, error) {
	return engine.New(engine.Config{
		URL:               url,
		VirtualUsers:      vus,
		RequestTimeout:    timeout,
		MaxConnsPerHost:   conns,
		MaxResponseBytes:  1024,
		LatencySampleRate: latencySampleRate,
		RateLimitRPS:      rateLimitRPS,
		RampUp:            rampUp,
	})
}

func runFor(loadEngine *engine.Engine, duration time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	if err := loadEngine.Start(ctx); err != nil {
		return err
	}
	<-loadEngine.Done()
	return nil
}

func serveOnly(addr string) error {
	_, shutdown, err := startLoopbackServer(addr)
	if err != nil {
		return err
	}
	defer shutdown()

	fmt.Printf("flowroutine bench server listening on http://%s\n", addr)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	return nil
}

func startLoopbackServer(addr string) (string, func(), error) {
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return "", nil, err
	}

	server := &fasthttp.Server{
		Name:                          "flowroutine-bench",
		NoDefaultServerHeader:         true,
		NoDefaultDate:                 true,
		NoDefaultContentType:          true,
		DisableHeaderNamesNormalizing: true,
		Handler: func(ctx *fasthttp.RequestCtx) {
			ctx.SetStatusCode(fasthttp.StatusOK)
			ctx.Response.SetBodyRaw(okBody)
		},
	}

	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("loopback server stopped: %v", err)
		}
	}()

	shutdown := func() {
		if err := server.Shutdown(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "loopback server shutdown failed: %v\n", err)
		}
	}
	return "http://" + ln.Addr().String(), shutdown, nil
}

func avgLatencyMS(totalLatencyNano uint64, totalRequests uint64) float64 {
	if totalRequests == 0 {
		return 0
	}
	return float64(totalLatencyNano) / float64(totalRequests) / float64(time.Millisecond)
}

func uint64Delta(after uint64, before uint64) int64 {
	if after >= before {
		return int64(after - before)
	}
	return -int64(before - after)
}

func firstErrorString(errs ...error) string {
	for _, err := range errs {
		if err != nil {
			return err.Error()
		}
	}
	return ""
}

func startCPUProfile(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() {
		pprof.StopCPUProfile()
		_ = file.Close()
	}, nil
}

func writeHeapProfile(path string) error {
	if path == "" {
		return nil
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	runtime.GC()
	return pprof.WriteHeapProfile(file)
}
