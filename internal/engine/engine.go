package engine

import (
	"bytes"
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
	engine.stats.Init(compiled.profile.maxWorkers)
	engine.stats.initRequestSteps(compiled.profile.maxWorkers, requestStepDescriptors(compiled.steps))

	return engine, nil
}

func (e *Engine) Start(parent context.Context) error {
	if parent == nil {
		parent = context.Background()
	}
	if !e.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}

	ctx, cancel := context.WithCancel(parent)

	e.cancel = cancel
	e.done = make(chan struct{})
	e.stats.Reset(time.Now())
	goroutines := 1
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
	go e.runLoadProfile(ctx, cancel)

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

func (e *Engine) RequestStepSnapshots() []RequestStepSnapshot {
	return e.stats.RequestStepSnapshots()
}

type workerRuntime struct {
	index            int
	stats            *statsShard
	sampleCountdown  int
	variables        *workerVariables
	assertionResults []bool
}

func (e *Engine) newWorkerRuntime(index int) workerRuntime {
	return workerRuntime{
		index:            index,
		stats:            e.stats.Shard(index),
		sampleCountdown:  e.cfg.latencySampleRate,
		variables:        newWorkerVariables(),
		assertionResults: make([]bool, e.cfg.assertionCount),
	}
}

type stepOutcome uint8

const (
	stepContinue stepOutcome = iota
	stepStopIteration
	stepCanceled
)

func (e *Engine) runIteration(ctx context.Context, runtime *workerRuntime) bool {
	runtime.variables.beginIteration()
	sampleLatency := runtime.nextLatencySample(e.cfg.latencySampleRate)
	var lastStatus int
	lastRequestMetricsIndex := -1
	for i := range e.cfg.steps {
		outcome := e.runStep(
			ctx,
			runtime,
			sampleLatency,
			&lastStatus,
			&lastRequestMetricsIndex,
			&e.cfg.steps[i],
		)
		if outcome == stepCanceled {
			return false
		}
		if outcome == stepStopIteration {
			return true
		}
	}
	return true
}

func (runtime *workerRuntime) nextLatencySample(sampleRate int) bool {
	if runtime.sampleCountdown <= 1 {
		runtime.sampleCountdown = max(1, sampleRate)
		return true
	}
	runtime.sampleCountdown--
	return false
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

func (e *Engine) runStep(
	ctx context.Context,
	runtime *workerRuntime,
	sampleLatency bool,
	lastStatus *int,
	lastRequestMetricsIndex *int,
	step *compiledStep,
) stepOutcome {
	switch step.kind {
	case compiledDelay:
		if !sleepContext(ctx, step.delay) {
			return stepCanceled
		}
		return stepContinue
	case compiledAssert:
		if runtime.assertionResults[step.assertion.resultIndex] {
			return stepContinue
		}
		countOnly := step.assertion.failureMode == AssertionCountOnly
		runtime.stats.RecordScenarioAssertionFailure(step.assertion.typeName, countOnly)
		if stepStats := e.stats.requestStepShard(*lastRequestMetricsIndex, runtime.index); stepStats != nil {
			stepStats.RecordScenarioAssertionFailure(step.assertion.typeName, countOnly)
		}
		if step.assertion.failureMode == AssertionStop {
			return stepStopIteration
		}
		return stepContinue
	case compiledRequest:
		*lastRequestMetricsIndex = step.request.metricsIndex
		return e.runRequestStep(ctx, runtime, sampleLatency, lastStatus, step.request)
	default:
		return stepContinue
	}
}

func (e *Engine) runRequestStep(
	ctx context.Context,
	runtime *workerRuntime,
	sampleLatency bool,
	lastStatus *int,
	step compiledRequestStep,
) stepOutcome {
	stepStartedAt := time.Time{}
	if hasAssertionType(step.assertions, AssertionStepLatency) {
		stepStartedAt = time.Now()
	}
	if e.limiter != nil && !e.limiter.Wait(ctx) {
		return stepCanceled
	}
	stepStats := e.stats.requestStepShard(step.metricsIndex, runtime.index)

	req, err := e.acquireRequest(step, runtime.variables)
	if err != nil {
		runtime.variables.releaseRenderBuffer()
		*lastStatus = 0
		invalidateCaptures(step.captures, runtime.variables)
		runtime.stats.RecordTemplateFailure()
		if stepStats != nil {
			stepStats.RecordTemplateFailure()
		}
		setFailedAssertionResults(step.assertions, runtime.assertionResults)
		return stepContinue
	}
	resp := e.acquireResponse()
	startedAt := time.Time{}
	measureResponseLatency := sampleLatency || hasAssertionType(step.assertions, AssertionResponseLatency)
	if measureResponseLatency {
		startedAt = time.Now()
	}
	err = e.clients[step.clientIndex].DoTimeout(req, resp, e.cfg.requestTimeout)
	latency := time.Duration(0)
	if measureResponseLatency {
		latency = time.Since(startedAt)
	}

	if err != nil {
		*lastStatus = 0
		invalidateCaptures(step.captures, runtime.variables)
		failure := ClassifyFailure(err)
		runtime.stats.RecordFailureSampled(latency, step.requestBytes, sampleLatency, failure)
		if stepStats != nil {
			stepStats.RecordHTTPFailureSampled(latency, sampleLatency, failure, 0)
		}
	} else {
		*lastStatus = resp.StatusCode()
		if isSuccessStatus(*lastStatus) {
			runtime.stats.RecordHTTPSuccessSampled(latency, len(resp.Body()), step.requestBytes, sampleLatency, *lastStatus)
			if stepStats != nil {
				stepStats.RecordHTTPSuccessSampled(latency, sampleLatency, *lastStatus)
			}
		} else {
			runtime.stats.RecordHTTPFailureSampled(latency, step.requestBytes, sampleLatency, FailureOther, *lastStatus)
			if stepStats != nil {
				stepStats.RecordHTTPFailureSampled(latency, sampleLatency, FailureOther, *lastStatus)
			}
		}
		if len(step.captures) > 0 {
			if err := captureVariables(resp.Body(), *lastStatus, step.captures, runtime.variables); err != nil {
				runtime.stats.RecordCaptureFailure()
				if stepStats != nil {
					stepStats.RecordCaptureFailure()
				}
			}
		}
	}
	stepLatency := time.Duration(0)
	if !stepStartedAt.IsZero() {
		stepLatency = time.Since(stepStartedAt)
	}
	evaluateAssertions(step.assertions, resp, err == nil, latency, stepLatency, runtime.assertionResults)

	e.releaseResponse(resp)
	e.releaseRequest(req)
	runtime.variables.releaseRenderBuffer()
	return stepContinue
}

func hasAssertionType(assertions []compiledAssertion, typeName AssertionType) bool {
	for index := range assertions {
		if assertions[index].typeName == typeName {
			return true
		}
	}
	return false
}

func setFailedAssertionResults(assertions []compiledAssertion, results []bool) {
	for index := range assertions {
		results[assertions[index].resultIndex] = false
	}
}

func evaluateAssertions(
	assertions []compiledAssertion,
	response *fasthttp.Response,
	requestSucceeded bool,
	responseLatency time.Duration,
	stepLatency time.Duration,
	results []bool,
) {
	var document any
	jsonParsed := false
	jsonValid := false
	for index := range assertions {
		assertion := &assertions[index]
		passed := false
		switch assertion.typeName {
		case AssertionStatus:
			passed = requestSucceeded && matchStatus(response.StatusCode(), assertion.expectedString)
		case AssertionHeader:
			if requestSucceeded {
				value, exists := responseHeaderValue(&response.Header, assertion.headerName)
				passed = exists && (assertion.operator == AssertionExists || bytes.Equal(value, []byte(assertion.expectedString)))
			}
		case AssertionJSON:
			if requestSucceeded {
				if !jsonParsed {
					jsonParsed = true
					jsonValid = json.Unmarshal(response.Body(), &document) == nil
				}
				if jsonValid {
					value, err := jsonPathValue(document, assertion.jsonPath)
					if err == nil {
						passed = assertion.operator == AssertionExists || jsonAssertionValueMatches(value, assertion)
					}
				}
			}
		case AssertionResponseLatency:
			passed = requestSucceeded && responseLatency <= assertion.maxLatency
		case AssertionStepLatency:
			passed = requestSucceeded && stepLatency <= assertion.maxLatency
		}
		results[assertion.resultIndex] = passed
	}
}

func responseHeaderValue(header *fasthttp.ResponseHeader, name []byte) ([]byte, bool) {
	var value []byte
	found := false
	header.VisitAll(func(key []byte, candidate []byte) {
		if !found && bytes.EqualFold(key, name) {
			value = candidate
			found = true
		}
	})
	return value, found
}

func jsonAssertionValueMatches(value any, assertion *compiledAssertion) bool {
	switch assertion.valueType {
	case AssertionValueString:
		typed, ok := value.(string)
		return ok && typed == assertion.expectedString
	case AssertionValueNumber:
		typed, ok := value.(float64)
		return ok && typed == assertion.expectedNumber
	case AssertionValueBoolean:
		typed, ok := value.(bool)
		return ok && typed == assertion.expectedBoolean
	case AssertionValueNull:
		return value == nil
	default:
		return false
	}
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
