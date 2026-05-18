package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
)

var (
	ErrAlreadyRunning = errors.New("engine is already running")
	ErrNotRunning     = errors.New("engine is not running")
)

type Engine struct {
	cfg     compiledConfig
	client  *fasthttp.HostClient
	clients []*fasthttp.HostClient
	stats   AtomicStats

	reqPool  sync.Pool
	respPool sync.Pool
	limiter  *rateLimiter

	running atomic.Bool
	cancel  context.CancelFunc
	done    chan struct{}
	wg      sync.WaitGroup
}

func New(cfg Config) (*Engine, error) {
	compiled, err := compileConfig(cfg)
	if err != nil {
		return nil, err
	}

	engine := &Engine{cfg: compiled}
	engine.clients = make([]*fasthttp.HostClient, 0, len(compiled.clients))
	for _, client := range compiled.clients {
		engine.clients = append(engine.clients, newHostClient(compiled, client))
	}
	if len(engine.clients) > 0 {
		engine.client = engine.clients[0]
	}
	if compiled.rateLimitRPS > 0 {
		engine.limiter = newRateLimiter(compiled.rateLimitRPS)
	}
	engine.reqPool.New = func() any {
		return &fasthttp.Request{}
	}
	engine.respPool.New = func() any {
		return &fasthttp.Response{}
	}
	engine.stats.Init(compiled.virtualUsers)

	return engine, nil
}

func (e *Engine) Start(parent context.Context) error {
	if parent == nil {
		parent = context.Background()
	}
	if !e.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}

	var ctx context.Context
	var cancel context.CancelFunc
	if e.cfg.duration > 0 {
		ctx, cancel = context.WithTimeout(parent, e.cfg.duration)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}

	e.cancel = cancel
	e.done = make(chan struct{})
	e.stats.Reset(time.Now())
	if e.limiter != nil {
		e.limiter.Reset(time.Now())
	}
	e.wg.Add(e.cfg.virtualUsers)
	for i := 0; i < e.cfg.virtualUsers; i++ {
		go e.worker(ctx, i)
	}

	go func() {
		e.wg.Wait()
		e.running.Store(false)
		close(e.done)
	}()

	return nil
}

func (e *Engine) Stop() error {
	if !e.running.Load() {
		return ErrNotRunning
	}
	e.cancel()
	<-e.done
	return nil
}

func (e *Engine) Running() bool {
	return e.running.Load()
}

func (e *Engine) Done() <-chan struct{} {
	return e.done
}

func (e *Engine) Snapshot() Snapshot {
	return e.stats.Snapshot(time.Now())
}

func (e *Engine) worker(ctx context.Context, index int) {
	defer e.wg.Done()

	if !e.waitForRampUp(ctx, index) {
		return
	}

	stats := e.stats.Shard(index)
	sampleCountdown := e.cfg.latencySampleRate
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		var lastStatus int
		for i := range e.cfg.steps {
			if !e.runStep(ctx, stats, &sampleCountdown, &lastStatus, &e.cfg.steps[i]) {
				return
			}
		}
	}
}

func newHostClient(cfg compiledConfig, client compiledClient) *fasthttp.HostClient {
	return &fasthttp.HostClient{
		Addr:                          client.addr,
		IsTLS:                         client.isTLS,
		MaxConns:                      cfg.maxConnsPerHost,
		MaxIdleConnDuration:           10 * time.Second,
		ReadBufferSize:                cfg.readBufferSize,
		WriteBufferSize:               cfg.writeBufferSize,
		MaxResponseBodySize:           cfg.maxResponseBytes,
		NoDefaultUserAgentHeader:      true,
		DisableHeaderNamesNormalizing: true,
	}
}

func (e *Engine) runStep(ctx context.Context, stats *statsShard, sampleCountdown *int, lastStatus *int, step *compiledStep) bool {
	switch step.kind {
	case compiledDelay:
		return sleepContext(ctx, step.delay)
	case compiledAssertStatus:
		if !matchStatus(*lastStatus, step.expectedStatus) {
			stats.RecordAssertionFailure()
		}
		return true
	case compiledRequest:
		return e.runRequestStep(ctx, stats, sampleCountdown, lastStatus, step.request)
	default:
		return true
	}
}

func (e *Engine) runRequestStep(ctx context.Context, stats *statsShard, sampleCountdown *int, lastStatus *int, step compiledRequestStep) bool {
	if e.limiter != nil && !e.limiter.Wait(ctx) {
		return false
	}

	req := e.acquireRequest(step)
	resp := e.acquireResponse()
	sampleLatency := false
	if *sampleCountdown <= 1 {
		sampleLatency = true
		*sampleCountdown = e.cfg.latencySampleRate
	} else {
		*sampleCountdown = *sampleCountdown - 1
	}
	startedAt := time.Time{}
	if sampleLatency {
		startedAt = time.Now()
	}
	err := e.clients[step.clientIndex].DoTimeout(req, resp, e.cfg.requestTimeout)
	latency := time.Duration(0)
	if sampleLatency {
		latency = time.Since(startedAt)
	}

	if err != nil {
		*lastStatus = 0
		stats.RecordFailureSampled(latency, step.requestBytes, sampleLatency, ClassifyFailure(err))
	} else {
		*lastStatus = resp.StatusCode()
		if isSuccessStatus(*lastStatus) {
			stats.RecordHTTPSuccessSampled(latency, len(resp.Body()), step.requestBytes, sampleLatency, *lastStatus)
		} else {
			stats.RecordHTTPFailureSampled(latency, step.requestBytes, sampleLatency, FailureOther, *lastStatus)
		}
	}

	e.releaseResponse(resp)
	e.releaseRequest(req)
	return true
}

func isSuccessStatus(status int) bool {
	return status >= 200 && status < 400
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func matchStatus(status int, expected string) bool {
	if len(expected) == 3 && expected[1:] == "xx" {
		return status/100 == int(expected[0]-'0')
	}
	code := 0
	for _, ch := range expected {
		if ch < '0' || ch > '9' {
			return false
		}
		code = code*10 + int(ch-'0')
	}
	return status == code
}

func (e *Engine) waitForRampUp(ctx context.Context, index int) bool {
	if e.cfg.rampUp <= 0 || index == 0 {
		return true
	}
	delay := time.Duration(int64(e.cfg.rampUp) * int64(index) / int64(e.cfg.virtualUsers))
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (e *Engine) acquireRequest(step compiledRequestStep) *fasthttp.Request {
	req := e.reqPool.Get().(*fasthttp.Request)
	req.Reset()
	req.SetRequestURIBytes(step.requestURI)
	req.Header.SetHostBytes(step.hostHeader)
	req.Header.SetMethodBytes(step.method)
	for i := range step.headers {
		req.Header.SetBytesKV(step.headers[i].name, step.headers[i].value)
	}
	if len(step.body) > 0 {
		req.SetBodyRaw(step.body)
	}
	return req
}

func (e *Engine) releaseRequest(req *fasthttp.Request) {
	req.Reset()
	e.reqPool.Put(req)
}

func (e *Engine) acquireResponse() *fasthttp.Response {
	resp := e.respPool.Get().(*fasthttp.Response)
	resp.Reset()
	return resp
}

func (e *Engine) releaseResponse(resp *fasthttp.Response) {
	resp.Reset()
	e.respPool.Put(resp)
}
