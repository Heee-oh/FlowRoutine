package bridge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"flowroutine/internal/engine"
)

const (
	MinDuration     = time.Millisecond
	MaxDuration     = time.Hour
	MaxRampUp       = time.Hour
	MaxRequestDelay = 5 * time.Minute
	MaxVirtualUsers = 100_000
)

var ErrControllerNotStarted = errors.New("bridge controller has no active engine")

type StartRequest struct {
	Config          LoadConfig `json:"config"`
	BatchIntervalMS int        `json:"batchIntervalMs"`
}

type StartResponse struct {
	Started bool `json:"started"`
}

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ScenarioStep struct {
	Kind           string   `json:"kind"`
	URL            string   `json:"url"`
	Method         string   `json:"method"`
	Headers        []Header `json:"headers"`
	Body           string   `json:"body"`
	DelayMS        int64    `json:"delayMs"`
	ExpectedStatus string   `json:"expectedStatus"`
}

type LoadConfig struct {
	URL               string         `json:"url"`
	Method            string         `json:"method"`
	Headers           []Header       `json:"headers"`
	Body              string         `json:"body"`
	VirtualUsers      int            `json:"virtualUsers"`
	DurationMS        int64          `json:"durationMs"`
	RequestTimeoutMS  int64          `json:"requestTimeoutMs"`
	MaxConnsPerHost   int            `json:"maxConnsPerHost"`
	ReadBufferSize    int            `json:"readBufferSize"`
	WriteBufferSize   int            `json:"writeBufferSize"`
	MaxResponseBytes  int            `json:"maxResponseBytes"`
	LatencySampleRate int            `json:"latencySampleRate"`
	RateLimitRPS      int            `json:"rateLimitRps"`
	RampUpMS          int64          `json:"rampUpMs"`
	ScenarioSteps     []ScenarioStep `json:"scenarioSteps"`
}

type SnapshotResponse struct {
	StartedAtUnixMs   int64             `json:"startedAtUnixMs"`
	AtUnixMs          int64             `json:"atUnixMs"`
	TotalRequests     uint64            `json:"totalRequests"`
	SuccessRequests   uint64            `json:"successRequests"`
	FailedRequests    uint64            `json:"failedRequests"`
	TimeoutFailures   uint64            `json:"timeoutFailures"`
	DNSFailures       uint64            `json:"dnsFailures"`
	TLSFailures       uint64            `json:"tlsFailures"`
	ConnRefused       uint64            `json:"connRefused"`
	OtherFailures     uint64            `json:"otherFailures"`
	AssertionFailures uint64            `json:"assertionFailures"`
	LatencySamples    uint64            `json:"latencySamples"`
	TotalLatencyNano  uint64            `json:"totalLatencyNano"`
	MinLatencyNano    uint64            `json:"minLatencyNano"`
	MaxLatencyNano    uint64            `json:"maxLatencyNano"`
	P95LatencyNano    uint64            `json:"p95LatencyNano"`
	P99LatencyNano    uint64            `json:"p99LatencyNano"`
	P999LatencyNano   uint64            `json:"p999LatencyNano"`
	BytesRead         uint64            `json:"bytesRead"`
	BytesWritten      uint64            `json:"bytesWritten"`
	StatusCodes       []StatusCodeCount `json:"statusCodes"`
}

type Controller struct {
	emitter Emitter

	mu      sync.Mutex
	engine  *engine.Engine
	batcher *Batcher
}

func NewController(emitter Emitter) *Controller {
	return &Controller{emitter: emitter}
}

func (c *Controller) Start(ctx context.Context, req StartRequest) (StartResponse, error) {
	if err := req.Config.Validate(); err != nil {
		return StartResponse{}, err
	}
	e, err := engine.New(req.Config.toEngineConfig())
	if err != nil {
		return StartResponse{}, err
	}

	c.mu.Lock()
	if c.engine != nil && c.engine.Running() {
		c.mu.Unlock()
		return StartResponse{}, engine.ErrAlreadyRunning
	}
	previousBatcher := c.batcher
	c.engine = e
	batcher := NewBatcher(e, c.emitter, time.Duration(req.BatchIntervalMS)*time.Millisecond)
	c.batcher = batcher
	c.mu.Unlock()

	if previousBatcher != nil {
		previousBatcher.Stop()
	}

	if err := e.Start(ctx); err != nil {
		return StartResponse{}, err
	}
	if err := batcher.Start(ctx); err != nil {
		_ = e.Stop()
		return StartResponse{}, err
	}

	go c.stopBatcherWhenEngineStops(e, batcher)
	return StartResponse{Started: true}, nil
}

func (c *Controller) Stop() error {
	c.mu.Lock()
	e := c.engine
	batcher := c.batcher
	c.engine = nil
	c.batcher = nil
	c.mu.Unlock()

	if e == nil {
		if batcher != nil {
			batcher.Stop()
		}
		return ErrControllerNotStarted
	}
	if e.Running() {
		if err := e.Stop(); err != nil {
			return err
		}
	}
	if batcher != nil {
		batcher.Stop()
	}
	return nil
}

func (c *Controller) Snapshot() (SnapshotResponse, error) {
	c.mu.Lock()
	e := c.engine
	c.mu.Unlock()

	if e == nil {
		return SnapshotResponse{}, ErrControllerNotStarted
	}
	return snapshotResponse(e.Snapshot()), nil
}

func (c *Controller) stopBatcherWhenEngineStops(e *engine.Engine, batcher *Batcher) {
	done := e.Done()
	if done == nil {
		return
	}
	<-done
	batcher.Stop()
}

func (c LoadConfig) Validate() error {
	duration := time.Duration(c.DurationMS) * time.Millisecond
	requestTimeout := time.Duration(c.RequestTimeoutMS) * time.Millisecond
	rampUp := time.Duration(c.RampUpMS) * time.Millisecond

	if duration < MinDuration {
		return fmt.Errorf("duration must be at least %s", MinDuration)
	}
	if duration > MaxDuration {
		return fmt.Errorf("duration must be at most %s", MaxDuration)
	}
	if requestTimeout <= 0 {
		return errors.New("request timeout must be greater than 0")
	}
	if requestTimeout > MaxRequestDelay {
		return fmt.Errorf("request timeout must be at most %s", MaxRequestDelay)
	}
	if c.VirtualUsers < 1 {
		return errors.New("virtual users must be at least 1")
	}
	if c.VirtualUsers > MaxVirtualUsers {
		return fmt.Errorf("virtual users must be at most %d", MaxVirtualUsers)
	}
	if c.RateLimitRPS < 0 {
		return errors.New("rate limit rps must be greater than or equal to 0")
	}
	if rampUp < 0 {
		return errors.New("ramp-up must be greater than or equal to 0")
	}
	if rampUp > MaxRampUp {
		return fmt.Errorf("ramp-up must be at most %s", MaxRampUp)
	}
	return nil
}

func (c LoadConfig) toEngineConfig() engine.Config {
	headers := make([]engine.Header, 0, len(c.Headers))
	for _, h := range c.Headers {
		headers = append(headers, engine.Header{Name: h.Name, Value: h.Value})
	}
	steps := make([]engine.ScenarioStep, 0, len(c.ScenarioSteps))
	for _, step := range c.ScenarioSteps {
		stepHeaders := make([]engine.Header, 0, len(step.Headers))
		for _, h := range step.Headers {
			stepHeaders = append(stepHeaders, engine.Header{Name: h.Name, Value: h.Value})
		}
		steps = append(steps, engine.ScenarioStep{
			Kind:           engine.StepKind(step.Kind),
			URL:            step.URL,
			Method:         step.Method,
			Headers:        stepHeaders,
			Body:           []byte(step.Body),
			Delay:          time.Duration(step.DelayMS) * time.Millisecond,
			ExpectedStatus: step.ExpectedStatus,
		})
	}

	return engine.Config{
		URL:               c.URL,
		Method:            c.Method,
		Headers:           headers,
		Body:              []byte(c.Body),
		VirtualUsers:      c.VirtualUsers,
		Duration:          time.Duration(c.DurationMS) * time.Millisecond,
		RequestTimeout:    time.Duration(c.RequestTimeoutMS) * time.Millisecond,
		MaxConnsPerHost:   c.MaxConnsPerHost,
		ReadBufferSize:    c.ReadBufferSize,
		WriteBufferSize:   c.WriteBufferSize,
		MaxResponseBytes:  c.MaxResponseBytes,
		LatencySampleRate: c.LatencySampleRate,
		RateLimitRPS:      c.RateLimitRPS,
		RampUp:            time.Duration(c.RampUpMS) * time.Millisecond,
		ScenarioSteps:     steps,
	}
}

func snapshotResponse(snapshot engine.Snapshot) SnapshotResponse {
	return SnapshotResponse{
		StartedAtUnixMs:   snapshot.StartedAt.UnixMilli(),
		AtUnixMs:          snapshot.At.UnixMilli(),
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
		TotalLatencyNano:  snapshot.TotalLatencyNano,
		MinLatencyNano:    snapshot.MinLatencyNano,
		MaxLatencyNano:    snapshot.MaxLatencyNano,
		P95LatencyNano:    snapshot.P95LatencyNano,
		P99LatencyNano:    snapshot.P99LatencyNano,
		P999LatencyNano:   snapshot.P999LatencyNano,
		BytesRead:         snapshot.BytesRead,
		BytesWritten:      snapshot.BytesWritten,
		StatusCodes:       buildStatusCodeCounts(snapshot.StatusCodes),
	}
}
