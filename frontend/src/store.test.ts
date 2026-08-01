import { afterEach, describe, expect, it } from "vitest";
import { MAX_LIVE_METRIC_POINTS, MAX_REPORT_TIMELINE_POINTS } from "./metricHistory";
import { DEFAULT_METRIC_WINDOW_MS, useMetricsStore } from "./store";
import type { MetricsBatch, StartRequest } from "./types";

describe("metrics store", () => {
  afterEach(() => {
    useMetricsStore.setState({
      latestReport: null,
      metricWindowMs: DEFAULT_METRIC_WINDOW_MS,
    });
    useMetricsStore.getState().reset();
  });

  it("keeps bounded histories and builds exact summaries from the final batch", () => {
    const batchCount = 2_500;
    const request = createRequest(batchCount * 100);
    useMetricsStore.getState().setMetricWindowMs(request.config.durationMs);
    useMetricsStore.getState().beginRun(request);

    for (let index = 1; index <= batchCount; index += 1) {
      useMetricsStore.getState().pushBatch(batch(index, index < batchCount));
    }

    const state = useMetricsStore.getState();
    expect(state.points.length).toBeLessThanOrEqual(MAX_LIVE_METRIC_POINTS);
    expect(state.latestReport?.timeline.length).toBeLessThanOrEqual(MAX_REPORT_TIMELINE_POINTS);
    expect(state.latestReport?.run.batchCount).toBe(batchCount);
    expect(state.latestReport?.summary.totalRequests).toBe(batchCount);
    expect(state.latestReport?.summary.latencySamples).toBe(batchCount);
    expect(state.reportHistory).toBeNull();
    expect(state.runBatchCount).toBe(0);
  });
});

function batch(index: number, running: boolean): MetricsBatch {
  return {
    timestampUnixMs: index * 100,
    startedAtUnixMs: 100,
    running,
    rps: index % 100,
    total: index,
    success: index - Math.floor(index / 10),
    failed: Math.floor(index / 10),
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
      samples: 1,
      avgMs: 10,
      p95Ms: index % 50,
      p99Ms: index % 75,
      p999Ms: index % 100,
    },
    runLatency: {
      samples: index,
      avgMs: 10,
      minMs: 1,
      maxMs: 100,
      p95Ms: 50,
      p99Ms: 75,
      p999Ms: 100,
    },
    bytesRead: index * 10,
    bytesWritten: index,
    statusCodes: [{ code: 200, count: index }],
  };
}

function createRequest(durationMs: number): StartRequest {
  return {
    config: {
      url: "https://example.com",
      method: "GET",
      headers: [],
      body: "",
      virtualUsers: 1,
      durationMs,
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
    batchIntervalMs: 100,
  };
}
