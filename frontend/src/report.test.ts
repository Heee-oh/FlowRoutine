import { afterEach, describe, expect, it, vi } from "vitest";

import { metricHistoryPoint } from "./metricHistory";
import { applyBaselineComparison, buildRunReport, purgeLegacyRunBaselines } from "./report";
import type { MetricsBatch, StartRequest } from "./types";

describe("buildRunReport", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("uses cumulative run latency for summaries and quality gates", () => {
    const request: StartRequest = {
      config: {
        url: "https://example.com",
        method: "GET",
        headers: [],
        body: "",
        virtualUsers: 1,
        durationMs: 1_000,
        requestTimeoutMs: 1_000,
        maxConnsPerHost: 10,
        readBufferSize: 4_096,
        writeBufferSize: 4_096,
        maxResponseBytes: 1_048_576,
        latencySampleRate: 1,
        rateLimitRps: 0,
        rampUpMs: 0,
        scenarioSteps: [],
      },
      batchIntervalMs: 1_000,
      qualityGate: {
        maxFailureRatePct: 100,
        maxP95LatencyMs: 500,
        maxP99LatencyMs: 2_000,
        minRps: 0,
      },
    };
    const finalBatch: MetricsBatch = {
      timestampUnixMs: 2_000,
      startedAtUnixMs: 1_000,
      running: false,
      rps: 10,
      total: 100,
      success: 100,
      failed: 0,
      timeout: 0,
      dns: 0,
      tls: 0,
      connRefused: 0,
      otherErrors: 0,
      assertionsFailed: 0,
      captureFailures: 0,
      templateFailures: 0,
      latencyPercentileErrorBoundPct: 2,
      intervalLatency: {
        samples: 10,
        avgMs: 1,
        p95Ms: 1,
        p99Ms: 1,
        p999Ms: 1,
      },
      runLatency: {
        samples: 100,
        avgMs: 900.1,
        minMs: 1,
        maxMs: 1_000,
        p95Ms: 1_000,
        p99Ms: 1_000,
        p999Ms: 1_000,
      },
      bytesRead: 1_000,
      bytesWritten: 100,
      statusCodes: [{ code: 200, count: 100 }],
    };

    const report = buildRunReport(request, {
      finalBatch,
      timeline: [metricHistoryPoint({ ...finalBatch, total: 1 })],
      batchCount: 36_000,
    });

    expect(report.run.batchCount).toBe(36_000);
    expect(report.summary.totalRequests).toBe(100);
    expect(report.summary.latencySamples).toBe(100);
    expect(report.summary.effectiveLatencySampleRate).toBe(1);
    expect(report.summary.latencyPercentileErrorBoundPct).toBe(2);
    expect(report.summary.latencyMs).toEqual({
      avg: 900.1,
      min: 1,
      max: 1_000,
      p95: 1_000,
      p99: 1_000,
      p999: 1_000,
    });
    expect(report.timeline[0]).toMatchObject({
      p95LatencyMs: 1,
      p99LatencyMs: 1,
    });
    expect(report.qualityGate.passed).toBe(false);
    expect(report.qualityGate.status).toBe("fail");
  });

  it("withholds percentile gates when the sampled rank cannot resolve the tail", () => {
    const report = buildRunReport(createRequest(), reportMetrics([completedBatch(100, 19)]));

    expect(report.summary.totalRequests).toBe(100);
    expect(report.summary.latencySamples).toBe(19);
    expect(report.summary.effectiveLatencySampleRate).toBe(0.19);
    expect(report.qualityGate.status).toBe("insufficient");
    expect(report.qualityGate.passed).toBeNull();
    expect(report.qualityGate.checks).toEqual(expect.arrayContaining([
      expect.objectContaining({
        name: "p95_latency_ms",
        status: "insufficient",
        passed: null,
        samples: 19,
        minimumSamples: 20,
      }),
      expect.objectContaining({
        name: "p99_latency_ms",
        status: "insufficient",
        passed: null,
        samples: 19,
        minimumSamples: 100,
      }),
    ]));
  });

  it("redacts secrets from report URLs, headers, scenario metadata, and baseline keys", () => {
    const request = createRequest();
    request.config.url = "https://alice:password@example.com/items?access_token=report-url-secret";
    request.config.headers = [
      { name: "aUtHoRiZaTiOn", value: "Bearer report-auth-secret" },
      { name: "Accept", value: "application/json" },
    ];
    request.config.scenarioSteps = [{
      kind: "request",
      method: "GET",
      url: "https://example.com/items?X-Amz-Signature=report-step-secret",
      headers: [{ name: "X-API-Key", value: "report-api-secret" }],
      captures: [{
        name: "token",
        path: "$.items[0].token",
        scope: "run",
        onStatus: "2xx",
      }],
    }];

    const report = buildRunReport(request, reportMetrics([]));
    const serialized = JSON.stringify(report);
    const otherSecrets = structuredClone(request);
    otherSecrets.config.url = "https://bob:other@example.com/items?access_token=different";
    otherSecrets.config.scenarioSteps[0].url = "https://example.com/items?X-Amz-Signature=different";

    expect(report.schemaVersion).toBe(5);
    expect(serialized).not.toMatch(/alice|password|report-(?:url|auth|step|api)-secret/);
    expect(report.run.targetUrl).toContain("REDACTED");
    expect(report.config.headers[0].value).toBe("[redacted]");
    expect(report.config.scenarioSteps[0].url).toContain("REDACTED");
    expect(report.config.scenarioSteps[0].captures).toEqual([{
      name: "token",
      path: "$.items[0].token",
      scope: "run",
      onStatus: "2xx",
    }]);
    expect(buildRunReport(otherSecrets, reportMetrics([])).baseline.key).toBe(report.baseline.key);
  });

  it("purges legacy baselines that may contain raw signed URLs", () => {
    const removeItem = vi.fn();
    vi.stubGlobal("window", { localStorage: { removeItem } });

    purgeLegacyRunBaselines();

    expect(removeItem).toHaveBeenCalledWith("flowroutine:run-baselines:v1");
    expect(removeItem).toHaveBeenCalledWith("flowroutine:run-baselines:v2");
  });

  it("stores only redacted v3 baseline metadata", () => {
    const values = new Map<string, string>([
      ["flowroutine:run-baselines:v2", "legacy-baseline-secret"],
    ]);
    vi.stubGlobal("window", {
      localStorage: {
        getItem: (key: string) => values.get(key) ?? null,
        setItem: (key: string, value: string) => values.set(key, value),
        removeItem: (key: string) => values.delete(key),
      },
    });
    const request = createRequest();
    request.config.url = "https://example.com?access_token=stored-url-secret";

    applyBaselineComparison(buildRunReport(request, reportMetrics([])), request);
    const persisted = Array.from(values.values()).join("\n");

    expect(values.has("flowroutine:run-baselines:v2")).toBe(false);
    expect(values.has("flowroutine:run-baselines:v3")).toBe(true);
    expect(persisted).not.toMatch(/legacy-baseline-secret|stored-url-secret/);
    expect(persisted).toContain("REDACTED");
  });
});

function createRequest(): StartRequest {
  return {
    config: {
      url: "https://example.com",
      method: "GET",
      headers: [],
      body: "",
      virtualUsers: 1,
      durationMs: 1_000,
      requestTimeoutMs: 1_000,
      maxConnsPerHost: 10,
      readBufferSize: 4_096,
      writeBufferSize: 4_096,
      maxResponseBytes: 1_048_576,
      latencySampleRate: 1,
      rateLimitRps: 0,
      rampUpMs: 0,
      scenarioSteps: [],
    },
    batchIntervalMs: 1_000,
  };
}

function reportMetrics(batches: MetricsBatch[]) {
  return {
    finalBatch: batches[batches.length - 1] ?? null,
    timeline: batches.map(metricHistoryPoint),
    batchCount: batches.length,
  };
}

function completedBatch(total: number, samples: number): MetricsBatch {
  return {
    timestampUnixMs: 2_000,
    startedAtUnixMs: 1_000,
    running: false,
    rps: total,
    total,
    success: total,
    failed: 0,
    timeout: 0,
    dns: 0,
    tls: 0,
    connRefused: 0,
    otherErrors: 0,
    assertionsFailed: 0,
    captureFailures: 0,
    templateFailures: 0,
    latencyPercentileErrorBoundPct: 2,
    intervalLatency: {
      samples,
      avgMs: 100,
      p95Ms: 100,
      p99Ms: 200,
      p999Ms: 300,
    },
    runLatency: {
      samples,
      avgMs: 100,
      minMs: 50,
      maxMs: 300,
      p95Ms: 100,
      p99Ms: 200,
      p999Ms: 300,
    },
    bytesRead: 0,
    bytesWritten: 0,
    statusCodes: [{ code: 200, count: total }],
  };
}
