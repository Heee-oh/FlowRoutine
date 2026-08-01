package bridge

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"flowroutine/internal/engine"
)

const (
	MinDuration              = time.Millisecond
	MaxDuration              = time.Hour
	MaxRampUp                = time.Hour
	MaxRequestDelay          = 5 * time.Minute
	MaxVirtualUsers          = 100_000
	MaxConnectionsPerHost    = 100_000
	DefaultReadBufferBytes   = 4 << 10
	DefaultWriteBufferBytes  = 4 << 10
	DefaultResponseLimit     = 1 << 20
	MinIOBufferBytes         = 1 << 10
	MaxIOBufferBytes         = 1 << 20
	MinResponseLimit         = 1 << 10
	MaxResponseLimit         = 64 << 20
	MaxScenarioSteps         = 512
	MaxScenarioBytes         = 64 << 20
	MaxScenarioHosts         = 32
	MaxHeadersPerRequest     = 128
	MaxHeaderNameBytes       = 256
	MaxHeaderValueBytes      = 16 << 10
	MaxHeaderBytesPerRequest = 64 << 10
	MaxRequestBodyBytes      = 16 << 20
	MaxURLBytes              = 8 << 10
	MaxMethodBytes           = 32
	MaxCapturesPerStep       = 64
	MaxCaptureNameBytes      = 128
	MaxCapturePathBytes      = 1 << 10
	MaxLatencySampleRate     = 1_000_000
	MaxRateLimitRPS          = engine.MaxRateLimitRPS
	MaxEstimatedConnections  = 50_000
	WorkerMemoryOverhead     = 64 << 10
)

const (
	WarningMemoryBytes      uint64 = 512 << 20
	MaxEstimatedMemoryBytes uint64 = 8 << 30
	WarningConnections             = 5_000
	WarningScenarioHosts           = 8
)

type PreflightResponse struct {
	EffectiveConfig          EffectiveLoadConfig `json:"effectiveConfig"`
	EffectiveBatchIntervalMS int                 `json:"effectiveBatchIntervalMs"`
	Estimate                 PreflightEstimate   `json:"estimate"`
	Warnings                 []PreflightWarning  `json:"warnings"`
	normalizedConfig         LoadConfig
}

type EffectiveLoadConfig struct {
	VirtualUsers      int   `json:"virtualUsers"`
	DurationMS        int64 `json:"durationMs"`
	RequestTimeoutMS  int64 `json:"requestTimeoutMs"`
	MaxConnsPerHost   int   `json:"maxConnsPerHost"`
	ReadBufferSize    int   `json:"readBufferSize"`
	WriteBufferSize   int   `json:"writeBufferSize"`
	MaxResponseBytes  int   `json:"maxResponseBytes"`
	LatencySampleRate int   `json:"latencySampleRate"`
	RateLimitRPS      int   `json:"rateLimitRps"`
	RampUpMS          int64 `json:"rampUpMs"`
}

type PreflightEstimate struct {
	MemoryBytes uint64 `json:"memoryBytes"`
	Connections int    `json:"connections"`
	TargetHosts int    `json:"targetHosts"`
}

type PreflightWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func preflightStartRequest(request StartRequest) (PreflightResponse, error) {
	config, err := normalizeLoadConfig(request.Config)
	if err != nil {
		return PreflightResponse{}, err
	}
	batchIntervalMS, err := normalizeBatchInterval(request.BatchIntervalMS)
	if err != nil {
		return PreflightResponse{}, err
	}
	estimate, err := estimateResources(config)
	if err != nil {
		return PreflightResponse{}, err
	}

	warnings := make([]PreflightWarning, 0, 3)
	if estimate.MemoryBytes >= WarningMemoryBytes {
		warnings = append(warnings, PreflightWarning{
			Code: "high_memory",
			Message: fmt.Sprintf(
				"Estimated peak request memory is %s; reduce virtual users, response limit, or request body size.",
				formatResourceBytes(estimate.MemoryBytes),
			),
		})
	}
	if estimate.Connections >= WarningConnections {
		warnings = append(warnings, PreflightWarning{
			Code: "high_connections",
			Message: fmt.Sprintf(
				"Up to %d concurrent connections may be opened; reduce virtual users or the per-host connection limit.",
				estimate.Connections,
			),
		})
	}
	if estimate.TargetHosts >= WarningScenarioHosts {
		warnings = append(warnings, PreflightWarning{
			Code: "many_hosts",
			Message: fmt.Sprintf(
				"The scenario targets %d hosts; split it or reduce host fan-out to limit connection pools.",
				estimate.TargetHosts,
			),
		})
	}

	return PreflightResponse{
		EffectiveConfig:          effectiveLoadConfig(config),
		EffectiveBatchIntervalMS: batchIntervalMS,
		Estimate:                 estimate,
		Warnings:                 warnings,
		normalizedConfig:         config,
	}, nil
}

func effectiveLoadConfig(config LoadConfig) EffectiveLoadConfig {
	return EffectiveLoadConfig{
		VirtualUsers:      config.VirtualUsers,
		DurationMS:        config.DurationMS,
		RequestTimeoutMS:  config.RequestTimeoutMS,
		MaxConnsPerHost:   config.MaxConnsPerHost,
		ReadBufferSize:    config.ReadBufferSize,
		WriteBufferSize:   config.WriteBufferSize,
		MaxResponseBytes:  config.MaxResponseBytes,
		LatencySampleRate: config.LatencySampleRate,
		RateLimitRPS:      config.RateLimitRPS,
		RampUpMS:          config.RampUpMS,
	}
}

func normalizeLoadConfig(config LoadConfig) (LoadConfig, error) {
	next := config
	next.URL = strings.TrimSpace(config.URL)
	if _, err := validateHTTPURL("url", next.URL); err != nil {
		return LoadConfig{}, err
	}
	next.Method = strings.TrimSpace(config.Method)
	if next.Method == "" {
		next.Method = engine.DefaultMethod
	}
	if err := validateHTTPToken("method", next.Method, MaxMethodBytes); err != nil {
		return LoadConfig{}, err
	}

	headers, _, err := normalizeHeaders("headers", config.Headers)
	if err != nil {
		return LoadConfig{}, err
	}
	next.Headers = headers
	if len(config.Body) > MaxRequestBodyBytes {
		return LoadConfig{}, fmt.Errorf("body must be at most %d bytes", MaxRequestBodyBytes)
	}

	if config.VirtualUsers < 1 || config.VirtualUsers > MaxVirtualUsers {
		return LoadConfig{}, fmt.Errorf("virtual users must be between 1 and %d", MaxVirtualUsers)
	}
	if config.DurationMS < MinDuration.Milliseconds() || config.DurationMS > MaxDuration.Milliseconds() {
		return LoadConfig{}, fmt.Errorf(
			"duration must be between %s and %s",
			MinDuration,
			MaxDuration,
		)
	}
	if config.RequestTimeoutMS < 1 || config.RequestTimeoutMS > MaxRequestDelay.Milliseconds() {
		return LoadConfig{}, fmt.Errorf(
			"request timeout must be between 1ms and %s",
			MaxRequestDelay,
		)
	}
	if config.RampUpMS < 0 || config.RampUpMS > MaxRampUp.Milliseconds() {
		return LoadConfig{}, fmt.Errorf("ramp-up must be between 0 and %s", MaxRampUp)
	}

	next.MaxConnsPerHost, err = normalizeBoundedDefault(
		"max connections per host",
		config.MaxConnsPerHost,
		engine.DefaultMaxConnsPerHost,
		1,
		MaxConnectionsPerHost,
	)
	if err != nil {
		return LoadConfig{}, err
	}
	next.ReadBufferSize, err = normalizeBoundedDefault(
		"read buffer size",
		config.ReadBufferSize,
		DefaultReadBufferBytes,
		MinIOBufferBytes,
		MaxIOBufferBytes,
	)
	if err != nil {
		return LoadConfig{}, err
	}
	next.WriteBufferSize, err = normalizeBoundedDefault(
		"write buffer size",
		config.WriteBufferSize,
		DefaultWriteBufferBytes,
		MinIOBufferBytes,
		MaxIOBufferBytes,
	)
	if err != nil {
		return LoadConfig{}, err
	}
	next.MaxResponseBytes, err = normalizeBoundedDefault(
		"max response bytes",
		config.MaxResponseBytes,
		DefaultResponseLimit,
		MinResponseLimit,
		MaxResponseLimit,
	)
	if err != nil {
		return LoadConfig{}, err
	}
	next.LatencySampleRate, err = normalizeBoundedDefault(
		"latency sample rate",
		config.LatencySampleRate,
		engine.DefaultLatencySampleRate,
		1,
		MaxLatencySampleRate,
	)
	if err != nil {
		return LoadConfig{}, err
	}
	if config.RateLimitRPS < 0 || config.RateLimitRPS > MaxRateLimitRPS {
		return LoadConfig{}, fmt.Errorf("rate limit rps must be between 0 and %d", MaxRateLimitRPS)
	}

	next.ScenarioSteps, err = normalizeScenarioSteps(config.ScenarioSteps, next.Method)
	if err != nil {
		return LoadConfig{}, err
	}
	if err := engine.ValidateConfig(next.toEngineConfig()); err != nil {
		return LoadConfig{}, fmt.Errorf("scenario configuration: %w", err)
	}
	return next, nil
}

func normalizeScenarioSteps(steps []ScenarioStep, defaultMethod string) ([]ScenarioStep, error) {
	if len(steps) > MaxScenarioSteps {
		return nil, fmt.Errorf("scenario must have at most %d steps", MaxScenarioSteps)
	}
	if len(steps) == 0 {
		return make([]ScenarioStep, 0), nil
	}

	normalized := make([]ScenarioStep, 0, len(steps))
	requests := 0
	scenarioBytes := 0
	for index, step := range steps {
		scenarioBytes += scenarioStepBytes(step)
		if scenarioBytes > MaxScenarioBytes {
			return nil, fmt.Errorf("scenario must total at most %d bytes", MaxScenarioBytes)
		}
		next := step
		next.Kind = strings.TrimSpace(step.Kind)
		if next.Kind == "" {
			next.Kind = string(engine.StepRequest)
		}
		scope := fmt.Sprintf("scenario step %d", index+1)
		switch engine.StepKind(next.Kind) {
		case engine.StepRequest:
			requests++
			next.URL = strings.TrimSpace(step.URL)
			if _, err := validateHTTPURL(scope+" url", next.URL); err != nil {
				return nil, err
			}
			next.Method = strings.TrimSpace(step.Method)
			if next.Method == "" {
				next.Method = defaultMethod
			}
			if err := validateHTTPToken(scope+" method", next.Method, MaxMethodBytes); err != nil {
				return nil, err
			}
			headers, _, err := normalizeHeaders(scope+" headers", step.Headers)
			if err != nil {
				return nil, err
			}
			next.Headers = headers
			if len(step.Body) > MaxRequestBodyBytes {
				return nil, fmt.Errorf("%s body must be at most %d bytes", scope, MaxRequestBodyBytes)
			}
			captures, err := normalizeCaptures(scope, step.Captures)
			if err != nil {
				return nil, err
			}
			next.Captures = captures
		case engine.StepDelay:
			if step.DelayMS < 0 || step.DelayMS > MaxRequestDelay.Milliseconds() {
				return nil, fmt.Errorf("%s delay must be between 0 and %s", scope, MaxRequestDelay)
			}
		case engine.StepAssertStatus:
			next.ExpectedStatus = strings.TrimSpace(step.ExpectedStatus)
			if next.ExpectedStatus == "" {
				return nil, fmt.Errorf("%s expected status is required", scope)
			}
			if len(next.ExpectedStatus) > MaxMethodBytes {
				return nil, fmt.Errorf("%s expected status must be at most %d bytes", scope, MaxMethodBytes)
			}
		default:
			return nil, fmt.Errorf("%s has unsupported kind %q", scope, next.Kind)
		}
		normalized = append(normalized, next)
	}
	if requests == 0 {
		return nil, errors.New("scenario requires at least one request step")
	}
	return normalized, nil
}

func normalizeHeaders(scope string, headers []Header) ([]Header, int, error) {
	if len(headers) > MaxHeadersPerRequest {
		return nil, 0, fmt.Errorf("%s must have at most %d entries", scope, MaxHeadersPerRequest)
	}
	normalized := make([]Header, 0, len(headers))
	totalBytes := 0
	for index, header := range headers {
		next := Header{Name: strings.TrimSpace(header.Name), Value: header.Value}
		if err := validateHTTPToken(
			fmt.Sprintf("%s header %d name", scope, index+1),
			next.Name,
			MaxHeaderNameBytes,
		); err != nil {
			return nil, 0, err
		}
		if len(next.Value) > MaxHeaderValueBytes {
			return nil, 0, fmt.Errorf(
				"%s header %d value must be at most %d bytes",
				scope,
				index+1,
				MaxHeaderValueBytes,
			)
		}
		for _, character := range next.Value {
			if character == 0x7f || (character < 0x20 && character != '\t') {
				return nil, 0, fmt.Errorf("%s header %d value contains a forbidden control character", scope, index+1)
			}
		}
		totalBytes += len(next.Name) + len(next.Value) + 4
		if totalBytes > MaxHeaderBytesPerRequest {
			return nil, 0, fmt.Errorf("%s must total at most %d bytes", scope, MaxHeaderBytesPerRequest)
		}
		normalized = append(normalized, next)
	}
	return normalized, totalBytes, nil
}

func normalizeCaptures(scope string, captures []Capture) ([]Capture, error) {
	if len(captures) > MaxCapturesPerStep {
		return nil, fmt.Errorf("%s must have at most %d captures", scope, MaxCapturesPerStep)
	}
	normalized := make([]Capture, 0, len(captures))
	for index, capture := range captures {
		next := Capture{
			Name:     strings.TrimSpace(capture.Name),
			Path:     strings.TrimSpace(capture.Path),
			Scope:    strings.ToLower(strings.TrimSpace(capture.Scope)),
			OnStatus: strings.ToLower(strings.TrimSpace(capture.OnStatus)),
		}
		if next.Scope == "" {
			next.Scope = string(engine.VariableScopeIteration)
		}
		if next.OnStatus == "" {
			next.OnStatus = engine.CaptureStatusSuccess
		}
		if next.Name == "" || next.Path == "" {
			return nil, fmt.Errorf("%s capture %d name and path are required", scope, index+1)
		}
		if len(next.Name) > MaxCaptureNameBytes {
			return nil, fmt.Errorf("%s capture %d name must be at most %d bytes", scope, index+1, MaxCaptureNameBytes)
		}
		if len(next.Path) > MaxCapturePathBytes {
			return nil, fmt.Errorf("%s capture %d path must be at most %d bytes", scope, index+1, MaxCapturePathBytes)
		}
		normalized = append(normalized, next)
	}
	return normalized, nil
}

func scenarioStepBytes(step ScenarioStep) int {
	total := len(step.Kind) + len(step.URL) + len(step.Method) + len(step.Body) + len(step.ExpectedStatus)
	for _, header := range step.Headers {
		total += len(header.Name) + len(header.Value) + 4
	}
	for _, capture := range step.Captures {
		total += len(capture.Name) + len(capture.Path) + len(capture.Scope) + len(capture.OnStatus)
	}
	return total
}

func normalizeBatchInterval(intervalMS int) (int, error) {
	if intervalMS == 0 {
		return int(DefaultInterval / time.Millisecond), nil
	}
	minimum := int(MinInterval / time.Millisecond)
	maximum := int(MaxInterval / time.Millisecond)
	if intervalMS < minimum || intervalMS > maximum {
		return 0, fmt.Errorf("batch interval must be between %dms and %dms", minimum, maximum)
	}
	return intervalMS, nil
}

func normalizeBoundedDefault(name string, value int, defaultValue int, minimum int, maximum int) (int, error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return value, nil
}

func validateHTTPURL(name string, rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	if len(rawURL) > MaxURLBytes {
		return "", fmt.Errorf("%s must be at most %d bytes", name, MaxURLBytes)
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("%s is invalid: %w", name, err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return "", fmt.Errorf("%s scheme must be http or https", name)
	}
	if target.Host == "" {
		return "", fmt.Errorf("%s host is required", name)
	}
	return strings.ToLower(target.Scheme + "://" + target.Host), nil
}

func validateHTTPToken(name string, value string, maximum int) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > maximum {
		return fmt.Errorf("%s must be at most %d bytes", name, maximum)
	}
	for _, character := range value {
		if character <= 0x20 || character >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", character) {
			return fmt.Errorf("%s contains an invalid HTTP token character", name)
		}
	}
	return nil
}

func estimateResources(config LoadConfig) (PreflightEstimate, error) {
	hosts := make(map[string]struct{})
	largestBody := len(config.Body)
	sharedBytes := len(config.URL) + len(config.Method) + len(config.Body)
	_, baseHeaderBytes, _ := normalizeHeaders("headers", config.Headers)
	sharedBytes += baseHeaderBytes

	if len(config.ScenarioSteps) == 0 {
		host, _ := validateHTTPURL("url", config.URL)
		hosts[host] = struct{}{}
	} else {
		for _, step := range config.ScenarioSteps {
			sharedBytes += len(step.Kind) + len(step.URL) + len(step.Method) + len(step.Body) + len(step.ExpectedStatus)
			for _, capture := range step.Captures {
				sharedBytes += len(capture.Name) + len(capture.Path) + len(capture.Scope) + len(capture.OnStatus)
			}
			_, headerBytes, _ := normalizeHeaders("scenario headers", step.Headers)
			sharedBytes += headerBytes
			if engine.StepKind(step.Kind) != engine.StepRequest {
				continue
			}
			host, _ := validateHTTPURL("scenario url", step.URL)
			hosts[host] = struct{}{}
			if len(step.Body) > largestBody {
				largestBody = len(step.Body)
			}
		}
	}
	if len(hosts) > MaxScenarioHosts {
		return PreflightEstimate{}, fmt.Errorf("scenario must target at most %d hosts", MaxScenarioHosts)
	}

	perWorkerBytes := uint64(
		config.ReadBufferSize +
			config.WriteBufferSize +
			config.MaxResponseBytes +
			largestBody +
			WorkerMemoryOverhead,
	)
	memoryBytes := uint64(sharedBytes) + uint64(config.VirtualUsers)*perWorkerBytes
	if memoryBytes > MaxEstimatedMemoryBytes {
		return PreflightEstimate{}, fmt.Errorf(
			"estimated peak request memory %s exceeds the %s safety limit; reduce virtual users, response limit, or body size",
			formatResourceBytes(memoryBytes),
			formatResourceBytes(MaxEstimatedMemoryBytes),
		)
	}

	connectionCapacity := config.MaxConnsPerHost * len(hosts)
	connections := min(config.VirtualUsers, connectionCapacity)
	if connections > MaxEstimatedConnections {
		return PreflightEstimate{}, fmt.Errorf(
			"estimated concurrent connections %d exceed the %d safety limit; reduce virtual users or per-host connections",
			connections,
			MaxEstimatedConnections,
		)
	}
	return PreflightEstimate{
		MemoryBytes: memoryBytes,
		Connections: connections,
		TargetHosts: len(hosts),
	}, nil
}

func formatResourceBytes(value uint64) string {
	const (
		mebibyte = uint64(1 << 20)
		gibibyte = uint64(1 << 30)
	)
	if value >= gibibyte {
		return fmt.Sprintf("%.1f GiB", float64(value)/float64(gibibyte))
	}
	return fmt.Sprintf("%.1f MiB", float64(value)/float64(mebibyte))
}

func (c LoadConfig) Validate() error {
	_, err := normalizeLoadConfig(c)
	return err
}
