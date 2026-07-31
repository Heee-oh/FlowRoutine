import { describe, expect, it } from "vitest";

import { buildRunReport } from "./report";
import type { MetricsBatch, StartRequest } from "./types";

describe("buildRunReport", () => {
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

    const report = buildRunReport(request, [finalBatch]);

    expect(report.summary.latencySamples).toBe(100);
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
  });
});
