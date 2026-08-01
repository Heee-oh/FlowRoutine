package headless

import (
	"math"

	"flowroutine/internal/bridge"
)

const (
	QualityGatePass         = "pass"
	QualityGateFail         = "fail"
	QualityGateInsufficient = "insufficient"
)

type QualityGateConfig struct {
	MaxFailureRatePct *float64 `json:"maxFailureRatePct,omitempty"`
	MaxP95LatencyMS   *float64 `json:"maxP95LatencyMs,omitempty"`
	MaxP99LatencyMS   *float64 `json:"maxP99LatencyMs,omitempty"`
	MinRPS            *float64 `json:"minRps,omitempty"`
}

type QualityGate struct {
	MaxFailureRatePct float64 `json:"maxFailureRatePct"`
	MaxP95LatencyMS   float64 `json:"maxP95LatencyMs"`
	MaxP99LatencyMS   float64 `json:"maxP99LatencyMs"`
	MinRPS            float64 `json:"minRps"`
}

type QualityGateResult struct {
	Status string             `json:"status"`
	Passed *bool              `json:"passed"`
	Checks []QualityGateCheck `json:"checks"`
}

type QualityGateCheck struct {
	Name           string  `json:"name"`
	Actual         float64 `json:"actual"`
	Threshold      float64 `json:"threshold"`
	Operator       string  `json:"operator"`
	Status         string  `json:"status"`
	Passed         *bool   `json:"passed"`
	Samples        uint64  `json:"samples,omitempty"`
	MinimumSamples uint64  `json:"minimumSamples,omitempty"`
}

func NormalizeQualityGate(config *QualityGateConfig) QualityGate {
	if config == nil {
		config = &QualityGateConfig{}
	}
	return QualityGate{
		MaxFailureRatePct: nonNegative(config.MaxFailureRatePct, 1),
		MaxP95LatencyMS:   nonNegative(config.MaxP95LatencyMS, 500),
		MaxP99LatencyMS:   nonNegative(config.MaxP99LatencyMS, 1_000),
		MinRPS:            nonNegative(config.MinRPS, 0),
	}
}

func EvaluateQualityGate(gate QualityGate, final bridge.MetricsBatch, averageRPS float64) QualityGateResult {
	failureRatePct := 0.0
	if final.Total > 0 {
		failureRatePct = float64(final.Failed) / float64(final.Total) * 100
	}
	checks := make([]QualityGateCheck, 0, 4)
	if check := upperBoundCheck("failure_rate_pct", failureRatePct, gate.MaxFailureRatePct); check != nil {
		checks = append(checks, *check)
	}
	if check := latencyUpperBoundCheck("p95_latency_ms", final.RunLatency.P95Ms, gate.MaxP95LatencyMS, final.RunLatency.Samples, 20); check != nil {
		checks = append(checks, *check)
	}
	if check := latencyUpperBoundCheck("p99_latency_ms", final.RunLatency.P99Ms, gate.MaxP99LatencyMS, final.RunLatency.Samples, 100); check != nil {
		checks = append(checks, *check)
	}
	if check := lowerBoundCheck("average_rps", averageRPS, gate.MinRPS); check != nil {
		checks = append(checks, *check)
	}

	status := QualityGatePass
	for _, check := range checks {
		if check.Status == QualityGateFail {
			status = QualityGateFail
			break
		}
		if check.Status == QualityGateInsufficient {
			status = QualityGateInsufficient
		}
	}
	return QualityGateResult{
		Status: status,
		Passed: statusPassed(status),
		Checks: checks,
	}
}

func latencyUpperBoundCheck(name string, actual float64, threshold float64, samples uint64, minimumSamples uint64) *QualityGateCheck {
	check := upperBoundCheck(name, actual, threshold)
	if check == nil || samples >= minimumSamples {
		return check
	}
	check.Status = QualityGateInsufficient
	check.Passed = nil
	check.Samples = samples
	check.MinimumSamples = minimumSamples
	return check
}

func upperBoundCheck(name string, actual float64, threshold float64) *QualityGateCheck {
	if threshold <= 0 {
		return nil
	}
	passed := actual <= threshold
	return &QualityGateCheck{
		Name:      name,
		Actual:    actual,
		Threshold: threshold,
		Operator:  "<=",
		Status:    passOrFail(passed),
		Passed:    boolPointer(passed),
	}
}

func lowerBoundCheck(name string, actual float64, threshold float64) *QualityGateCheck {
	if threshold <= 0 {
		return nil
	}
	passed := actual >= threshold
	return &QualityGateCheck{
		Name:      name,
		Actual:    actual,
		Threshold: threshold,
		Operator:  ">=",
		Status:    passOrFail(passed),
		Passed:    boolPointer(passed),
	}
}

func nonNegative(value *float64, fallback float64) float64 {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return fallback
	}
	return math.Max(0, *value)
}

func statusPassed(status string) *bool {
	if status == QualityGateInsufficient {
		return nil
	}
	return boolPointer(status == QualityGatePass)
}

func passOrFail(passed bool) string {
	if passed {
		return QualityGatePass
	}
	return QualityGateFail
}

func boolPointer(value bool) *bool {
	return &value
}
