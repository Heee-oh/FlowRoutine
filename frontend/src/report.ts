import type { AssertionDefinition, AssertionFailureCounts, BranchRouteMetrics, ExecutionPlan, Header, LoadProfile, MetricsBatch, QualityGate, RequestStepMetrics, ScenarioStep, StartRequest, StatusCodeCount } from "./types";
import type { MetricHistoryPoint } from "./metricHistory";
import { isSensitiveHeaderName, isSensitiveHeaderValue, redactHeaders, redactSensitiveURL } from "./secretSanitization";
import type { BaselineComparison } from "./reportBaseline";
import { evaluateQualityGate, normalizeQualityGate } from "./reportQuality";
import type { QualityGateResult } from "./reportQuality";

export { applyBaselineComparison, purgeLegacyRunBaselines } from "./reportBaseline";
export type { BaselineComparison } from "./reportBaseline";
export type { QualityGateResult } from "./reportQuality";

export type RunReport = {
  schemaVersion: 9;
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

export type RunReportMetrics = {
  finalBatch: MetricsBatch | null;
  timeline: readonly MetricHistoryPoint[];
  batchCount: number;
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
  profile?: LoadProfile;
  batchIntervalMs: number;
  qualityGate: QualityGate;
  headers: Header[];
  bodyBytes: number;
  scenarioSteps: ReportScenarioStep[];
  executionPlan?: ExecutionPlan;
};

type ReportScenarioStep = {
  id?: string;
  name?: string;
  kind: ScenarioStep["kind"];
  url?: string;
  method?: string;
  headers?: Header[];
  bodyBytes?: number;
  captures?: Array<{ name: string; path: string; scope: string; onStatus: string }>;
  delayMs?: number;
  expectedStatus?: string;
  assertion?: AssertionDefinition;
};

type ReportSummary = {
  averageRps: number;
  totalRequests: number;
  successRequests: number;
  failedRequests: number;
  successRate: number;
  failureRate: number;
  droppedIterations: number;
  latencySamples: number;
  effectiveLatencySampleRate: number;
  latencyPercentileErrorBoundPct: number;
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
    assertionTypes: AssertionFailureCounts;
    captures: number;
    templates: number;
  };
  bytes: {
    read: number;
    written: number;
  };
  statusCodes: StatusCodeCount[];
  requestSteps: RequestStepMetrics[];
  branchRoutes: BranchRouteMetrics[];
};

type ReportTimelinePoint = {
  timestampUnixMs: number;
  rps: number;
  total: number;
  failed: number;
  p95LatencyMs: number;
  p99LatencyMs: number;
};

export function buildRunReport(request: StartRequest, metrics: RunReportMetrics): RunReport {
  const finalBatch = metrics.finalBatch ?? emptyBatch();
  const runtimeSecretValues = request.runtimeSecretValues ?? [];
  const runLatency = finalBatch.runLatency;
  const qualityGate = normalizeQualityGate(request.qualityGate);
  const startedAtUnixMs = validTimestamp(finalBatch.startedAtUnixMs)
    ? finalBatch.startedAtUnixMs
    : metrics.timeline[0]?.timestampUnixMs ?? finalBatch.timestampUnixMs;
  const finishedAtUnixMs = finalBatch.timestampUnixMs;
  const elapsedMs = Math.max(0, finishedAtUnixMs - startedAtUnixMs);
  const total = finalBatch.total;
  const averageRps = elapsedMs > 0 ? total / (elapsedMs / 1_000) : 0;

  return {
    schemaVersion: 9,
    generatedAtUnixMs: Date.now(),
    run: {
      targetUrl: redactReportText(request.config.url, runtimeSecretValues),
      method: request.config.method,
      startedAtUnixMs,
      finishedAtUnixMs,
      elapsedMs,
      batchCount: metrics.batchCount,
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
      profile: request.config.profile ? {
        ...request.config.profile,
        stages: request.config.profile.stages.map((stage) => ({ ...stage })),
      } : undefined,
      batchIntervalMs: request.batchIntervalMs,
      qualityGate,
      headers: redactReportHeaders(request.config.headers, runtimeSecretValues),
      bodyBytes: byteLength(request.config.body),
      scenarioSteps: request.config.scenarioSteps.map((step) => redactScenarioStep(step, runtimeSecretValues)),
      executionPlan: request.config.executionPlan ? cloneExecutionPlan(request.config.executionPlan) : undefined,
    },
    summary: {
      averageRps,
      totalRequests: total,
      successRequests: finalBatch.success,
      failedRequests: finalBatch.failed,
      successRate: ratio(finalBatch.success, total),
      failureRate: ratio(finalBatch.failed, total),
      droppedIterations: finalBatch.droppedIterations ?? 0,
      latencySamples: runLatency.samples,
      effectiveLatencySampleRate: ratio(runLatency.samples, total),
      latencyPercentileErrorBoundPct: finalBatch.latencyPercentileErrorBoundPct,
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
        assertionTypes: normalizedAssertionFailures(finalBatch.assertionFailuresByType),
        captures: finalBatch.captureFailures,
        templates: finalBatch.templateFailures,
      },
      bytes: {
        read: finalBatch.bytesRead,
        written: finalBatch.bytesWritten,
      },
      statusCodes: finalBatch.statusCodes.map((status) => ({
        code: status.code,
        count: status.count,
      })),
      requestSteps: (finalBatch.stepMetrics ?? []).map((step) => redactRequestStepMetrics(step, runtimeSecretValues)),
      branchRoutes: (finalBatch.branchMetrics ?? []).map((route) => ({ ...route })),
    },
    qualityGate: evaluateQualityGate(qualityGate, finalBatch, averageRps),
    baseline: {
      key: baselineKey(request),
      verdict: "new-baseline",
      previousRunUnixMs: null,
      deltas: null,
    },
    timeline: metrics.timeline.map((point) => ({ ...point })),
  };
}

function baselineKey(request: StartRequest) {
  const runtimeSecretValues = request.runtimeSecretValues ?? [];
  const scenarioSignature = request.config.scenarioSteps
    .map((step) => [
      step.kind,
      step.method ?? "",
      canonicalURL(redactRuntimeSecrets(step.url ?? "", runtimeSecretValues)),
      step.delayMs ?? "",
      step.expectedStatus ?? "",
      JSON.stringify(step.assertion ? redactAssertion(step.assertion, runtimeSecretValues) : null),
    ].join(":"))
    .join("|");
  const executionSignature = JSON.stringify(request.config.executionPlan ?? null);
  return stableHash([
    request.config.method,
    canonicalURL(redactRuntimeSecrets(request.config.url, runtimeSecretValues)),
    scenarioSignature,
    executionSignature,
  ].join("|"));
}

function cloneExecutionPlan(plan: ExecutionPlan): ExecutionPlan {
  return {
    schemaVersion: 1,
    entryStepId: plan.entryStepId,
    steps: plan.steps.map((step) => ({
      ...step,
      routes: step.routes?.map((route) => ({ ...route })),
    })),
  };
}

function canonicalURL(rawURL: string) {
  const sanitizedURL = redactSensitiveURL(rawURL);
  try {
    const url = new URL(sanitizedURL);
    url.hash = "";
    return url.toString();
  } catch {
    return sanitizedURL.trim();
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

function redactScenarioStep(step: ScenarioStep, runtimeSecretValues: readonly string[]): ReportScenarioStep {
  if (step.kind === "request") {
    return {
      id: step.id,
      name: step.name ? redactReportText(step.name, runtimeSecretValues) : undefined,
      kind: step.kind,
      url: redactReportText(step.url ?? "", runtimeSecretValues),
      method: step.method,
      headers: redactReportHeaders(step.headers ?? [], runtimeSecretValues),
      bodyBytes: byteLength(step.body ?? ""),
      captures: (step.captures ?? []).map((capture) => ({
        name: capture.name,
        path: capture.path,
        scope: capture.scope ?? "iteration",
        onStatus: capture.onStatus ?? "success",
      })),
    };
  }
  if (step.kind === "delay") {
    return {
      id: step.id,
      name: step.name ? redactRuntimeSecrets(step.name, runtimeSecretValues) : undefined,
      kind: step.kind,
      delayMs: step.delayMs,
    };
  }
  return {
    id: step.id,
    name: step.name ? redactRuntimeSecrets(step.name, runtimeSecretValues) : undefined,
    kind: step.kind,
    expectedStatus: step.expectedStatus,
    assertion: step.assertion ? redactAssertion(step.assertion, runtimeSecretValues) : undefined,
  };
}

function redactAssertion(
  assertion: AssertionDefinition,
  runtimeSecretValues: readonly string[],
): AssertionDefinition {
  const sensitiveHeader = assertion.type === "header" && (
    isSensitiveHeaderName(assertion.headerName ?? "") || isSensitiveHeaderValue(assertion.expected ?? "")
  );
  const sensitiveJSON = assertion.type === "json" &&
    (assertion.valueType ?? "string") === "string" &&
    (isSensitiveHeaderName(assertion.jsonPath ?? "") || isSensitiveHeaderValue(assertion.expected ?? ""));
  if (sensitiveHeader || sensitiveJSON) {
    return { ...assertion, expected: assertion.expected ? "<redacted>" : assertion.expected };
  }
  return {
    ...assertion,
    expected: assertion.expected === undefined
      ? undefined
      : redactRuntimeSecrets(assertion.expected, runtimeSecretValues),
  };
}

function normalizedAssertionFailures(counts: AssertionFailureCounts | undefined): AssertionFailureCounts {
  return {
    status: counts?.status ?? 0,
    header: counts?.header ?? 0,
    json: counts?.json ?? 0,
    responseLatency: counts?.responseLatency ?? 0,
    stepLatency: counts?.stepLatency ?? 0,
    countOnly: counts?.countOnly ?? 0,
  };
}

function redactRequestStepMetrics(
  step: RequestStepMetrics,
  runtimeSecretValues: readonly string[],
): RequestStepMetrics {
  return {
    ...step,
    name: redactReportText(step.name, runtimeSecretValues),
    runLatency: { ...step.runLatency },
    statusCodes: step.statusCodes.map((status) => ({ ...status })),
    ...(step.assertionFailuresByType
      ? { assertionFailuresByType: normalizedAssertionFailures(step.assertionFailuresByType) }
      : {}),
  };
}

function redactReportHeaders(headers: Header[], runtimeSecretValues: readonly string[]) {
  return redactHeaders(headers).map((header) => ({
    name: redactRuntimeSecrets(header.name, runtimeSecretValues),
    value: redactRuntimeSecrets(header.value, runtimeSecretValues),
  }));
}

function redactReportText(value: string, runtimeSecretValues: readonly string[]) {
  return redactRuntimeSecrets(redactSensitiveURL(value), runtimeSecretValues);
}

function redactRuntimeSecrets(value: string, runtimeSecretValues: readonly string[]) {
  let redacted = value;
  for (const secret of runtimeSecretValues) {
    if (secret) {
      redacted = redacted.split(secret).join("<redacted>");
    }
  }
  return redacted;
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
    captureFailures: 0,
    templateFailures: 0,
    droppedIterations: 0,
    latencyPercentileErrorBoundPct: 2,
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
    stepMetrics: [],
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
