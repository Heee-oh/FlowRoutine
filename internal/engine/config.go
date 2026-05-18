package engine

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"
)

const (
	DefaultMethod            = "GET"
	DefaultVirtualUsers      = 1
	DefaultRequestTimeout    = 5 * time.Second
	DefaultMaxConnsPerHost   = 10_000
	DefaultLatencySampleRate = 1
)

type Header struct {
	Name  string
	Value string
}

type Config struct {
	URL               string
	Method            string
	Headers           []Header
	Body              []byte
	VirtualUsers      int
	Duration          time.Duration
	RequestTimeout    time.Duration
	MaxConnsPerHost   int
	ReadBufferSize    int
	WriteBufferSize   int
	MaxResponseBytes  int
	LatencySampleRate int
	RateLimitRPS      int
	RampUp            time.Duration
	ScenarioSteps     []ScenarioStep
}

type StepKind string

const (
	StepRequest      StepKind = "request"
	StepDelay        StepKind = "delay"
	StepAssertStatus StepKind = "assertStatus"
)

type ScenarioStep struct {
	Kind           StepKind
	URL            string
	Method         string
	Headers        []Header
	Body           []byte
	Delay          time.Duration
	ExpectedStatus string
}

type compiledConfig struct {
	requestURI        []byte
	hostHeader        []byte
	addr              string
	isTLS             bool
	method            []byte
	headers           []compiledHeader
	body              []byte
	requestBytes      int
	virtualUsers      int
	duration          time.Duration
	requestTimeout    time.Duration
	maxConnsPerHost   int
	readBufferSize    int
	writeBufferSize   int
	maxResponseBytes  int
	latencySampleRate int
	rateLimitRPS      int
	rampUp            time.Duration
	steps             []compiledStep
	clients           []compiledClient
}

type compiledHeader struct {
	name  []byte
	value []byte
}

type compiledStepKind uint8

const (
	compiledRequest compiledStepKind = iota
	compiledDelay
	compiledAssertStatus
)

type compiledStep struct {
	kind           compiledStepKind
	request        compiledRequestStep
	delay          time.Duration
	expectedStatus string
}

type compiledRequestStep struct {
	clientIndex  int
	requestURI   []byte
	hostHeader   []byte
	method       []byte
	headers      []compiledHeader
	body         []byte
	requestBytes int
}

type compiledClient struct {
	addr  string
	isTLS bool
}

func compileConfig(cfg Config) (compiledConfig, error) {
	if cfg.URL == "" {
		return compiledConfig{}, errors.New("url is required")
	}
	target, err := url.Parse(cfg.URL)
	if err != nil {
		return compiledConfig{}, err
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return compiledConfig{}, fmt.Errorf("url scheme must be http or https: %s", target.Scheme)
	}
	if target.Host == "" {
		return compiledConfig{}, errors.New("url host is required")
	}
	if cfg.VirtualUsers < 0 {
		return compiledConfig{}, fmt.Errorf("virtual users must be >= 0: %d", cfg.VirtualUsers)
	}
	if cfg.MaxConnsPerHost < 0 {
		return compiledConfig{}, fmt.Errorf("max conns per host must be >= 0: %d", cfg.MaxConnsPerHost)
	}
	if cfg.RequestTimeout < 0 {
		return compiledConfig{}, fmt.Errorf("request timeout must be >= 0: %s", cfg.RequestTimeout)
	}
	if cfg.Duration < 0 {
		return compiledConfig{}, fmt.Errorf("duration must be >= 0: %s", cfg.Duration)
	}
	if cfg.RampUp < 0 {
		return compiledConfig{}, fmt.Errorf("ramp up must be >= 0: %s", cfg.RampUp)
	}
	if cfg.LatencySampleRate < 0 {
		return compiledConfig{}, fmt.Errorf("latency sample rate must be >= 0: %d", cfg.LatencySampleRate)
	}
	if cfg.RateLimitRPS < 0 {
		return compiledConfig{}, fmt.Errorf("rate limit rps must be >= 0: %d", cfg.RateLimitRPS)
	}

	method := cfg.Method
	if method == "" {
		method = DefaultMethod
	}
	virtualUsers := cfg.VirtualUsers
	if virtualUsers == 0 {
		virtualUsers = DefaultVirtualUsers
	}
	requestTimeout := cfg.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = DefaultRequestTimeout
	}
	maxConnsPerHost := cfg.MaxConnsPerHost
	if maxConnsPerHost == 0 {
		maxConnsPerHost = DefaultMaxConnsPerHost
	}
	latencySampleRate := cfg.LatencySampleRate
	if latencySampleRate == 0 {
		latencySampleRate = DefaultLatencySampleRate
	}

	steps, clients, err := compileScenarioSteps(cfg, method)
	if err != nil {
		return compiledConfig{}, err
	}
	firstRequest := firstCompiledRequest(steps)
	if firstRequest.requestURI == nil {
		return compiledConfig{}, errors.New("scenario requires at least one request step")
	}

	return compiledConfig{
		requestURI:        firstRequest.requestURI,
		hostHeader:        firstRequest.hostHeader,
		addr:              targetAddr(target),
		isTLS:             target.Scheme == "https",
		method:            firstRequest.method,
		headers:           firstRequest.headers,
		body:              firstRequest.body,
		requestBytes:      firstRequest.requestBytes,
		virtualUsers:      virtualUsers,
		duration:          cfg.Duration,
		requestTimeout:    requestTimeout,
		maxConnsPerHost:   maxConnsPerHost,
		readBufferSize:    cfg.ReadBufferSize,
		writeBufferSize:   cfg.WriteBufferSize,
		maxResponseBytes:  cfg.MaxResponseBytes,
		latencySampleRate: latencySampleRate,
		rateLimitRPS:      cfg.RateLimitRPS,
		rampUp:            cfg.RampUp,
		steps:             steps,
		clients:           clients,
	}, nil
}

func compileScenarioSteps(cfg Config, defaultMethod string) ([]compiledStep, []compiledClient, error) {
	steps := cfg.ScenarioSteps
	if len(steps) == 0 {
		steps = []ScenarioStep{{
			Kind:    StepRequest,
			URL:     cfg.URL,
			Method:  defaultMethod,
			Headers: cfg.Headers,
			Body:    cfg.Body,
		}}
	}

	clientIndexes := make(map[string]int)
	clients := make([]compiledClient, 0, len(steps))
	compiled := make([]compiledStep, 0, len(steps))
	for _, step := range steps {
		switch step.Kind {
		case "", StepRequest:
			target, err := parseTarget(step.URL)
			if err != nil {
				return nil, nil, err
			}
			clientKey := target.Scheme + "://" + targetAddr(target)
			clientIndex, ok := clientIndexes[clientKey]
			if !ok {
				clientIndex = len(clientIndexes)
				clientIndexes[clientKey] = clientIndex
				clients = append(clients, compiledClient{addr: targetAddr(target), isTLS: target.Scheme == "https"})
			}
			request, err := compileRequestStep(step, target, defaultMethod, clientIndex)
			if err != nil {
				return nil, nil, err
			}
			compiled = append(compiled, compiledStep{kind: compiledRequest, request: request})
		case StepDelay:
			if step.Delay < 0 {
				return nil, nil, fmt.Errorf("delay must be >= 0: %s", step.Delay)
			}
			compiled = append(compiled, compiledStep{kind: compiledDelay, delay: step.Delay})
		case StepAssertStatus:
			if step.ExpectedStatus == "" {
				return nil, nil, errors.New("assert status step requires expected status")
			}
			compiled = append(compiled, compiledStep{kind: compiledAssertStatus, expectedStatus: step.ExpectedStatus})
		default:
			return nil, nil, fmt.Errorf("unsupported scenario step kind: %s", step.Kind)
		}
	}
	return compiled, clients, nil
}

func compileRequestStep(step ScenarioStep, target *url.URL, defaultMethod string, clientIndex int) (compiledRequestStep, error) {
	method := step.Method
	if method == "" {
		method = defaultMethod
	}
	headers, err := compileHeaders(step.Headers)
	if err != nil {
		return compiledRequestStep{}, err
	}
	body := make([]byte, len(step.Body))
	copy(body, step.Body)

	requestBytes := len(method) + len(step.URL) + len(body)
	for _, h := range headers {
		requestBytes += len(h.name) + len(h.value)
	}

	return compiledRequestStep{
		clientIndex:  clientIndex,
		requestURI:   []byte(requestURI(target)),
		hostHeader:   []byte(target.Host),
		method:       []byte(method),
		headers:      headers,
		body:         body,
		requestBytes: requestBytes,
	}, nil
}

func compileHeaders(headers []Header) ([]compiledHeader, error) {
	compiled := make([]compiledHeader, 0, len(headers))
	for _, h := range headers {
		if h.Name == "" {
			return nil, errors.New("header name is required")
		}
		compiled = append(compiled, compiledHeader{
			name:  []byte(h.Name),
			value: []byte(h.Value),
		})
	}
	return compiled, nil
}

func firstCompiledRequest(steps []compiledStep) compiledRequestStep {
	for _, step := range steps {
		if step.kind == compiledRequest {
			return step.request
		}
	}
	return compiledRequestStep{}
}

func parseTarget(rawURL string) (*url.URL, error) {
	if rawURL == "" {
		return nil, errors.New("url is required")
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("url scheme must be http or https: %s", target.Scheme)
	}
	if target.Host == "" {
		return nil, errors.New("url host is required")
	}
	return target, nil
}

func requestURI(target *url.URL) string {
	uri := target.RequestURI()
	if uri == "" {
		return "/"
	}
	return uri
}

func targetAddr(target *url.URL) string {
	if target.Port() != "" {
		return target.Host
	}
	port := "80"
	if target.Scheme == "https" {
		port = "443"
	}
	return net.JoinHostPort(target.Hostname(), port)
}
