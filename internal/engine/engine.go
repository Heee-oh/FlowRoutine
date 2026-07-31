package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

type workerVariables struct {
	iteration        map[string]string
	run              map[string]string
	renderBuffer     []byte
	eligibleCaptures []compiledVariableCapture
	captureValues    []string
}

func newWorkerVariables() *workerVariables {
	return &workerVariables{
		iteration: make(map[string]string),
		run:       make(map[string]string),
	}
}

func (variables *workerVariables) beginIteration() {
	clear(variables.iteration)
}

func (variables *workerVariables) value(name string) (string, bool) {
	if value, ok := variables.iteration[name]; ok {
		return value, true
	}
	value, ok := variables.run[name]
	return value, ok
}

func (variables *workerVariables) runValueExists(name string) bool {
	_, ok := variables.run[name]
	return ok
}

func (variables *workerVariables) set(capture compiledVariableCapture, value string) {
	if capture.scope == VariableScopeRun {
		if !variables.runValueExists(capture.name) {
			variables.run[capture.name] = value
		}
		return
	}
	variables.iteration[capture.name] = value
}

func (variables *workerVariables) invalidate(capture compiledVariableCapture) {
	if capture.scope == VariableScopeIteration {
		delete(variables.iteration, capture.name)
	}
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
	goroutines := e.cfg.virtualUsers
	if e.limiter != nil {
		e.limiter.Reset()
		goroutines++
	}
	e.wg.Add(goroutines)
	if e.limiter != nil {
		go func() {
			defer e.wg.Done()
			e.limiter.Run(ctx)
		}()
	}
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
	variables := newWorkerVariables()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		variables.beginIteration()
		var lastStatus int
		for i := range e.cfg.steps {
			if !e.runStep(ctx, stats, &sampleCountdown, &lastStatus, &e.cfg.steps[i], variables) {
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

func (e *Engine) runStep(ctx context.Context, stats *statsShard, sampleCountdown *int, lastStatus *int, step *compiledStep, variables *workerVariables) bool {
	switch step.kind {
	case compiledDelay:
		return sleepContext(ctx, step.delay)
	case compiledAssertStatus:
		if !matchStatus(*lastStatus, step.expectedStatus) {
			stats.RecordAssertionFailure()
		}
		return true
	case compiledRequest:
		return e.runRequestStep(ctx, stats, sampleCountdown, lastStatus, step.request, variables)
	default:
		return true
	}
}

func (e *Engine) runRequestStep(ctx context.Context, stats *statsShard, sampleCountdown *int, lastStatus *int, step compiledRequestStep, variables *workerVariables) bool {
	if e.limiter != nil && !e.limiter.Wait(ctx) {
		return false
	}

	req, err := e.acquireRequest(step, variables)
	if err != nil {
		variables.releaseRenderBuffer()
		*lastStatus = 0
		invalidateCaptures(step.captures, variables)
		stats.RecordTemplateFailure()
		return true
	}
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
	err = e.clients[step.clientIndex].DoTimeout(req, resp, e.cfg.requestTimeout)
	latency := time.Duration(0)
	if sampleLatency {
		latency = time.Since(startedAt)
	}

	if err != nil {
		*lastStatus = 0
		invalidateCaptures(step.captures, variables)
		stats.RecordFailureSampled(latency, step.requestBytes, sampleLatency, ClassifyFailure(err))
	} else {
		*lastStatus = resp.StatusCode()
		if isSuccessStatus(*lastStatus) {
			stats.RecordHTTPSuccessSampled(latency, len(resp.Body()), step.requestBytes, sampleLatency, *lastStatus)
		} else {
			stats.RecordHTTPFailureSampled(latency, step.requestBytes, sampleLatency, FailureOther, *lastStatus)
		}
		if len(step.captures) > 0 {
			if err := captureVariables(resp.Body(), *lastStatus, step.captures, variables); err != nil {
				stats.RecordCaptureFailure()
			}
		}
	}

	e.releaseResponse(resp)
	e.releaseRequest(req)
	variables.releaseRenderBuffer()
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

func (e *Engine) acquireRequest(step compiledRequestStep, variables *workerVariables) (*fasthttp.Request, error) {
	req := e.reqPool.Get().(*fasthttp.Request)
	req.Reset()
	if step.requestURITemplate.dynamic() {
		rendered, err := variables.render(step.requestURITemplate)
		if err != nil {
			e.releaseRequest(req)
			return nil, fmt.Errorf("request URL: %w", err)
		}
		req.SetRequestURIBytes(rendered)
	} else {
		req.SetRequestURIBytes(step.requestURI)
	}
	req.Header.SetHostBytes(step.hostHeader)
	req.Header.SetMethodBytes(step.method)
	for i := range step.headers {
		if step.headers[i].template.dynamic() {
			rendered, err := variables.render(step.headers[i].template)
			if err != nil {
				e.releaseRequest(req)
				return nil, fmt.Errorf("header %q: %w", step.headers[i].name, err)
			}
			req.Header.SetBytesKV(step.headers[i].name, rendered)
		} else {
			req.Header.SetBytesKV(step.headers[i].name, step.headers[i].value)
		}
	}
	if len(step.body) > 0 {
		if step.bodyTemplate.dynamic() {
			rendered, err := variables.render(step.bodyTemplate)
			if err != nil {
				e.releaseRequest(req)
				return nil, fmt.Errorf("request body: %w", err)
			}
			req.SetBodyRaw(rendered)
		} else {
			req.SetBodyRaw(step.body)
		}
	}
	return req, nil
}

func captureVariables(body []byte, status int, captures []compiledVariableCapture, variables *workerVariables) error {
	eligible := variables.eligibleCaptures[:0]
	for _, capture := range captures {
		if capture.scope == VariableScopeRun && variables.runValueExists(capture.name) {
			continue
		}
		if !captureStatusMatches(status, capture.onStatus) {
			variables.invalidate(capture)
			continue
		}
		eligible = append(eligible, capture)
	}
	variables.eligibleCaptures = eligible
	clear(variables.captureValues)
	if len(eligible) == 0 {
		return nil
	}

	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		invalidateCaptures(eligible, variables)
		return fmt.Errorf("capture response is invalid JSON: %w", err)
	}
	if cap(variables.captureValues) < len(eligible) {
		variables.captureValues = make([]string, len(eligible))
	} else {
		variables.captureValues = variables.captureValues[:len(eligible)]
	}
	values := variables.captureValues
	for index, capture := range eligible {
		value, err := jsonPathValue(document, capture.path)
		if err != nil {
			invalidateCaptures(eligible, variables)
			clear(values)
			return fmt.Errorf("capture %q path %q: %w", capture.name, formatJSONPath(capture.path), err)
		}
		values[index] = stringifyCaptureValue(value)
	}
	for index, capture := range eligible {
		variables.set(capture, values[index])
	}
	clear(values)
	return nil
}

func invalidateCaptures(captures []compiledVariableCapture, variables *workerVariables) {
	for _, capture := range captures {
		variables.invalidate(capture)
	}
}

func captureStatusMatches(status int, policy string) bool {
	switch policy {
	case CaptureStatusAny:
		return true
	case CaptureStatusSuccess:
		return isSuccessStatus(status)
	default:
		return matchStatus(status, policy)
	}
}

func jsonPathValue(value any, path []string) (any, error) {
	current := value
	for index, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[segment]
			if !ok {
				return nil, fmt.Errorf("segment %q was not found after %q", segment, formatJSONPath(path[:index]))
			}
			current = next
		case []any:
			arrayIndex, err := strconv.Atoi(segment)
			if err != nil {
				return nil, fmt.Errorf("segment %q must be an array index after %q", segment, formatJSONPath(path[:index]))
			}
			if arrayIndex < 0 || arrayIndex >= len(typed) {
				return nil, fmt.Errorf(
					"array index %d is out of range after %q (length %d)",
					arrayIndex,
					formatJSONPath(path[:index]),
					len(typed),
				)
			}
			current = typed[arrayIndex]
		default:
			return nil, fmt.Errorf(
				"cannot traverse segment %q after %q because the current value is %T",
				segment,
				formatJSONPath(path[:index]),
				current,
			)
		}
	}
	return current, nil
}

func formatJSONPath(path []string) string {
	if len(path) == 0 {
		return "$"
	}
	return strings.Join(path, ".")
}

func stringifyCaptureValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64, bool:
		return fmt.Sprint(typed)
	case nil:
		return ""
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
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
