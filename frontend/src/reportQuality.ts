import type { MetricsBatch, QualityGate } from "./types";

export type QualityGateResult = {
  status: "pass" | "fail" | "insufficient";
  passed: boolean | null;
  checks: QualityGateCheck[];
};

type QualityGateCheck = {
  name: string;
  actual: number;
  threshold: number;
  operator: "<=" | ">=";
  status: "pass" | "fail" | "insufficient";
  passed: boolean | null;
  samples?: number;
  minimumSamples?: number;
};

export function normalizeQualityGate(gate: QualityGate | undefined): QualityGate {
  return {
    maxFailureRatePct: nonNegative(gate?.maxFailureRatePct, 1),
    maxP95LatencyMs: nonNegative(gate?.maxP95LatencyMs, 500),
    maxP99LatencyMs: nonNegative(gate?.maxP99LatencyMs, 1000),
    minRps: nonNegative(gate?.minRps, 0),
  };
}

export function evaluateQualityGate(gate: QualityGate, finalBatch: MetricsBatch, averageRps: number): QualityGateResult {
  const failureRatePct = finalBatch.total > 0 ? (finalBatch.failed / finalBatch.total) * 100 : 0;
  const checks = [
    upperBoundCheck("failure_rate_pct", failureRatePct, gate.maxFailureRatePct),
    latencyUpperBoundCheck("p95_latency_ms", finalBatch.runLatency.p95Ms, gate.maxP95LatencyMs, finalBatch.runLatency.samples, 20),
    latencyUpperBoundCheck("p99_latency_ms", finalBatch.runLatency.p99Ms, gate.maxP99LatencyMs, finalBatch.runLatency.samples, 100),
    lowerBoundCheck("average_rps", averageRps, gate.minRps),
  ].filter((check): check is QualityGateCheck => Boolean(check));
  const status = checks.some((check) => check.status === "fail")
    ? "fail"
    : checks.some((check) => check.status === "insufficient")
      ? "insufficient"
      : "pass";
  return {
    status,
    passed: status === "insufficient" ? null : status === "pass",
    checks,
  };
}

function latencyUpperBoundCheck(
  name: string,
  actual: number,
  threshold: number,
  samples: number,
  minimumSamples: number,
): QualityGateCheck | null {
  const check = upperBoundCheck(name, actual, threshold);
  if (!check || samples >= minimumSamples) {
    return check;
  }
  return {
    ...check,
    status: "insufficient",
    passed: null,
    samples,
    minimumSamples,
  };
}

function upperBoundCheck(name: string, actual: number, threshold: number): QualityGateCheck | null {
  if (threshold <= 0) {
    return null;
  }
  const passed = actual <= threshold;
  return {
    name,
    actual,
    threshold,
    operator: "<=",
    status: passed ? "pass" : "fail",
    passed,
  };
}

function lowerBoundCheck(name: string, actual: number, threshold: number): QualityGateCheck | null {
  if (threshold <= 0) {
    return null;
  }
  const passed = actual >= threshold;
  return {
    name,
    actual,
    threshold,
    operator: ">=",
    status: passed ? "pass" : "fail",
    passed,
  };
}

function nonNegative(value: number | undefined, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) ? Math.max(0, value) : fallback;
}
