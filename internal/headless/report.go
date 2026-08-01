package headless

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strconv"
	"time"

	"flowroutine/internal/bridge"
)

const ReportSchemaVersion = 1

type RunReport struct {
	SchemaVersion     int                      `json:"schemaVersion"`
	GeneratedAtUnixMS int64                    `json:"generatedAtUnixMs"`
	Scenario          ReportScenario           `json:"scenario"`
	Run               ReportRun                `json:"run"`
	Preflight         bridge.PreflightResponse `json:"preflight"`
	Summary           ReportSummary            `json:"summary"`
	QualityGate       QualityGateResult        `json:"qualityGate"`
}

type ReportScenario struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int    `json:"revision"`
}

type ReportRun struct {
	StartedAtUnixMS  int64 `json:"startedAtUnixMs"`
	FinishedAtUnixMS int64 `json:"finishedAtUnixMs"`
	ElapsedMS        int64 `json:"elapsedMs"`
	BatchCount       int   `json:"batchCount"`
}

type ReportSummary struct {
	AverageRPS                     float64                         `json:"averageRps"`
	TotalRequests                  uint64                          `json:"totalRequests"`
	SuccessRequests                uint64                          `json:"successRequests"`
	FailedRequests                 uint64                          `json:"failedRequests"`
	SuccessRate                    float64                         `json:"successRate"`
	FailureRate                    float64                         `json:"failureRate"`
	DroppedIterations              uint64                          `json:"droppedIterations"`
	LatencySamples                 uint64                          `json:"latencySamples"`
	EffectiveLatencySampleRate     float64                         `json:"effectiveLatencySampleRate"`
	LatencyPercentileErrorBoundPct float64                         `json:"latencyPercentileErrorBoundPct"`
	LatencyMS                      bridge.CumulativeLatencyMetrics `json:"latencyMs"`
	Failures                       ReportFailures                  `json:"failures"`
	Bytes                          ReportBytes                     `json:"bytes"`
	StatusCodes                    []bridge.StatusCodeCount        `json:"statusCodes"`
	RequestSteps                   []bridge.RequestStepMetrics     `json:"requestSteps"`
}

type ReportFailures struct {
	Timeout     uint64                        `json:"timeout"`
	DNS         uint64                        `json:"dns"`
	TLS         uint64                        `json:"tls"`
	ConnRefused uint64                        `json:"connRefused"`
	Other       uint64                        `json:"other"`
	Assertions  uint64                        `json:"assertions"`
	Types       bridge.AssertionFailureCounts `json:"assertionTypes"`
	Captures    uint64                        `json:"captures"`
	Templates   uint64                        `json:"templates"`
}

type ReportBytes struct {
	Read    uint64 `json:"read"`
	Written uint64 `json:"written"`
}

func BuildRunReport(file *ScenarioFile, preflight bridge.PreflightResponse, final bridge.MetricsBatch, batchCount int, generatedAt time.Time) *RunReport {
	startedAt := final.StartedAtUnixMs
	finishedAt := final.TimestampUnixMs
	if startedAt <= 0 || finishedAt < startedAt {
		startedAt = finishedAt
	}
	elapsedMS := finishedAt - startedAt
	averageRPS := 0.0
	if elapsedMS > 0 {
		averageRPS = float64(final.Total) / (float64(elapsedMS) / 1_000)
	}
	gate := NormalizeQualityGate(file.Scenario.QualityGate)
	return &RunReport{
		SchemaVersion:     ReportSchemaVersion,
		GeneratedAtUnixMS: generatedAt.UnixMilli(),
		Scenario: ReportScenario{
			ID:       file.Scenario.ID,
			Name:     file.Scenario.Name,
			Revision: file.Scenario.Revision,
		},
		Run: ReportRun{
			StartedAtUnixMS:  startedAt,
			FinishedAtUnixMS: finishedAt,
			ElapsedMS:        elapsedMS,
			BatchCount:       batchCount,
		},
		Preflight: preflight,
		Summary: ReportSummary{
			AverageRPS:                     averageRPS,
			TotalRequests:                  final.Total,
			SuccessRequests:                final.Success,
			FailedRequests:                 final.Failed,
			SuccessRate:                    ratio(final.Success, final.Total),
			FailureRate:                    ratio(final.Failed, final.Total),
			DroppedIterations:              final.DroppedIterations,
			LatencySamples:                 final.RunLatency.Samples,
			EffectiveLatencySampleRate:     ratio(final.RunLatency.Samples, final.Total),
			LatencyPercentileErrorBoundPct: final.LatencyPercentileErrorBoundPct,
			LatencyMS:                      final.RunLatency,
			Failures: ReportFailures{
				Timeout:     final.Timeout,
				DNS:         final.DNS,
				TLS:         final.TLS,
				ConnRefused: final.ConnRefused,
				Other:       final.OtherErrors,
				Assertions:  final.AssertionsFailed,
				Types:       final.AssertionFailuresByType,
				Captures:    final.CaptureFailures,
				Templates:   final.TemplateFailures,
			},
			Bytes: ReportBytes{
				Read:    final.BytesRead,
				Written: final.BytesWritten,
			},
			StatusCodes:  cloneStatusCodes(final.StatusCodes),
			RequestSteps: cloneRequestSteps(final.StepMetrics),
		},
		QualityGate: EvaluateQualityGate(gate, final, averageRPS),
	}
}

func MarshalJSONReport(report *RunReport) ([]byte, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode JSON report: %w", err)
	}
	return append(data, '\n'), nil
}

func MarshalJUnitReport(report *RunReport) ([]byte, error) {
	suite := junitTestSuite{
		Name:      "FlowRoutine " + report.Scenario.Name,
		Timestamp: time.UnixMilli(report.GeneratedAtUnixMS).UTC().Format(time.RFC3339),
		Time:      secondsString(report.Run.ElapsedMS),
	}
	suite.Cases = append(suite.Cases, junitTestCase{
		Name:      "scenario_execution",
		ClassName: "flowroutine." + report.Scenario.ID,
		Time:      secondsString(report.Run.ElapsedMS),
	})
	for _, check := range report.QualityGate.Checks {
		caseResult := junitTestCase{
			Name:      check.Name,
			ClassName: "flowroutine." + report.Scenario.ID + ".quality_gate",
		}
		message := fmt.Sprintf("actual %g %s threshold %g", check.Actual, check.Operator, check.Threshold)
		switch check.Status {
		case QualityGateFail:
			caseResult.Failure = &junitFailure{Message: message, Body: message}
			suite.Failures++
		case QualityGateInsufficient:
			caseResult.Skipped = &junitSkipped{Message: fmt.Sprintf("%s; samples %d, minimum %d", message, check.Samples, check.MinimumSamples)}
			suite.Skipped++
		}
		suite.Cases = append(suite.Cases, caseResult)
	}
	suite.Tests = len(suite.Cases)
	return marshalJUnit(junitTestSuites{
		Tests:    suite.Tests,
		Failures: suite.Failures,
		Skipped:  suite.Skipped,
		Time:     suite.Time,
		Suites:   []junitTestSuite{suite},
	})
}

func MarshalJUnitError(scenarioName string, scenarioID string, kind string, runError error) ([]byte, error) {
	if scenarioName == "" {
		scenarioName = "scenario"
	}
	if scenarioID == "" {
		scenarioID = "unknown"
	}
	message := kind
	if runError != nil {
		message += ": " + runError.Error()
	}
	suite := junitTestSuite{
		Name:     "FlowRoutine " + scenarioName,
		Tests:    1,
		Failures: 1,
		Cases: []junitTestCase{{
			Name:      kind,
			ClassName: "flowroutine." + scenarioID,
			Failure:   &junitFailure{Message: message, Body: message},
		}},
	}
	return marshalJUnit(junitTestSuites{Tests: 1, Failures: 1, Suites: []junitTestSuite{suite}})
}

func ratio(numerator uint64, denominator uint64) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func cloneStatusCodes(values []bridge.StatusCodeCount) []bridge.StatusCodeCount {
	if len(values) == 0 {
		return []bridge.StatusCodeCount{}
	}
	return append([]bridge.StatusCodeCount(nil), values...)
}

func cloneRequestSteps(values []bridge.RequestStepMetrics) []bridge.RequestStepMetrics {
	if len(values) == 0 {
		return []bridge.RequestStepMetrics{}
	}
	cloned := append([]bridge.RequestStepMetrics(nil), values...)
	for index := range cloned {
		cloned[index].StatusCodes = cloneStatusCodes(cloned[index].StatusCodes)
	}
	return cloned
}

func secondsString(milliseconds int64) string {
	return strconv.FormatFloat(float64(milliseconds)/1_000, 'f', 3, 64)
}

type junitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Skipped  int              `xml:"skipped,attr"`
	Time     string           `xml:"time,attr,omitempty"`
	Suites   []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Skipped   int             `xml:"skipped,attr"`
	Time      string          `xml:"time,attr,omitempty"`
	Timestamp string          `xml:"timestamp,attr,omitempty"`
	Cases     []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr,omitempty"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}

func marshalJUnit(suites junitTestSuites) ([]byte, error) {
	data, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode JUnit report: %w", err)
	}
	output := append([]byte(xml.Header), data...)
	return append(output, '\n'), nil
}
