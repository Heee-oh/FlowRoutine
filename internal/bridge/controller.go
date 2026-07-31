package bridge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"flowroutine/internal/engine"
)

var ErrControllerNotStarted = errors.New("bridge controller has no active engine")

type StartRequest struct {
	Config          LoadConfig `json:"config"`
	BatchIntervalMS int        `json:"batchIntervalMs"`
}

type StartResponse struct {
	Started   bool              `json:"started"`
	Preflight PreflightResponse `json:"preflight"`
}

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ScenarioStep struct {
	Kind           string    `json:"kind"`
	URL            string    `json:"url"`
	Method         string    `json:"method"`
	Headers        []Header  `json:"headers"`
	Body           string    `json:"body"`
	DelayMS        int64     `json:"delayMs"`
	ExpectedStatus string    `json:"expectedStatus"`
	Captures       []Capture `json:"captures"`
}

type Capture struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Scope    string `json:"scope"`
	OnStatus string `json:"onStatus"`
}

type LoadStage struct {
	DurationMS int64 `json:"durationMs"`
	Target     int   `json:"target"`
}

type LoadProfile struct {
	Mode            string      `json:"mode"`
	StartTarget     int         `json:"startTarget"`
	Stages          []LoadStage `json:"stages"`
	PreAllocatedVUs int         `json:"preAllocatedVUs"`
	MaxVUs          int         `json:"maxVUs"`
	GracefulStopMS  int64       `json:"gracefulStopMs"`
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
	Profile           *LoadProfile   `json:"profile,omitempty"`
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
	CaptureFailures   uint64            `json:"captureFailures"`
	TemplateFailures  uint64            `json:"templateFailures"`
	DroppedIterations uint64            `json:"droppedIterations"`
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

type controllerState uint8

const (
	controllerIdle controllerState = iota
	controllerStarting
	controllerRunning
	controllerStopping
)

type engineFactory func(engine.Config) (*engine.Engine, error)
type batcherFactory func(*engine.Engine, Emitter, time.Duration) *Batcher

type Controller struct {
	emitter Emitter

	mu             sync.Mutex
	state          controllerState
	transitionDone chan struct{}
	engine         *engine.Engine
	batcher        *Batcher
	newEngine      engineFactory
	newBatcher     batcherFactory
}

func NewController(emitter Emitter) *Controller {
	return &Controller{
		emitter:    emitter,
		newEngine:  engine.New,
		newBatcher: defaultBatcherFactory,
	}
}

func (c *Controller) Start(ctx context.Context, req StartRequest) (StartResponse, error) {
	preflight, err := preflightStartRequest(req)
	if err != nil {
		return StartResponse{}, err
	}
	req.Config = preflight.normalizedConfig
	req.BatchIntervalMS = preflight.EffectiveBatchIntervalMS

	c.mu.Lock()
	if c.state != controllerIdle {
		c.mu.Unlock()
		return StartResponse{}, engine.ErrAlreadyRunning
	}
	c.state = controllerStarting
	transitionDone := make(chan struct{})
	c.transitionDone = transitionDone
	newEngine := c.newEngine
	if newEngine == nil {
		newEngine = engine.New
	}
	newBatcher := c.newBatcher
	if newBatcher == nil {
		newBatcher = defaultBatcherFactory
	}
	c.mu.Unlock()

	e, err := newEngine(req.Config.toEngineConfig())
	if err != nil {
		c.rollbackStart(transitionDone, nil, nil)
		return StartResponse{}, err
	}
	batcher := newBatcher(e, c.emitter, time.Duration(req.BatchIntervalMS)*time.Millisecond)

	if err := e.Start(ctx); err != nil {
		c.rollbackStart(transitionDone, e, batcher)
		return StartResponse{}, err
	}
	if err := batcher.Start(ctx); err != nil {
		c.rollbackStart(transitionDone, e, batcher)
		return StartResponse{}, err
	}

	c.mu.Lock()
	c.engine = e
	c.batcher = batcher
	c.state = controllerRunning
	c.completeTransitionLocked(transitionDone)
	c.mu.Unlock()

	go c.stopBatcherWhenEngineStops(e, batcher)
	return StartResponse{Started: true, Preflight: preflight}, nil
}

func (c *Controller) Preflight(req StartRequest) (PreflightResponse, error) {
	return preflightStartRequest(req)
}

func (c *Controller) Stop() error {
	for {
		c.mu.Lock()
		switch c.state {
		case controllerIdle:
			c.mu.Unlock()
			return nil
		case controllerStarting, controllerStopping:
			transitionDone := c.transitionDone
			c.mu.Unlock()
			if transitionDone != nil {
				<-transitionDone
			}
			continue
		case controllerRunning:
			e := c.engine
			batcher := c.batcher
			c.state = controllerStopping
			transitionDone := make(chan struct{})
			c.transitionDone = transitionDone
			c.mu.Unlock()

			stopErr := stopEngine(e)
			if batcher != nil {
				batcher.Stop()
			}

			c.mu.Lock()
			c.engine = nil
			c.batcher = nil
			c.state = controllerIdle
			c.completeTransitionLocked(transitionDone)
			c.mu.Unlock()
			return stopErr
		default:
			state := c.state
			c.mu.Unlock()
			return fmt.Errorf("invalid controller state %d", state)
		}
	}
}

func (c *Controller) rollbackStart(transitionDone chan struct{}, e *engine.Engine, batcher *Batcher) {
	_ = stopEngine(e)
	if batcher != nil {
		batcher.Stop()
	}

	c.mu.Lock()
	c.engine = nil
	c.batcher = nil
	c.state = controllerIdle
	c.completeTransitionLocked(transitionDone)
	c.mu.Unlock()
}

func (c *Controller) completeTransitionLocked(transitionDone chan struct{}) {
	if c.transitionDone != transitionDone {
		return
	}
	close(transitionDone)
	c.transitionDone = nil
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

	c.mu.Lock()
	if c.state == controllerRunning && c.engine == e {
		c.engine = nil
		c.batcher = nil
		c.state = controllerIdle
	}
	c.mu.Unlock()
}

func defaultBatcherFactory(e *engine.Engine, emitter Emitter, interval time.Duration) *Batcher {
	return NewBatcher(e, emitter, interval)
}

func stopEngine(e *engine.Engine) error {
	if e == nil {
		return nil
	}
	err := e.Stop()
	if errors.Is(err, engine.ErrNotRunning) {
		return nil
	}
	return err
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
		captures := make([]engine.VariableCapture, 0, len(step.Captures))
		for _, capture := range step.Captures {
			captures = append(captures, engine.VariableCapture{
				Name:     capture.Name,
				Path:     capture.Path,
				Scope:    engine.VariableScope(capture.Scope),
				OnStatus: capture.OnStatus,
			})
		}
		steps = append(steps, engine.ScenarioStep{
			Kind:           engine.StepKind(step.Kind),
			URL:            step.URL,
			Method:         step.Method,
			Headers:        stepHeaders,
			Body:           []byte(step.Body),
			Delay:          time.Duration(step.DelayMS) * time.Millisecond,
			ExpectedStatus: step.ExpectedStatus,
			Captures:       captures,
		})
	}
	var profile *engine.LoadProfile
	if c.Profile != nil {
		stages := make([]engine.LoadStage, 0, len(c.Profile.Stages))
		for _, stage := range c.Profile.Stages {
			stages = append(stages, engine.LoadStage{
				Duration: time.Duration(stage.DurationMS) * time.Millisecond,
				Target:   stage.Target,
			})
		}
		profile = &engine.LoadProfile{
			Mode:            engine.LoadMode(c.Profile.Mode),
			StartTarget:     c.Profile.StartTarget,
			Stages:          stages,
			PreAllocatedVUs: c.Profile.PreAllocatedVUs,
			MaxVUs:          c.Profile.MaxVUs,
			GracefulStop:    time.Duration(c.Profile.GracefulStopMS) * time.Millisecond,
		}
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
		Profile:           profile,
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
		CaptureFailures:   snapshot.CaptureFailures,
		TemplateFailures:  snapshot.TemplateFailures,
		DroppedIterations: snapshot.DroppedIterations,
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
