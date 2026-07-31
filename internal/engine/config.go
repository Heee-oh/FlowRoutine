package engine

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
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
	Captures       []VariableCapture
}

type VariableCapture struct {
	Name     string
	Path     string
	Scope    VariableScope
	OnStatus string
}

type VariableScope string

const (
	VariableScopeIteration VariableScope = "iteration"
	VariableScopeRun       VariableScope = "run"
	CaptureStatusSuccess                 = "success"
	CaptureStatusAny                     = "any"
)

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
	name      []byte
	value     []byte
	templated bool
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
	clientIndex         int
	requestURI          []byte
	requestURITemplated bool
	hostHeader          []byte
	method              []byte
	headers             []compiledHeader
	body                []byte
	bodyTemplated       bool
	requestBytes        int
	captures            []compiledVariableCapture
	templateNames       []string
}

type compiledVariableCapture struct {
	name     string
	path     []string
	scope    VariableScope
	onStatus string
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

func ValidateConfig(cfg Config) error {
	_, err := compileConfig(cfg)
	return err
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
	availableVariables := make(map[string]VariableScope)
	clients := make([]compiledClient, 0, len(steps))
	compiled := make([]compiledStep, 0, len(steps))
	for index, step := range steps {
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
			for _, name := range request.templateNames {
				if _, ok := availableVariables[name]; !ok {
					return nil, nil, fmt.Errorf(
						"scenario step %d template variable %q is not defined by an earlier capture",
						index+1,
						name,
					)
				}
			}
			for _, capture := range request.captures {
				if scope, ok := availableVariables[capture.name]; ok && scope != capture.scope {
					return nil, nil, fmt.Errorf(
						"scenario step %d capture %q changes scope from %q to %q",
						index+1,
						capture.name,
						scope,
						capture.scope,
					)
				}
				availableVariables[capture.name] = capture.scope
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
	if names, err := parseTemplateNames(method); err != nil {
		return compiledRequestStep{}, fmt.Errorf("request method template: %w", err)
	} else if len(names) > 0 {
		return compiledRequestStep{}, errors.New("request method cannot contain templates")
	}
	if names, err := parseTemplateNames(target.Host); err != nil {
		return compiledRequestStep{}, fmt.Errorf("request host template: %w", err)
	} else if len(names) > 0 {
		return compiledRequestStep{}, errors.New("request host cannot contain templates")
	}
	headers, headerTemplates, err := compileHeaders(step.Headers)
	if err != nil {
		return compiledRequestStep{}, err
	}
	body := make([]byte, len(step.Body))
	copy(body, step.Body)
	captures, err := compileVariableCaptures(step.Captures)
	if err != nil {
		return compiledRequestStep{}, err
	}
	rawURI := rawRequestURI(step.URL)
	uriTemplates, err := parseTemplateNames(rawURI)
	if err != nil {
		return compiledRequestStep{}, fmt.Errorf("request URL template: %w", err)
	}
	uri, err := requestURI(step.URL)
	if err != nil {
		return compiledRequestStep{}, fmt.Errorf("request URL: %w", err)
	}
	bodyTemplates, err := parseTemplateNames(string(body))
	if err != nil {
		return compiledRequestStep{}, fmt.Errorf("request body template: %w", err)
	}
	templateNames := uniqueStrings(append(append(uriTemplates, headerTemplates...), bodyTemplates...))

	requestBytes := len(method) + len(step.URL) + len(body)
	for _, h := range headers {
		requestBytes += len(h.name) + len(h.value)
	}

	return compiledRequestStep{
		clientIndex:         clientIndex,
		requestURI:          []byte(uri),
		requestURITemplated: len(uriTemplates) > 0,
		hostHeader:          []byte(target.Host),
		method:              []byte(method),
		headers:             headers,
		body:                body,
		bodyTemplated:       len(bodyTemplates) > 0,
		requestBytes:        requestBytes,
		captures:            captures,
		templateNames:       templateNames,
	}, nil
}

func compileHeaders(headers []Header) ([]compiledHeader, []string, error) {
	compiled := make([]compiledHeader, 0, len(headers))
	templateNames := make([]string, 0)
	for _, h := range headers {
		if h.Name == "" {
			return nil, nil, errors.New("header name is required")
		}
		if names, err := parseTemplateNames(h.Name); err != nil {
			return nil, nil, fmt.Errorf("header name template: %w", err)
		} else if len(names) > 0 {
			return nil, nil, errors.New("header names cannot contain templates")
		}
		names, err := parseTemplateNames(h.Value)
		if err != nil {
			return nil, nil, fmt.Errorf("header %q value template: %w", h.Name, err)
		}
		compiled = append(compiled, compiledHeader{
			name:      []byte(h.Name),
			value:     []byte(h.Value),
			templated: len(names) > 0,
		})
		templateNames = append(templateNames, names...)
	}
	return compiled, uniqueStrings(templateNames), nil
}

func compileVariableCaptures(captures []VariableCapture) ([]compiledVariableCapture, error) {
	compiled := make([]compiledVariableCapture, 0, len(captures))
	names := make(map[string]struct{}, len(captures))
	for _, capture := range captures {
		name := strings.TrimSpace(capture.Name)
		path := strings.TrimSpace(capture.Path)
		if name == "" && path == "" {
			continue
		}
		if name == "" || path == "" {
			return nil, errors.New("capture name and path are required")
		}
		if err := validateVariableName(name); err != nil {
			return nil, fmt.Errorf("capture name %q: %w", name, err)
		}
		if _, exists := names[name]; exists {
			return nil, fmt.Errorf("capture name %q is duplicated in one request step", name)
		}
		names[name] = struct{}{}
		segments, err := parseJSONPath(path)
		if err != nil {
			return nil, fmt.Errorf("capture %q path %q: %w", name, path, err)
		}
		scope := VariableScope(strings.ToLower(strings.TrimSpace(string(capture.Scope))))
		if scope == "" {
			scope = VariableScopeIteration
		}
		if scope != VariableScopeIteration && scope != VariableScopeRun {
			return nil, fmt.Errorf("capture %q scope must be %q or %q", name, VariableScopeIteration, VariableScopeRun)
		}
		onStatus := strings.ToLower(strings.TrimSpace(capture.OnStatus))
		if onStatus == "" {
			onStatus = CaptureStatusSuccess
		}
		if !validCaptureStatus(onStatus) {
			return nil, fmt.Errorf(
				"capture %q status policy must be %q, %q, a status code, or a class such as 2xx",
				name,
				CaptureStatusSuccess,
				CaptureStatusAny,
			)
		}
		compiled = append(compiled, compiledVariableCapture{
			name:     name,
			path:     segments,
			scope:    scope,
			onStatus: onStatus,
		})
	}
	return compiled, nil
}

func parseTemplateNames(value string) ([]string, error) {
	names := make([]string, 0)
	seen := make(map[string]struct{})
	for offset := 0; offset < len(value); {
		remaining := value[offset:]
		start := strings.Index(remaining, "{{")
		unexpectedClose := strings.Index(remaining, "}}")
		if start < 0 {
			if unexpectedClose >= 0 {
				return nil, errors.New("contains an unexpected closing delimiter")
			}
			break
		}
		if unexpectedClose >= 0 && unexpectedClose < start {
			return nil, errors.New("contains an unexpected closing delimiter")
		}
		start += offset
		endOffset := strings.Index(value[start+2:], "}}")
		if endOffset < 0 {
			return nil, errors.New("contains an unclosed template")
		}
		end := start + 2 + endOffset
		name := strings.TrimSpace(value[start+2 : end])
		if err := validateVariableName(name); err != nil {
			return nil, fmt.Errorf("variable %q: %w", name, err)
		}
		if _, exists := seen[name]; !exists {
			seen[name] = struct{}{}
			names = append(names, name)
		}
		offset = end + 2
	}
	return names, nil
}

func validateVariableName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if strings.HasPrefix(strings.ToUpper(name), "SECRET_") {
		return errors.New("SECRET_ prefix is reserved for runtime bindings")
	}
	for index, character := range name {
		valid := character == '_' ||
			character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z'
		if index > 0 {
			valid = valid ||
				character == '-' ||
				character == '.' ||
				character >= '0' && character <= '9'
		}
		if !valid {
			return errors.New("must start with a letter or underscore and contain only letters, numbers, dots, dashes, or underscores")
		}
	}
	return nil
}

func parseJSONPath(path string) ([]string, error) {
	if path == "$" {
		return nil, nil
	}
	if strings.HasPrefix(path, "$.") {
		path = path[2:]
		if path == "" || path[0] == '.' || path[0] == '[' || path[0] == ']' {
			return nil, errors.New("path contains an empty segment")
		}
	} else if strings.HasPrefix(path, "$") {
		path = path[1:]
		if !strings.HasPrefix(path, "[") {
			return nil, errors.New("$ must be followed by a dot or array index")
		}
	}
	if path == "" {
		return nil, errors.New("path is required")
	}

	segments := make([]string, 0, strings.Count(path, ".")+1)
	for len(path) > 0 {
		if path[0] == '[' {
			closeAt := strings.IndexByte(path, ']')
			if closeAt < 0 {
				return nil, errors.New("array index is missing a closing bracket")
			}
			rawIndex := path[1:closeAt]
			index, err := strconv.Atoi(rawIndex)
			if err != nil || index < 0 {
				return nil, fmt.Errorf("array index %q must be a non-negative integer", rawIndex)
			}
			segments = append(segments, strconv.Itoa(index))
			path = path[closeAt+1:]
		} else {
			end := len(path)
			if dotAt := strings.IndexByte(path, '.'); dotAt >= 0 && dotAt < end {
				end = dotAt
			}
			if bracketAt := strings.IndexByte(path, '['); bracketAt >= 0 && bracketAt < end {
				end = bracketAt
			}
			if end == 0 {
				return nil, errors.New("path contains an empty segment")
			}
			segment := path[:end]
			if strings.ContainsRune(segment, ']') {
				return nil, errors.New("path contains an unexpected closing bracket")
			}
			segments = append(segments, segment)
			path = path[end:]
		}

		if path == "" {
			break
		}
		switch path[0] {
		case '.':
			path = path[1:]
			if path == "" || path[0] == '.' || path[0] == '[' || path[0] == ']' {
				return nil, errors.New("path contains an empty segment")
			}
		case '[':
			// Parse the next array index without consuming it here.
		default:
			return nil, fmt.Errorf("unexpected character %q after path segment", path[0])
		}
	}
	return segments, nil
}

func validCaptureStatus(value string) bool {
	if value == CaptureStatusSuccess || value == CaptureStatusAny {
		return true
	}
	if len(value) == 3 && value[0] >= '1' && value[0] <= '5' && value[1:] == "xx" {
		return true
	}
	if len(value) != 3 {
		return false
	}
	code, err := strconv.Atoi(value)
	return err == nil && code >= 100 && code <= 599
}

func uniqueStrings(values []string) []string {
	unique := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
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

func requestURI(rawURL string) (string, error) {
	transformed := rawURL
	templates := templateTokens(rawURL)
	sentinels := make([]string, len(templates))
	for index, template := range templates {
		sentinel := fmt.Sprintf("__flowroutine_template_%d__", index)
		for strings.Contains(rawURL, sentinel) {
			sentinel += "_"
		}
		sentinels[index] = sentinel
		transformed = strings.Replace(transformed, template, sentinel, 1)
	}

	target, err := url.Parse(transformed)
	if err != nil {
		return "", err
	}
	uri := target.RequestURI()
	if uri == "" {
		uri = "/"
	}
	for index, sentinel := range sentinels {
		uri = strings.ReplaceAll(uri, sentinel, templates[index])
	}
	return uri, nil
}

func rawRequestURI(rawURL string) string {
	schemeAt := strings.Index(rawURL, "://")
	if schemeAt < 0 {
		return rawURL
	}
	authorityStart := schemeAt + 3
	remainderAt := strings.IndexAny(rawURL[authorityStart:], "/?#")
	if remainderAt < 0 {
		return ""
	}
	remainder := rawURL[authorityStart+remainderAt:]
	if fragmentAt := strings.IndexByte(remainder, '#'); fragmentAt >= 0 {
		remainder = remainder[:fragmentAt]
	}
	return remainder
}

func templateTokens(value string) []string {
	tokens := make([]string, 0)
	for offset := 0; offset < len(value); {
		startOffset := strings.Index(value[offset:], "{{")
		if startOffset < 0 {
			break
		}
		start := offset + startOffset
		endOffset := strings.Index(value[start+2:], "}}")
		if endOffset < 0 {
			break
		}
		end := start + 2 + endOffset + 2
		tokens = append(tokens, value[start:end])
		offset = end
	}
	return tokens
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
