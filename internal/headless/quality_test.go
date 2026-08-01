package headless

import (
	"testing"

	"flowroutine/internal/bridge"
)

func TestQualityGateMatchesDesktopSemantics(t *testing.T) {
	defaults := NormalizeQualityGate(nil)
	if defaults.MaxFailureRatePct != 1 || defaults.MaxP95LatencyMS != 500 || defaults.MaxP99LatencyMS != 1_000 || defaults.MinRPS != 0 {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}

	insufficient := EvaluateQualityGate(defaults, bridge.MetricsBatch{
		Total:      10,
		RunLatency: bridge.CumulativeLatencyMetrics{Samples: 10, P95Ms: 1, P99Ms: 1},
	}, 100)
	if insufficient.Status != QualityGateInsufficient || insufficient.Passed != nil {
		t.Fatalf("expected insufficient quality gate, got %+v", insufficient)
	}

	failed := EvaluateQualityGate(defaults, bridge.MetricsBatch{
		Total:  100,
		Failed: 2,
		RunLatency: bridge.CumulativeLatencyMetrics{
			Samples: 100,
			P95Ms:   600,
			P99Ms:   900,
		},
	}, 100)
	if failed.Status != QualityGateFail || failed.Passed == nil || *failed.Passed {
		t.Fatalf("expected failed quality gate, got %+v", failed)
	}

	zero := 0.0
	disabled := EvaluateQualityGate(NormalizeQualityGate(&QualityGateConfig{
		MaxFailureRatePct: &zero,
		MaxP95LatencyMS:   &zero,
		MaxP99LatencyMS:   &zero,
		MinRPS:            &zero,
	}), bridge.MetricsBatch{}, 0)
	if disabled.Status != QualityGatePass || len(disabled.Checks) != 0 || disabled.Passed == nil || !*disabled.Passed {
		t.Fatalf("expected disabled gates to pass, got %+v", disabled)
	}
}
