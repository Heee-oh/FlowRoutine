package bridge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"flowroutine/internal/engine"
)

type discardEmitter struct{}

func (discardEmitter) Emit(context.Context, string, any) {}

func TestControllerConcurrentStartAllowsOneRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	controller := NewController(discardEmitter{})
	request := validControllerStartRequest(server.URL)
	const callers = 16
	start := make(chan struct{})
	results := make(chan error, callers)
	var workers sync.WaitGroup
	workers.Add(callers)

	for range callers {
		go func() {
			defer workers.Done()
			<-start
			_, err := controller.Start(context.Background(), request)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	started := 0
	for err := range results {
		if err == nil {
			started++
			continue
		}
		if !errors.Is(err, engine.ErrAlreadyRunning) {
			t.Fatalf("unexpected start error: %v", err)
		}
	}
	if started != 1 {
		t.Fatalf("started %d runs, want exactly 1", started)
	}
	if err := controller.Stop(); err != nil {
		t.Fatalf("stop run: %v", err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatalf("second stop should be idempotent: %v", err)
	}
}

func TestControllerConcurrentStopIsIdempotent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	controller := NewController(discardEmitter{})
	if _, err := controller.Start(context.Background(), validControllerStartRequest(server.URL)); err != nil {
		t.Fatalf("start run: %v", err)
	}

	const callers = 16
	start := make(chan struct{})
	results := make(chan error, callers)
	for range callers {
		go func() {
			<-start
			results <- controller.Stop()
		}()
	}
	close(start)

	for range callers {
		if err := <-results; err != nil {
			t.Fatalf("concurrent stop: %v", err)
		}
	}
	assertControllerIdle(t, controller)
}

func TestControllerStopDuringStartupLeavesNoRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	controller := NewController(discardEmitter{})
	realNewEngine := controller.newEngine
	factoryStarted := make(chan struct{})
	releaseFactory := make(chan struct{})
	controller.newEngine = func(config engine.Config) (*engine.Engine, error) {
		close(factoryStarted)
		<-releaseFactory
		return realNewEngine(config)
	}

	startResult := make(chan error, 1)
	go func() {
		_, err := controller.Start(context.Background(), validControllerStartRequest(server.URL))
		startResult <- err
	}()
	<-factoryStarted

	stopResult := make(chan error, 1)
	go func() {
		stopResult <- controller.Stop()
	}()

	var earlyStop error
	stoppedEarly := false
	select {
	case earlyStop = <-stopResult:
		stoppedEarly = true
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFactory)

	if err := <-startResult; err != nil {
		t.Fatalf("start run: %v", err)
	}
	if !stoppedEarly {
		earlyStop = <-stopResult
	}
	if earlyStop != nil {
		t.Fatalf("stop during startup: %v", earlyStop)
	}
	if stoppedEarly {
		_ = controller.Stop()
		t.Fatal("stop returned before startup transition completed")
	}

	assertControllerIdle(t, controller)
}

func TestControllerRollsBackEngineStartFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	controller := NewController(discardEmitter{})
	controller.newEngine = func(config engine.Config) (*engine.Engine, error) {
		e, err := engine.New(config)
		if err != nil {
			return nil, err
		}
		if err := e.Start(context.Background()); err != nil {
			return nil, err
		}
		return e, nil
	}

	_, err := controller.Start(context.Background(), validControllerStartRequest(server.URL))
	if !errors.Is(err, engine.ErrAlreadyRunning) {
		t.Fatalf("got start error %v, want %v", err, engine.ErrAlreadyRunning)
	}
	assertControllerIdle(t, controller)

	controller.newEngine = engine.New
	if _, err := controller.Start(context.Background(), validControllerStartRequest(server.URL)); err != nil {
		t.Fatalf("restart after rollback: %v", err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatalf("stop restarted run: %v", err)
	}
}

func TestControllerRollsBackBatcherStartFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	controller := NewController(discardEmitter{})
	controller.newBatcher = func(e *engine.Engine, emitter Emitter, interval time.Duration) *Batcher {
		batcher := NewBatcher(e, emitter, interval)
		if err := batcher.Start(context.Background()); err != nil {
			t.Fatalf("prime batcher: %v", err)
		}
		return batcher
	}

	_, err := controller.Start(context.Background(), validControllerStartRequest(server.URL))
	if !errors.Is(err, ErrBatcherRunning) {
		t.Fatalf("got start error %v, want %v", err, ErrBatcherRunning)
	}
	assertControllerIdle(t, controller)

	controller.newBatcher = defaultBatcherFactory
	if _, err := controller.Start(context.Background(), validControllerStartRequest(server.URL)); err != nil {
		t.Fatalf("restart after rollback: %v", err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatalf("stop restarted run: %v", err)
	}
}

func validControllerStartRequest(url string) StartRequest {
	return StartRequest{
		Config: LoadConfig{
			URL:               url,
			Method:            http.MethodGet,
			VirtualUsers:      1,
			DurationMS:        int64((5 * time.Second) / time.Millisecond),
			RequestTimeoutMS:  int64(time.Second / time.Millisecond),
			MaxConnsPerHost:   1,
			ReadBufferSize:    4_096,
			WriteBufferSize:   4_096,
			MaxResponseBytes:  1_048_576,
			LatencySampleRate: 1,
			RateLimitRPS:      10,
		},
		BatchIntervalMS: int(DefaultInterval / time.Millisecond),
	}
}

func assertControllerIdle(t *testing.T, controller *Controller) {
	t.Helper()
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.state != controllerIdle || controller.engine != nil || controller.batcher != nil {
		t.Fatalf(
			"controller not idle: state=%d engine=%p batcher=%p",
			controller.state,
			controller.engine,
			controller.batcher,
		)
	}
}
