package engine

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func TestConstantArrivalProfileSchedulesExpectedIterations(t *testing.T) {
	loadEngine, shutdown := newProfileTestEngine(t, 100, 200*time.Millisecond, 2, 4, 0)
	defer shutdown()

	if err := loadEngine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-loadEngine.Done()

	snapshot := loadEngine.Snapshot()
	if snapshot.TotalRequests != 20 {
		t.Fatalf("scheduled %d requests, want 20", snapshot.TotalRequests)
	}
	if snapshot.DroppedIterations != 0 {
		t.Fatalf("dropped %d iterations, want 0", snapshot.DroppedIterations)
	}
}

func TestArrivalProfileRecordsDroppedIterationsAtCapacity(t *testing.T) {
	loadEngine, shutdown := newProfileTestEngine(t, 500, 50*time.Millisecond, 1, 1, 15*time.Millisecond)
	defer shutdown()

	if err := loadEngine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-loadEngine.Done()

	snapshot := loadEngine.Snapshot()
	if snapshot.DroppedIterations == 0 {
		t.Fatalf("expected dropped iterations at capacity, got %+v", snapshot)
	}
	if scheduled := snapshot.TotalRequests + snapshot.DroppedIterations; scheduled != 25 {
		t.Fatalf("scheduled %d iterations, want 25", scheduled)
	}
}

func TestProfileCancellationStopsDuringStage(t *testing.T) {
	loadEngine, err := New(Config{
		URL:            "http://127.0.0.1:1",
		RequestTimeout: time.Millisecond,
		Profile: &LoadProfile{
			Mode:        LoadModeRampingVUs,
			StartTarget: 1,
			Stages:      []LoadStage{{Duration: time.Minute, Target: 100}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := loadEngine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now()
	if err := loadEngine.Stop(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("profile cancellation took %s", elapsed)
	}
}

func TestRampingVUProfileReactivatesAfterGracefulRampDown(t *testing.T) {
	listener := fasthttputil.NewInmemoryListener()
	server := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(fasthttp.StatusOK)
	}}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		_ = server.Shutdown()
		_ = listener.Close()
	}()

	loadEngine, err := New(Config{
		URL:            "http://unused",
		RequestTimeout: 50 * time.Millisecond,
		Profile: &LoadProfile{
			Mode:        LoadModeRampingVUs,
			StartTarget: 1,
			Stages: []LoadStage{
				{Duration: 50 * time.Millisecond, Target: 1},
				{Duration: 50 * time.Millisecond, Target: 0},
				{Duration: 200 * time.Millisecond, Target: 1},
			},
			GracefulStop: 10 * time.Millisecond,
		},
		ScenarioSteps: []ScenarioStep{
			{Kind: StepRequest, URL: "http://unused"},
			{Kind: StepDelay, Delay: 500 * time.Millisecond},
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

	if requests := loadEngine.Snapshot().TotalRequests; requests < 2 {
		t.Fatalf("worker did not resume after ramp-down: got %d requests", requests)
	}
}

func newProfileTestEngine(
	t *testing.T,
	rate int,
	duration time.Duration,
	preAllocatedVUs int,
	maxVUs int,
	handlerDelay time.Duration,
) (*Engine, func()) {
	t.Helper()
	listener := fasthttputil.NewInmemoryListener()
	server := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		if handlerDelay > 0 {
			time.Sleep(handlerDelay)
		}
		ctx.SetStatusCode(fasthttp.StatusOK)
	}}
	go func() { _ = server.Serve(listener) }()

	loadEngine, err := New(Config{
		URL:            "http://unused",
		RequestTimeout: time.Second,
		Profile: &LoadProfile{
			Mode:            LoadModeConstantArrival,
			StartTarget:     rate,
			Stages:          []LoadStage{{Duration: duration, Target: rate}},
			PreAllocatedVUs: preAllocatedVUs,
			MaxVUs:          maxVUs,
			GracefulStop:    time.Second,
		},
	})
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	loadEngine.client.Dial = func(addr string) (net.Conn, error) {
		return listener.Dial()
	}
	return loadEngine, func() {
		_ = server.Shutdown()
		_ = listener.Close()
	}
}
