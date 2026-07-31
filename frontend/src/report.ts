import type { Header, MetricsBatch, QualityGate, ScenarioStep, StartRequest, StatusCodeCount } from "./types";

export type RunReport = {
  schemaVersion: 2;
  generatedAtUnixMs: number;
  run: {
    targetUrl: string;
    method: string;
    startedAtUnixMs: number;
    finishedAtUnixMs: number;
    elapsedMs: number;
    batchCount: number;
  };
  config: ReportConfig;
  summary: ReportSummary;
  qualityGate: QualityGateResult;
  baseline: BaselineComparison;
  timeline: ReportTimelinePoint[];
};

type ReportConfig = {
  virtualUsers: number;
  durationMs: number;
  requestTimeoutMs: number;
  maxConnsPerHost: number;
  readBufferSize: number;
  writeBufferSize: number;
  maxResponseBytes: number;
  latencySampleRate: number;
  rateLimitRps: number;
  rampUpMs: number;
  batchIntervalMs: number;
  qualityGate: QualityGate;
  headers: Header[];
  bodyBytes: number;
  scenarioSteps: ReportScenarioStep[];
};

type ReportScenarioStep = {
  kind: ScenarioStep["kind"];
  url?: string;
  method?: string;
  headers?: Header[];
  bodyBytes?: number;
  captures?: Array<{ name: string; path: string }>;
  delayMs?: number;
  expectedStatus?: string;
};

type ReportSummary = {
  averageRps: number;
  totalRequests: number;
  successRequests: number;
  failedRequests: number;
  successRate: number;
  failureRate: number;
  latencySamples: number;
  latencyMs: {
    avg: number;
    min: number;
    max: number;
    p95: number;
    p99: number;
    p999: number;
  };
  failures: {
    timeout: number;
    dns: number;
    tls: number;
    connRefused: number;
    other: number;
    assertions: number;
  };
  bytes: {
    read: number;
    written: number;
  };
  statusCodes: StatusCodeCount[];
};

type ReportTimelinePoint = {
  timestampUnixMs: number;
  rps: number;
  total: number;
  failed: number;
  p95LatencyMs: number;
  p99LatencyMs: number;
};

export type QualityGateResult = {
  passed: boolean;
  checks: QualityGateCheck[];
};

type QualityGateCheck = {
  name: string;
  actual: number;
  threshold: number;
  operator: "<=" | ">=";
  passed: boolean;
};

export type BaselineComparison = {
  key: string;
  verdict: "new-baseline" | "improved" | "stable" | "mixed" | "regressed";
  previousRunUnixMs: number | null;
  deltas: {
    averageRpsPct: number;
    p95LatencyPct: number;
    p99LatencyPct: number;
    failureRatePctPoints: number;
  } | null;
};

type BaselineSnapshot = {
  schemaVersion: 2;
  key: string;
  savedAtUnixMs: number;
  targetUrl: string;
  method: string;
  averageRps: number;
  failureRate: number;
  p95LatencyMs: number;
  p99LatencyMs: number;
};

const baselineStorageKey = "flowroutine:run-baselines:v2";
const maxBaselines = 24;

export function buildRunReport(request: StartRequest, batches: MetricsBatch[]): RunReport {
  const finalBatch = batches[batches.length - 1] ?? emptyBatch();
  const runLatency = finalBatch.runLatency;
  const qualityGate = normalizeQualityGate(request.qualityGate);
  const startedAtUnixMs = validTimestamp(finalBatch.startedAtUnixMs)
    ? finalBatch.startedAtUnixMs
    : batches[0]?.timestampUnixMs ?? finalBatch.timestampUnixMs;
  const finishedAtUnixMs = finalBatch.timestampUnixMs;
  const elapsedMs = Math.max(0, finishedAtUnixMs - startedAtUnixMs);
  const total = finalBatch.total;
  const averageRps = elapsedMs > 0 ? total / (elapsedMs / 1_000) : 0;

  return {
    schemaVersion: 2,
    generatedAtUnixMs: Date.now(),
    run: {
      targetUrl: request.config.url,
      method: request.config.method,
      startedAtUnixMs,
      finishedAtUnixMs,
      elapsedMs,
      batchCount: batches.length,
    },
    config: {
      virtualUsers: request.config.virtualUsers,
      durationMs: request.config.durationMs,
      requestTimeoutMs: request.config.requestTimeoutMs,
      maxConnsPerHost: request.config.maxConnsPerHost,
      readBufferSize: request.config.readBufferSize,
      writeBufferSize: request.config.writeBufferSize,
      maxResponseBytes: request.config.maxResponseBytes,
      latencySampleRate: request.config.latencySampleRate,
      rateLimitRps: request.config.rateLimitRps,
      rampUpMs: request.config.rampUpMs,
      batchIntervalMs: request.batchIntervalMs,
      qualityGate,
      headers: redactHeaders(request.config.headers),
      bodyBytes: byteLength(request.config.body),
      scenarioSteps: request.config.scenarioSteps.map(redactScenarioStep),
    },
    summary: {
      averageRps,
      totalRequests: total,
      successRequests: finalBatch.success,
      failedRequests: finalBatch.failed,
      successRate: ratio(finalBatch.success, total),
      failureRate: ratio(finalBatch.failed, total),
      latencySamples: runLatency.samples,
      latencyMs: {
        avg: runLatency.avgMs,
        min: runLatency.minMs,
        max: runLatency.maxMs,
        p95: runLatency.p95Ms,
        p99: runLatency.p99Ms,
        p999: runLatency.p999Ms,
      },
      failures: {
        timeout: finalBatch.timeout,
        dns: finalBatch.dns,
        tls: finalBatch.tls,
        connRefused: finalBatch.connRefused,
        other: finalBatch.otherErrors,
        assertions: finalBatch.assertionsFailed,
      },
      bytes: {
        read: finalBatch.bytesRead,
        written: finalBatch.bytesWritten,
      },
      statusCodes: finalBatch.statusCodes,
    },
    qualityGate: evaluateQualityGate(qualityGate, finalBatch, averageRps),
    baseline: {
      key: baselineKey(request),
      verdict: "new-baseline",
      previousRunUnixMs: null,
      deltas: null,
    },
    timeline: batches.map((batch) => ({
      timestampUnixMs: batch.timestampUnixMs,
      rps: batch.rps,
      total: batch.total,
      failed: batch.failed,
      p95LatencyMs: batch.intervalLatency.p95Ms,
      p99LatencyMs: batch.intervalLatency.p99Ms,
    })),
  };
}

export function applyBaselineComparison(report: RunReport, request: StartRequest): RunReport {
  if (typeof window === "undefined") {
    return report;
  }
  try {
    const baselines = readBaselines();
    const key = baselineKey(request);
    const previous = baselines[key];
    const current = buildBaselineSnapshot(key, report);
    const baseline = previous ? compareBaseline(previous, current) : report.baseline;
    writeBaselines({ ...baselines, [key]: current });
    return { ...report, baseline };
  } catch {
    return report;
  }
}

function normalizeQualityGate(gate: QualityGate | undefined): QualityGate {
  return {
    maxFailureRatePct: nonNegative(gate?.maxFailureRatePct, 1),
    maxP95LatencyMs: nonNegative(gate?.maxP95LatencyMs, 500),
    maxP99LatencyMs: nonNegative(gate?.maxP99LatencyMs, 1000),
    minRps: nonNegative(gate?.minRps, 0),
  };
}

function evaluateQualityGate(gate: QualityGate, finalBatch: MetricsBatch, averageRps: number): QualityGateResult {
  const failureRatePct = finalBatch.total > 0 ? (finalBatch.failed / finalBatch.total) * 100 : 0;
  const checks = [
    upperBoundCheck("failure_rate_pct", failureRatePct, gate.maxFailureRatePct),
    upperBoundCheck("p95_latency_ms", finalBatch.runLatency.p95Ms, gate.maxP95LatencyMs),
    upperBoundCheck("p99_latency_ms", finalBatch.runLatency.p99Ms, gate.maxP99LatencyMs),
    lowerBoundCheck("average_rps", averageRps, gate.minRps),
  ].filter((check): check is QualityGateCheck => Boolean(check));
  return {
    passed: checks.every((check) => check.passed),
    checks,
  };
}

function upperBoundCheck(name: string, actual: number, threshold: number): QualityGateCheck | null {
  if (threshold <= 0) {
    return null;
  }
  return {
    name,
    actual,
    threshold,
    operator: "<=",
    passed: actual <= threshold,
  };
}

function lowerBoundCheck(name: string, actual: number, threshold: number): QualityGateCheck | null {
  if (threshold <= 0) {
    return null;
  }
  return {
    name,
    actual,
    threshold,
    operator: ">=",
    passed: actual >= threshold,
  };
}

function nonNegative(value: number | undefined, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) ? Math.max(0, value) : fallback;
}

function baselineKey(request: StartRequest) {
  const scenarioSignature = request.config.scenarioSteps
    .map((step) => [step.kind, step.method ?? "", step.url ?? "", step.delayMs ?? "", step.expectedStatus ?? ""].join(":"))
    .join("|");
  return stableHash([
    request.config.method,
    canonicalURL(request.config.url),
    scenarioSignature,
  ].join("|"));
}

function buildBaselineSnapshot(key: string, report: RunReport): BaselineSnapshot {
  return {
    schemaVersion: 2,
    key,
    savedAtUnixMs: report.generatedAtUnixMs,
    targetUrl: report.run.targetUrl,
    method: report.run.method,
    averageRps: report.summary.averageRps,
    failureRate: report.summary.failureRate,
    p95LatencyMs: report.summary.latencyMs.p95,
    p99LatencyMs: report.summary.latencyMs.p99,
  };
}

function compareBaseline(previous: BaselineSnapshot, current: BaselineSnapshot): BaselineComparison {
  const deltas = {
    averageRpsPct: percentDelta(current.averageRps, previous.averageRps),
    p95LatencyPct: percentDelta(current.p95LatencyMs, previous.p95LatencyMs),
    p99LatencyPct: percentDelta(current.p99LatencyMs, previous.p99LatencyMs),
    failureRatePctPoints: (current.failureRate - previous.failureRate) * 100,
  };
  return {
    key: current.key,
    verdict: baselineVerdict(deltas),
    previousRunUnixMs: previous.savedAtUnixMs,
    deltas,
  };
}

function baselineVerdict(deltas: BaselineComparison["deltas"]) {
  if (!deltas) {
    return "new-baseline";
  }
  const improvedSignals = [
    deltas.averageRpsPct >= 5,
    deltas.p95LatencyPct <= -5,
    deltas.p99LatencyPct <= -5,
    deltas.failureRatePctPoints <= -1,
  ].filter(Boolean).length;
  const regressedSignals = [
    deltas.averageRpsPct <= -5,
    deltas.p95LatencyPct >= 5,
    deltas.p99LatencyPct >= 5,
    deltas.failureRatePctPoints >= 1,
  ].filter(Boolean).length;
  if (regressedSignals > 0 && improvedSignals === 0) {
    return "regressed";
  }
  if (improvedSignals > 0 && regressedSignals === 0) {
    return "improved";
  }
  if (improvedSignals > 0 && regressedSignals > 0) {
    return "mixed";
  }
  return "stable";
}

function percentDelta(current: number, previous: number) {
  if (!Number.isFinite(current) || !Number.isFinite(previous) || previous === 0) {
    return 0;
  }
  return ((current - previous) / previous) * 100;
}

function readBaselines(): Record<string, BaselineSnapshot> {
  const raw = window.localStorage.getItem(baselineStorageKey);
  if (!raw) {
    return {};
  }
  const parsed: unknown = JSON.parse(raw);
  if (!parsed || typeof parsed !== "object") {
    return {};
  }
  return Object.fromEntries(
    Object.entries(parsed)
      .filter(([, value]) => isBaselineSnapshot(value))
      .map(([key, value]) => [key, value as BaselineSnapshot]),
  );
}

function writeBaselines(baselines: Record<string, BaselineSnapshot>) {
  const entries = Object.entries(baselines)
    .sort(([, a], [, b]) => b.savedAtUnixMs - a.savedAtUnixMs)
    .slice(0, maxBaselines);
  window.localStorage.setItem(baselineStorageKey, JSON.stringify(Object.fromEntries(entries)));
}

function isBaselineSnapshot(value: unknown): value is BaselineSnapshot {
  if (!value || typeof value !== "object") {
    return false;
  }
  const snapshot = value as BaselineSnapshot;
  return snapshot.schemaVersion === 2 &&
    typeof snapshot.key === "string" &&
    typeof snapshot.savedAtUnixMs === "number" &&
    typeof snapshot.averageRps === "number" &&
    typeof snapshot.failureRate === "number" &&
    typeof snapshot.p95LatencyMs === "number" &&
    typeof snapshot.p99LatencyMs === "number";
}

function canonicalURL(rawURL: string) {
  try {
    const url = new URL(rawURL);
    url.hash = "";
    return url.toString();
  } catch {
    return rawURL.trim();
  }
}

function stableHash(value: string) {
  let hash = 5381;
  for (let index = 0; index < value.length; index += 1) {
    hash = ((hash << 5) + hash) ^ value.charCodeAt(index);
  }
  return `b${(hash >>> 0).toString(36)}`;
}

export function downloadRunReport(report: RunReport) {
  const blob = new Blob([`${JSON.stringify(report, null, 2)}\n`], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = reportFilename(report);
  anchor.click();
  URL.revokeObjectURL(url);
}

function redactScenarioStep(step: ScenarioStep): ReportScenarioStep {
  if (step.kind === "request") {
    return {
      kind: step.kind,
      url: step.url,
      method: step.method,
      headers: redactHeaders(step.headers ?? []),
      bodyBytes: byteLength(step.body ?? ""),
      captures: step.captures ?? [],
    };
  }
  if (step.kind === "delay") {
    return {
      kind: step.kind,
      delayMs: step.delayMs,
    };
  }
  return {
    kind: step.kind,
    expectedStatus: step.expectedStatus,
  };
}

function redactHeaders(headers: Header[]) {
  return headers.map((header) => ({
    name: header.name,
    value: isSensitiveHeader(header.name) ? "[redacted]" : header.value,
  }));
}

function isSensitiveHeader(name: string) {
  return /authorization|cookie|token|secret|api[-_]?key/i.test(name);
}

function byteLength(value: string) {
  return new TextEncoder().encode(value).length;
}

function ratio(value: number, total: number) {
  return total > 0 ? value / total : 0;
}

function validTimestamp(value: number) {
  return Number.isFinite(value) && value > 0;
}

function emptyBatch(): MetricsBatch {
  const now = Date.now();
  return {
    timestampUnixMs: now,
    startedAtUnixMs: now,
    running: false,
    rps: 0,
    total: 0,
    success: 0,
    failed: 0,
    timeout: 0,
    dns: 0,
    tls: 0,
    connRefused: 0,
    otherErrors: 0,
    assertionsFailed: 0,
    intervalLatency: {
      samples: 0,
      avgMs: 0,
      p95Ms: 0,
      p99Ms: 0,
      p999Ms: 0,
    },
    runLatency: {
      samples: 0,
      avgMs: 0,
      minMs: 0,
      maxMs: 0,
      p95Ms: 0,
      p99Ms: 0,
      p999Ms: 0,
    },
    bytesRead: 0,
    bytesWritten: 0,
    statusCodes: [],
  };
}

function reportFilename(report: RunReport) {
  const host = safeHost(report.run.targetUrl);
  const timestamp = new Date(report.generatedAtUnixMs).toISOString().replace(/[:.]/g, "-");
  return `flowroutine-${host}-${timestamp}.json`;
}

function safeHost(rawURL: string) {
  try {
    return new URL(rawURL).host.replace(/[^a-z0-9.-]+/gi, "-").slice(0, 64) || "report";
  } catch {
    return "report";
  }
}
