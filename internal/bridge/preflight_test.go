package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPreflightNormalizesDefaultsAndEstimatesResources(t *testing.T) {
	request := validPreflightRequest()
	request.Config.MaxConnsPerHost = 0
	request.Config.ReadBufferSize = 0
	request.Config.WriteBufferSize = 0
	request.Config.MaxResponseBytes = 0
	request.Config.LatencySampleRate = 0
	request.BatchIntervalMS = 0

	preflight, err := preflightStartRequest(request)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if preflight.EffectiveConfig.MaxConnsPerHost != 10_000 {
		t.Fatalf("got max connections %d", preflight.EffectiveConfig.MaxConnsPerHost)
	}
	if preflight.EffectiveConfig.ReadBufferSize != DefaultReadBufferBytes {
		t.Fatalf("got read buffer %d", preflight.EffectiveConfig.ReadBufferSize)
	}
	if preflight.EffectiveConfig.WriteBufferSize != DefaultWriteBufferBytes {
		t.Fatalf("got write buffer %d", preflight.EffectiveConfig.WriteBufferSize)
	}
	if preflight.EffectiveConfig.MaxResponseBytes != DefaultResponseLimit {
		t.Fatalf("got response limit %d", preflight.EffectiveConfig.MaxResponseBytes)
	}
	if preflight.EffectiveConfig.LatencySampleRate != 1 {
		t.Fatalf("got latency sample rate %d", preflight.EffectiveConfig.LatencySampleRate)
	}
	if preflight.EffectiveBatchIntervalMS != int(DefaultInterval/time.Millisecond) {
		t.Fatalf("got batch interval %d", preflight.EffectiveBatchIntervalMS)
	}
	if preflight.Estimate.Connections != 1 || preflight.Estimate.TargetHosts != 1 {
		t.Fatalf("unexpected connection estimate: %+v", preflight.Estimate)
	}
	if preflight.Estimate.MemoryBytes == 0 {
		t.Fatal("expected a non-zero memory estimate")
	}
}

func TestPreflightAllowsSlowMetricBatches(t *testing.T) {
	request := validPreflightRequest()
	request.BatchIntervalMS = 1_000

	preflight, err := preflightStartRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.EffectiveBatchIntervalMS != 1_000 {
		t.Fatalf("got batch interval %d, want 1000", preflight.EffectiveBatchIntervalMS)
	}
}

func TestPreflightNormalizesStagedVUProfile(t *testing.T) {
	request := validPreflightRequest()
	request.Config.MaxConnsPerHost = 100
	request.Config.Profile = &LoadProfile{
		Mode:        string("ramping-vus"),
		StartTarget: 0,
		Stages: []LoadStage{
			{DurationMS: 1_000, Target: 10},
			{DurationMS: 2_000, Target: 50},
			{DurationMS: 1_000, Target: 0},
		},
		GracefulStopMS: 500,
	}

	preflight, err := preflightStartRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.EffectiveConfig.VirtualUsers != 50 || preflight.EffectiveConfig.DurationMS != 4_000 {
		t.Fatalf("unexpected effective profile: %+v", preflight.EffectiveConfig)
	}
	if preflight.EffectiveConfig.RampUpMS != 0 || preflight.EffectiveConfig.Profile == nil {
		t.Fatalf("profile was not normalized: %+v", preflight.EffectiveConfig)
	}
	if preflight.Estimate.Connections != 50 {
		t.Fatalf("connection estimate = %d, want 50", preflight.Estimate.Connections)
	}
}

func TestPreflightNormalizesArrivalProfileWorkerCapacity(t *testing.T) {
	request := validPreflightRequest()
	request.Config.MaxConnsPerHost = 100
	request.Config.RateLimitRPS = 0
	request.Config.Profile = &LoadProfile{
		Mode:            "constant-arrival-rate",
		StartTarget:     500,
		Stages:          []LoadStage{{DurationMS: 2_000, Target: 500}},
		PreAllocatedVUs: 4,
		MaxVUs:          20,
	}

	preflight, err := preflightStartRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.EffectiveConfig.VirtualUsers != 20 || preflight.Estimate.Connections != 20 {
		t.Fatalf("arrival capacity was not used for estimates: %+v", preflight)
	}
	if got := preflight.EffectiveConfig.Profile.GracefulStopMS; got != request.Config.RequestTimeoutMS {
		t.Fatalf("default graceful stop = %d, want %d", got, request.Config.RequestTimeoutMS)
	}
	engineConfig := preflight.normalizedConfig.toEngineConfig()
	if engineConfig.Profile == nil || engineConfig.Profile.MaxVUs != 20 || engineConfig.Profile.Stages[0].Duration != 2*time.Second {
		t.Fatalf("unexpected engine profile mapping: %+v", engineConfig.Profile)
	}
}

func TestPreflightRejectsInvalidLoadProfiles(t *testing.T) {
	tests := []struct {
		name    string
		profile LoadProfile
		rateCap int
		message string
	}{
		{
			name:    "missing stages",
			profile: LoadProfile{Mode: "ramping-vus", StartTarget: 1},
			message: "between 1 and",
		},
		{
			name: "stage target",
			profile: LoadProfile{
				Mode:        "ramping-vus",
				StartTarget: 1,
				Stages:      []LoadStage{{DurationMS: 1_000, Target: MaxVirtualUsers + 1}},
			},
			message: "target must be between",
		},
		{
			name: "arrival capacity",
			profile: LoadProfile{
				Mode:            "constant-arrival-rate",
				StartTarget:     10,
				Stages:          []LoadStage{{DurationMS: 1_000, Target: 10}},
				PreAllocatedVUs: 2,
				MaxVUs:          1,
			},
			message: "max virtual users",
		},
		{
			name: "double throttle",
			profile: LoadProfile{
				Mode:            "constant-arrival-rate",
				StartTarget:     10,
				Stages:          []LoadStage{{DurationMS: 1_000, Target: 10}},
				PreAllocatedVUs: 1,
				MaxVUs:          1,
			},
			rateCap: 10,
			message: "cannot be combined",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validPreflightRequest()
			request.Config.Profile = &test.profile
			request.Config.RateLimitRPS = test.rateCap
			_, err := preflightStartRequest(request)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v, want error containing %q", err, test.message)
			}
		})
	}
}

func TestPreflightReturnsActionablePressureWarnings(t *testing.T) {
	request := validPreflightRequest()
	request.Config.VirtualUsers = WarningConnections
	request.Config.MaxConnsPerHost = WarningConnections
	request.Config.ScenarioSteps = make([]ScenarioStep, WarningScenarioHosts)
	for index := range request.Config.ScenarioSteps {
		request.Config.ScenarioSteps[index] = ScenarioStep{
			Kind: "request",
			URL:  fmt.Sprintf("http://warning-%d.example.com", index),
		}
	}

	preflight, err := preflightStartRequest(request)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	codes := make(map[string]bool)
	for _, warning := range preflight.Warnings {
		codes[warning.Code] = true
		if warning.Message == "" {
			t.Fatalf("warning %q has no message", warning.Code)
		}
	}
	if !codes["high_memory"] || !codes["high_connections"] || !codes["many_hosts"] {
		t.Fatalf("missing pressure warnings: %+v", preflight.Warnings)
	}
}

func TestPreflightResponseDoesNotEchoRequestPayloads(t *testing.T) {
	request := validPreflightRequest()
	request.Config.Headers = []Header{{Name: "Authorization", Value: "Bearer preflight-secret"}}
	request.Config.Body = "preflight-sensitive-body"

	preflight, err := preflightStartRequest(request)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	payload, err := json.Marshal(preflight)
	if err != nil {
		t.Fatalf("marshal preflight: %v", err)
	}
	for _, sensitive := range []string{"preflight-secret", "preflight-sensitive-body"} {
		if strings.Contains(string(payload), sensitive) {
			t.Fatalf("preflight response echoed sensitive payload %q", sensitive)
		}
	}
}

func TestPreflightRejectsConfiguredBounds(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*StartRequest)
		message string
	}{
		{
			name:    "url required",
			mutate:  func(request *StartRequest) { request.Config.URL = "" },
			message: "url is required",
		},
		{
			name: "url size",
			mutate: func(request *StartRequest) {
				request.Config.URL = "http://example.com/" + strings.Repeat("a", MaxURLBytes)
			},
			message: "url must be at most",
		},
		{
			name:    "url scheme",
			mutate:  func(request *StartRequest) { request.Config.URL = "ftp://example.com" },
			message: "scheme must be http or https",
		},
		{
			name:    "method size",
			mutate:  func(request *StartRequest) { request.Config.Method = strings.Repeat("A", MaxMethodBytes+1) },
			message: "method must be at most",
		},
		{
			name:    "method token",
			mutate:  func(request *StartRequest) { request.Config.Method = "GET BAD" },
			message: "invalid HTTP token",
		},
		{
			name: "header count",
			mutate: func(request *StartRequest) {
				request.Config.Headers = make([]Header, MaxHeadersPerRequest+1)
			},
			message: "headers must have at most",
		},
		{
			name: "header name size",
			mutate: func(request *StartRequest) {
				request.Config.Headers = []Header{{Name: strings.Repeat("A", MaxHeaderNameBytes+1)}}
			},
			message: "name must be at most",
		},
		{
			name: "header value size",
			mutate: func(request *StartRequest) {
				request.Config.Headers = []Header{{Name: "X-Test", Value: strings.Repeat("a", MaxHeaderValueBytes+1)}}
			},
			message: "value must be at most",
		},
		{
			name: "header total size",
			mutate: func(request *StartRequest) {
				headers := make([]Header, MaxHeadersPerRequest)
				for index := range headers {
					headers[index] = Header{
						Name:  fmt.Sprintf("X-Test-%d", index),
						Value: strings.Repeat("a", MaxHeaderBytesPerRequest/MaxHeadersPerRequest),
					}
				}
				request.Config.Headers = headers
			},
			message: "must total at most",
		},
		{
			name: "header control character",
			mutate: func(request *StartRequest) {
				request.Config.Headers = []Header{{Name: "X-Test", Value: "safe\r\nInjected: yes"}}
			},
			message: "forbidden control character",
		},
		{
			name:    "body size",
			mutate:  func(request *StartRequest) { request.Config.Body = strings.Repeat("a", MaxRequestBodyBytes+1) },
			message: "body must be at most",
		},
		{
			name:    "virtual users minimum",
			mutate:  func(request *StartRequest) { request.Config.VirtualUsers = 0 },
			message: "virtual users must be between",
		},
		{
			name:    "virtual users maximum",
			mutate:  func(request *StartRequest) { request.Config.VirtualUsers = MaxVirtualUsers + 1 },
			message: "virtual users must be between",
		},
		{
			name:    "duration minimum",
			mutate:  func(request *StartRequest) { request.Config.DurationMS = 0 },
			message: "duration must be between",
		},
		{
			name: "duration maximum",
			mutate: func(request *StartRequest) {
				request.Config.DurationMS = MaxDuration.Milliseconds() + 1
			},
			message: "duration must be between",
		},
		{
			name:    "request timeout minimum",
			mutate:  func(request *StartRequest) { request.Config.RequestTimeoutMS = 0 },
			message: "request timeout must be between",
		},
		{
			name: "request timeout maximum",
			mutate: func(request *StartRequest) {
				request.Config.RequestTimeoutMS = MaxRequestDelay.Milliseconds() + 1
			},
			message: "request timeout must be between",
		},
		{
			name:    "ramp-up minimum",
			mutate:  func(request *StartRequest) { request.Config.RampUpMS = -1 },
			message: "ramp-up must be between",
		},
		{
			name:    "ramp-up maximum",
			mutate:  func(request *StartRequest) { request.Config.RampUpMS = MaxRampUp.Milliseconds() + 1 },
			message: "ramp-up must be between",
		},
		{
			name:    "connections minimum",
			mutate:  func(request *StartRequest) { request.Config.MaxConnsPerHost = -1 },
			message: "max connections per host must be between",
		},
		{
			name: "connections maximum",
			mutate: func(request *StartRequest) {
				request.Config.MaxConnsPerHost = MaxConnectionsPerHost + 1
			},
			message: "max connections per host must be between",
		},
		{
			name:    "read buffer minimum",
			mutate:  func(request *StartRequest) { request.Config.ReadBufferSize = MinIOBufferBytes - 1 },
			message: "read buffer size must be between",
		},
		{
			name:    "read buffer maximum",
			mutate:  func(request *StartRequest) { request.Config.ReadBufferSize = MaxIOBufferBytes + 1 },
			message: "read buffer size must be between",
		},
		{
			name:    "write buffer minimum",
			mutate:  func(request *StartRequest) { request.Config.WriteBufferSize = MinIOBufferBytes - 1 },
			message: "write buffer size must be between",
		},
		{
			name:    "write buffer maximum",
			mutate:  func(request *StartRequest) { request.Config.WriteBufferSize = MaxIOBufferBytes + 1 },
			message: "write buffer size must be between",
		},
		{
			name:    "response limit minimum",
			mutate:  func(request *StartRequest) { request.Config.MaxResponseBytes = MinResponseLimit - 1 },
			message: "max response bytes must be between",
		},
		{
			name:    "response limit maximum",
			mutate:  func(request *StartRequest) { request.Config.MaxResponseBytes = MaxResponseLimit + 1 },
			message: "max response bytes must be between",
		},
		{
			name:    "sample rate minimum",
			mutate:  func(request *StartRequest) { request.Config.LatencySampleRate = -1 },
			message: "latency sample rate must be between",
		},
		{
			name:    "sample rate maximum",
			mutate:  func(request *StartRequest) { request.Config.LatencySampleRate = MaxLatencySampleRate + 1 },
			message: "latency sample rate must be between",
		},
		{
			name:    "rate limit minimum",
			mutate:  func(request *StartRequest) { request.Config.RateLimitRPS = -1 },
			message: "rate limit rps must be between",
		},
		{
			name:    "rate limit maximum",
			mutate:  func(request *StartRequest) { request.Config.RateLimitRPS = MaxRateLimitRPS + 1 },
			message: "rate limit rps must be between",
		},
		{
			name:    "batch interval minimum",
			mutate:  func(request *StartRequest) { request.BatchIntervalMS = int(MinInterval/time.Millisecond) - 1 },
			message: "batch interval must be between",
		},
		{
			name:    "batch interval maximum",
			mutate:  func(request *StartRequest) { request.BatchIntervalMS = int(MaxInterval/time.Millisecond) + 1 },
			message: "batch interval must be between",
		},
		{
			name: "scenario step count",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = make([]ScenarioStep, MaxScenarioSteps+1)
			},
			message: "scenario must have at most",
		},
		{
			name: "scenario total size",
			mutate: func(request *StartRequest) {
				body := strings.Repeat("a", MaxRequestBodyBytes)
				steps := make([]ScenarioStep, 5)
				for index := range steps {
					steps[index] = ScenarioStep{Kind: "request", URL: request.Config.URL, Body: body}
				}
				request.Config.ScenarioSteps = steps
			},
			message: "scenario must total at most",
		},
		{
			name: "scenario kind",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{{Kind: "unknown"}}
			},
			message: "unsupported kind",
		},
		{
			name: "scenario requires request",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{{Kind: string("delay"), DelayMS: 1}}
			},
			message: "requires at least one request",
		},
		{
			name: "step url",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{{Kind: "request", URL: ""}}
			},
			message: "scenario step 1 url is required",
		},
		{
			name: "step method",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{{
					Kind:   "request",
					URL:    request.Config.URL,
					Method: strings.Repeat("A", MaxMethodBytes+1),
				}}
			},
			message: "scenario step 1 method must be at most",
		},
		{
			name: "step headers",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{{
					Kind:    "request",
					URL:     request.Config.URL,
					Headers: make([]Header, MaxHeadersPerRequest+1),
				}}
			},
			message: "scenario step 1 headers must have at most",
		},
		{
			name: "step body",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{{
					Kind: "request",
					URL:  request.Config.URL,
					Body: strings.Repeat("a", MaxRequestBodyBytes+1),
				}}
			},
			message: "scenario step 1 body must be at most",
		},
		{
			name: "step delay minimum",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{
					{Kind: "request", URL: request.Config.URL},
					{Kind: "delay", DelayMS: -1},
				}
			},
			message: "scenario step 2 delay must be between",
		},
		{
			name: "step delay maximum",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{
					{Kind: "request", URL: request.Config.URL},
					{Kind: "delay", DelayMS: MaxRequestDelay.Milliseconds() + 1},
				}
			},
			message: "scenario step 2 delay must be between",
		},
		{
			name: "assert status required",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{
					{Kind: "request", URL: request.Config.URL},
					{Kind: "assertStatus"},
				}
			},
			message: "expected status is required",
		},
		{
			name: "capture count",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{{
					Kind:     "request",
					URL:      request.Config.URL,
					Captures: make([]Capture, MaxCapturesPerStep+1),
				}}
			},
			message: "must have at most",
		},
		{
			name: "capture name required",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{{
					Kind:     "request",
					URL:      request.Config.URL,
					Captures: []Capture{{Path: "data.id"}},
				}}
			},
			message: "name and path are required",
		},
		{
			name: "capture name size",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{{
					Kind: "request",
					URL:  request.Config.URL,
					Captures: []Capture{{
						Name: strings.Repeat("a", MaxCaptureNameBytes+1),
						Path: "data.id",
					}},
				}}
			},
			message: "name must be at most",
		},
		{
			name: "capture path size",
			mutate: func(request *StartRequest) {
				request.Config.ScenarioSteps = []ScenarioStep{{
					Kind: "request",
					URL:  request.Config.URL,
					Captures: []Capture{{
						Name: "id",
						Path: strings.Repeat("a", MaxCapturePathBytes+1),
					}},
				}}
			},
			message: "path must be at most",
		},
		{
			name: "scenario hosts",
			mutate: func(request *StartRequest) {
				steps := make([]ScenarioStep, MaxScenarioHosts+1)
				for index := range steps {
					steps[index] = ScenarioStep{
						Kind: "request",
						URL:  fmt.Sprintf("http://host-%d.example.com", index),
					}
				}
				request.Config.ScenarioSteps = steps
			},
			message: "target at most",
		},
		{
			name: "estimated memory",
			mutate: func(request *StartRequest) {
				request.Config.VirtualUsers = 8_000
			},
			message: "estimated peak request memory",
		},
		{
			name: "estimated connections",
			mutate: func(request *StartRequest) {
				request.Config.VirtualUsers = MaxEstimatedConnections + 1
				request.Config.MaxConnsPerHost = MaxEstimatedConnections + 1
				request.Config.ReadBufferSize = MinIOBufferBytes
				request.Config.WriteBufferSize = MinIOBufferBytes
				request.Config.MaxResponseBytes = MinResponseLimit
			},
			message: "estimated concurrent connections",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validPreflightRequest()
			test.mutate(&request)
			_, err := preflightStartRequest(request)
			if err == nil {
				t.Fatal("expected preflight error")
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got error %q, want it to contain %q", err, test.message)
			}
		})
	}
}

func TestPreflightNormalizesCapturePolicyAndValidatesTemplates(t *testing.T) {
	request := validPreflightRequest()
	request.Config.ScenarioSteps = []ScenarioStep{
		{
			Kind: "request",
			URL:  request.Config.URL + "/login",
			Captures: []Capture{{
				Name: "token",
				Path: "data.items[0].token",
			}},
		},
		{
			Kind: "request",
			URL:  request.Config.URL + "/secure/{{token}}",
		},
	}

	preflight, err := preflightStartRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	capture := preflight.normalizedConfig.ScenarioSteps[0].Captures[0]
	if capture.Scope != "iteration" || capture.OnStatus != "success" {
		t.Fatalf("expected explicit capture defaults, got %+v", capture)
	}

	request.Config.ScenarioSteps = []ScenarioStep{{
		Kind: "request",
		URL:  request.Config.URL + "/secure/{{missing}}",
	}}
	_, err = preflightStartRequest(request)
	if err == nil || !strings.Contains(err.Error(), `template variable "missing" is not defined`) {
		t.Fatalf("expected unknown-template preflight error, got %v", err)
	}
}

func TestControllerStartReturnsEffectiveConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	request := validPreflightRequest()
	request.Config.URL = server.URL
	request.Config.ReadBufferSize = 0
	request.Config.WriteBufferSize = 0
	request.Config.MaxResponseBytes = 0
	request.BatchIntervalMS = 0

	controller := NewController(discardEmitter{})
	response, err := controller.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		if err := controller.Stop(); err != nil {
			t.Errorf("stop: %v", err)
		}
	}()

	if !response.Started {
		t.Fatal("expected started response")
	}
	if response.Preflight.EffectiveConfig.MaxResponseBytes != DefaultResponseLimit {
		t.Fatalf("got response limit %d", response.Preflight.EffectiveConfig.MaxResponseBytes)
	}
	if response.Preflight.EffectiveBatchIntervalMS != int(DefaultInterval/time.Millisecond) {
		t.Fatalf("got batch interval %d", response.Preflight.EffectiveBatchIntervalMS)
	}
}

func validPreflightRequest() StartRequest {
	return StartRequest{
		Config: LoadConfig{
			URL:               "http://127.0.0.1:8080",
			Method:            http.MethodGet,
			VirtualUsers:      1,
			DurationMS:        int64((5 * time.Second) / time.Millisecond),
			RequestTimeoutMS:  int64(time.Second / time.Millisecond),
			MaxConnsPerHost:   1,
			ReadBufferSize:    DefaultReadBufferBytes,
			WriteBufferSize:   DefaultWriteBufferBytes,
			MaxResponseBytes:  DefaultResponseLimit,
			LatencySampleRate: 1,
			RateLimitRPS:      10,
		},
		BatchIntervalMS: int(DefaultInterval / time.Millisecond),
	}
}
