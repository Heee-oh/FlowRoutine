package distributed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"flowroutine/internal/engine"
)

const (
	ProtocolVersion   = 1
	PlanSchemaVersion = 1
	MaxControlBytes   = 4 << 20
)

const (
	statusPath   = "/v1/status"
	preparePath  = "/v1/prepare"
	startPath    = "/v1/start"
	snapshotPath = "/v1/snapshot"
	stopPath     = "/v1/stop"
)

type WorkerState string

const (
	WorkerIdle      WorkerState = "idle"
	WorkerPrepared  WorkerState = "prepared"
	WorkerScheduled WorkerState = "scheduled"
	WorkerRunning   WorkerState = "running"
	WorkerStopped   WorkerState = "stopped"
	WorkerFailed    WorkerState = "failed"
)

func (state WorkerState) terminal() bool {
	return state == WorkerStopped || state == WorkerFailed
}

func (state WorkerState) valid() bool {
	switch state {
	case WorkerIdle, WorkerPrepared, WorkerScheduled, WorkerRunning, WorkerStopped, WorkerFailed:
		return true
	default:
		return false
	}
}

type ExecutionPlan struct {
	SchemaVersion int        `json:"schemaVersion"`
	ID            string     `json:"id"`
	Revision      uint64     `json:"revision"`
	Config        PlanConfig `json:"config"`
}

type PlanConfig struct {
	URL               string         `json:"url"`
	Method            string         `json:"method,omitempty"`
	Headers           []Header       `json:"headers,omitempty"`
	Body              []byte         `json:"body,omitempty"`
	VirtualUsers      int            `json:"virtualUsers"`
	DurationNS        int64          `json:"durationNs"`
	RequestTimeoutNS  int64          `json:"requestTimeoutNs"`
	MaxConnsPerHost   int            `json:"maxConnsPerHost"`
	ReadBufferSize    int            `json:"readBufferSize"`
	WriteBufferSize   int            `json:"writeBufferSize"`
	MaxResponseBytes  int            `json:"maxResponseBytes"`
	LatencySampleRate int            `json:"latencySampleRate"`
	RateLimitRPS      int            `json:"rateLimitRps"`
	RampUpNS          int64          `json:"rampUpNs"`
	Profile           *LoadProfile   `json:"profile,omitempty"`
	ScenarioSteps     []ScenarioStep `json:"scenarioSteps,omitempty"`
}

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type LoadProfile struct {
	Mode            engine.LoadMode `json:"mode"`
	StartTarget     int             `json:"startTarget"`
	Stages          []LoadStage     `json:"stages"`
	PreAllocatedVUs int             `json:"preAllocatedVUs"`
	MaxVUs          int             `json:"maxVUs"`
	GracefulStopNS  int64           `json:"gracefulStopNs"`
}

type LoadStage struct {
	DurationNS int64 `json:"durationNs"`
	Target     int   `json:"target"`
}

type ScenarioStep struct {
	ID             string            `json:"id,omitempty"`
	Name           string            `json:"name,omitempty"`
	Kind           engine.StepKind   `json:"kind"`
	URL            string            `json:"url,omitempty"`
	Method         string            `json:"method,omitempty"`
	Headers        []Header          `json:"headers,omitempty"`
	Body           []byte            `json:"body,omitempty"`
	DelayNS        int64             `json:"delayNs,omitempty"`
	ExpectedStatus string            `json:"expectedStatus,omitempty"`
	Assertion      Assertion         `json:"assertion,omitempty"`
	Captures       []VariableCapture `json:"captures,omitempty"`
}

type Assertion struct {
	Type         engine.AssertionType        `json:"type,omitempty"`
	Operator     engine.AssertionOperator    `json:"operator,omitempty"`
	HeaderName   string                      `json:"headerName,omitempty"`
	JSONPath     string                      `json:"jsonPath,omitempty"`
	Expected     string                      `json:"expected,omitempty"`
	ValueType    engine.AssertionValueType   `json:"valueType,omitempty"`
	MaxLatencyNS int64                       `json:"maxLatencyNs,omitempty"`
	FailureMode  engine.AssertionFailureMode `json:"failureMode,omitempty"`
}

type VariableCapture struct {
	Name     string               `json:"name"`
	Path     string               `json:"path"`
	Scope    engine.VariableScope `json:"scope,omitempty"`
	OnStatus string               `json:"onStatus,omitempty"`
}

type PrepareRequest struct {
	ProtocolVersion int               `json:"protocolVersion"`
	RunID           string            `json:"runId"`
	Plan            ExecutionPlan     `json:"plan"`
	RuntimeBindings map[string]string `json:"runtimeBindings,omitempty"`
}

type PrepareResponse struct {
	ProtocolVersion int    `json:"protocolVersion"`
	WorkerID        string `json:"workerId"`
	RunID           string `json:"runId"`
	PlanDigest      string `json:"planDigest"`
}

type StartRequest struct {
	ProtocolVersion int    `json:"protocolVersion"`
	RunID           string `json:"runId"`
	StartAtUnixNano int64  `json:"startAtUnixNano"`
}

type StopRequest struct {
	ProtocolVersion int    `json:"protocolVersion"`
	RunID           string `json:"runId"`
}

type StatusResponse struct {
	ProtocolVersion int         `json:"protocolVersion"`
	WorkerID        string      `json:"workerId"`
	ServerUnixNano  int64       `json:"serverUnixNano"`
	RunID           string      `json:"runId,omitempty"`
	PlanID          string      `json:"planId,omitempty"`
	PlanRevision    uint64      `json:"planRevision,omitempty"`
	State           WorkerState `json:"state"`
	ScheduledAtNano int64       `json:"scheduledAtUnixNano,omitempty"`
	StartedAtNano   int64       `json:"startedAtUnixNano,omitempty"`
	StoppedAtNano   int64       `json:"stoppedAtUnixNano,omitempty"`
	Error           string      `json:"error,omitempty"`
}

type SnapshotResponse struct {
	Status       StatusResponse               `json:"status"`
	Snapshot     engine.Snapshot              `json:"snapshot"`
	RequestSteps []engine.RequestStepSnapshot `json:"requestSteps,omitempty"`
}

func NewExecutionPlan(id string, revision uint64, cfg engine.Config) ExecutionPlan {
	return ExecutionPlan{
		SchemaVersion: PlanSchemaVersion,
		ID:            id,
		Revision:      revision,
		Config:        NewPlanConfig(cfg),
	}
}

func NewPlanConfig(cfg engine.Config) PlanConfig {
	converted := PlanConfig{
		URL:               cfg.URL,
		Method:            cfg.Method,
		Body:              append([]byte(nil), cfg.Body...),
		VirtualUsers:      cfg.VirtualUsers,
		DurationNS:        int64(cfg.Duration),
		RequestTimeoutNS:  int64(cfg.RequestTimeout),
		MaxConnsPerHost:   cfg.MaxConnsPerHost,
		ReadBufferSize:    cfg.ReadBufferSize,
		WriteBufferSize:   cfg.WriteBufferSize,
		MaxResponseBytes:  cfg.MaxResponseBytes,
		LatencySampleRate: cfg.LatencySampleRate,
		RateLimitRPS:      cfg.RateLimitRPS,
		RampUpNS:          int64(cfg.RampUp),
	}
	converted.Headers = fromEngineHeaders(cfg.Headers)
	if cfg.Profile != nil {
		profile := &LoadProfile{
			Mode:            cfg.Profile.Mode,
			StartTarget:     cfg.Profile.StartTarget,
			PreAllocatedVUs: cfg.Profile.PreAllocatedVUs,
			MaxVUs:          cfg.Profile.MaxVUs,
			GracefulStopNS:  int64(cfg.Profile.GracefulStop),
			Stages:          make([]LoadStage, len(cfg.Profile.Stages)),
		}
		for index, stage := range cfg.Profile.Stages {
			profile.Stages[index] = LoadStage{DurationNS: int64(stage.Duration), Target: stage.Target}
		}
		converted.Profile = profile
	}
	converted.ScenarioSteps = make([]ScenarioStep, len(cfg.ScenarioSteps))
	for index, step := range cfg.ScenarioSteps {
		converted.ScenarioSteps[index] = ScenarioStep{
			ID:             step.ID,
			Name:           step.Name,
			Kind:           step.Kind,
			URL:            step.URL,
			Method:         step.Method,
			Headers:        fromEngineHeaders(step.Headers),
			Body:           append([]byte(nil), step.Body...),
			DelayNS:        int64(step.Delay),
			ExpectedStatus: step.ExpectedStatus,
			Assertion: Assertion{
				Type:         step.Assertion.Type,
				Operator:     step.Assertion.Operator,
				HeaderName:   step.Assertion.HeaderName,
				JSONPath:     step.Assertion.JSONPath,
				Expected:     step.Assertion.Expected,
				ValueType:    step.Assertion.ValueType,
				MaxLatencyNS: int64(step.Assertion.MaxLatency),
				FailureMode:  step.Assertion.FailureMode,
			},
		}
		if len(step.Captures) > 0 {
			converted.ScenarioSteps[index].Captures = make([]VariableCapture, len(step.Captures))
			for captureIndex, capture := range step.Captures {
				converted.ScenarioSteps[index].Captures[captureIndex] = VariableCapture{
					Name: capture.Name, Path: capture.Path, Scope: capture.Scope, OnStatus: capture.OnStatus,
				}
			}
		}
	}
	return converted
}

func (cfg PlanConfig) EngineConfig(runtimeBindings map[string]string) engine.Config {
	converted := engine.Config{
		URL:               cfg.URL,
		Method:            cfg.Method,
		Headers:           toEngineHeaders(cfg.Headers),
		Body:              append([]byte(nil), cfg.Body...),
		VirtualUsers:      cfg.VirtualUsers,
		Duration:          time.Duration(cfg.DurationNS),
		RequestTimeout:    time.Duration(cfg.RequestTimeoutNS),
		MaxConnsPerHost:   cfg.MaxConnsPerHost,
		ReadBufferSize:    cfg.ReadBufferSize,
		WriteBufferSize:   cfg.WriteBufferSize,
		MaxResponseBytes:  cfg.MaxResponseBytes,
		LatencySampleRate: cfg.LatencySampleRate,
		RateLimitRPS:      cfg.RateLimitRPS,
		RampUp:            time.Duration(cfg.RampUpNS),
		RuntimeVariables:  cloneStrings(runtimeBindings),
	}
	if cfg.Profile != nil {
		profile := &engine.LoadProfile{
			Mode:            cfg.Profile.Mode,
			StartTarget:     cfg.Profile.StartTarget,
			PreAllocatedVUs: cfg.Profile.PreAllocatedVUs,
			MaxVUs:          cfg.Profile.MaxVUs,
			GracefulStop:    time.Duration(cfg.Profile.GracefulStopNS),
			Stages:          make([]engine.LoadStage, len(cfg.Profile.Stages)),
		}
		for index, stage := range cfg.Profile.Stages {
			profile.Stages[index] = engine.LoadStage{Duration: time.Duration(stage.DurationNS), Target: stage.Target}
		}
		converted.Profile = profile
	}
	converted.ScenarioSteps = make([]engine.ScenarioStep, len(cfg.ScenarioSteps))
	for index, step := range cfg.ScenarioSteps {
		converted.ScenarioSteps[index] = engine.ScenarioStep{
			ID:             step.ID,
			Name:           step.Name,
			Kind:           step.Kind,
			URL:            step.URL,
			Method:         step.Method,
			Headers:        toEngineHeaders(step.Headers),
			Body:           append([]byte(nil), step.Body...),
			Delay:          time.Duration(step.DelayNS),
			ExpectedStatus: step.ExpectedStatus,
			Assertion: engine.Assertion{
				Type:        step.Assertion.Type,
				Operator:    step.Assertion.Operator,
				HeaderName:  step.Assertion.HeaderName,
				JSONPath:    step.Assertion.JSONPath,
				Expected:    step.Assertion.Expected,
				ValueType:   step.Assertion.ValueType,
				MaxLatency:  time.Duration(step.Assertion.MaxLatencyNS),
				FailureMode: step.Assertion.FailureMode,
			},
		}
		if len(step.Captures) > 0 {
			converted.ScenarioSteps[index].Captures = make([]engine.VariableCapture, len(step.Captures))
			for captureIndex, capture := range step.Captures {
				converted.ScenarioSteps[index].Captures[captureIndex] = engine.VariableCapture{
					Name: capture.Name, Path: capture.Path, Scope: capture.Scope, OnStatus: capture.OnStatus,
				}
			}
		}
	}
	return converted
}

func (plan ExecutionPlan) validate(runtimeBindings map[string]string) error {
	if plan.SchemaVersion != PlanSchemaVersion {
		return fmt.Errorf("unsupported plan schema version %d", plan.SchemaVersion)
	}
	if err := validateIdentifier("plan id", plan.ID); err != nil {
		return err
	}
	if plan.Revision == 0 {
		return errors.New("plan revision must be greater than zero")
	}
	if err := engine.ValidateConfig(plan.Config.EngineConfig(runtimeBindings)); err != nil {
		return fmt.Errorf("invalid plan config: %w", err)
	}
	return nil
}

func planDigest(plan ExecutionPlan) (string, error) {
	payload, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func validateIdentifier(field string, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s is required", field)
	}
	if value != trimmed {
		return fmt.Errorf("%s must not start or end with whitespace", field)
	}
	if len(value) > 128 {
		return fmt.Errorf("%s must be at most 128 bytes", field)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s contains a control character", field)
		}
	}
	return nil
}

func fromEngineHeaders(headers []engine.Header) []Header {
	if len(headers) == 0 {
		return nil
	}
	converted := make([]Header, len(headers))
	for index, header := range headers {
		converted[index] = Header{Name: header.Name, Value: header.Value}
	}
	return converted
}

func toEngineHeaders(headers []Header) []engine.Header {
	if len(headers) == 0 {
		return nil
	}
	converted := make([]engine.Header, len(headers))
	for index, header := range headers {
		converted[index] = engine.Header{Name: header.Name, Value: header.Value}
	}
	return converted
}

func cloneStrings(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
