import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import type { MetricsBatch, RequestStepMetrics } from "../types";
import { StatusDetailsDialog } from "./Metrics";

describe("request-step metrics", () => {
  it("ranks failing steps before slower successful steps and shows diagnostics", () => {
    const markup = renderToStaticMarkup(
      <StatusDetailsDialog
        batch={{
          ...emptyBatch(),
          total: 12,
          success: 10,
          failed: 2,
          assertionFailuresByType: {
            status: 0,
            header: 1,
            json: 0,
            responseLatency: 0,
            stepLatency: 0,
            countOnly: 1,
          },
          stepMetrics: [
            stepMetrics("slow", "GET slow", 10, 0, 900, [{ code: 200, count: 10 }]),
            stepMetrics("failing", "POST failing", 2, 2, 20, [{ code: 500, count: 2 }]),
          ],
        }}
        onClose={vi.fn()}
      />,
    );

    expect(markup.indexOf("POST failing")).toBeLessThan(markup.indexOf("GET slow"));
    expect(markup).toContain("2 HTTP");
    expect(markup).toContain("500 2");
    expect(markup).toContain("900 / 900 ms");
    expect(markup).toContain("Header 1");
    expect(markup).toContain("Count-only 1");
  });
});

function stepMetrics(
  id: string,
  name: string,
  total: number,
  failed: number,
  p99Ms: number,
  statusCodes: RequestStepMetrics["statusCodes"],
): RequestStepMetrics {
  return {
    id,
    name,
    total,
    success: total-failed,
    failed,
    timeout: 0,
    dns: 0,
    tls: 0,
    connRefused: 0,
    otherErrors: failed,
    assertionsFailed: 0,
    captureFailures: 0,
    templateFailures: 0,
    runLatency: {
      samples: total,
      avgMs: p99Ms/2,
      minMs: 1,
      maxMs: p99Ms,
      p95Ms: p99Ms,
      p99Ms,
      p999Ms: p99Ms,
    },
    statusCodes,
  };
}

function emptyBatch(): MetricsBatch {
  return {
    timestampUnixMs: 2_000,
    startedAtUnixMs: 1_000,
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
    latencyPercentileErrorBoundPct: 2,
    intervalLatency: { samples: 0, avgMs: 0, p95Ms: 0, p99Ms: 0, p999Ms: 0 },
    runLatency: { samples: 0, avgMs: 0, minMs: 0, maxMs: 0, p95Ms: 0, p99Ms: 0, p999Ms: 0 },
    bytesRead: 0,
    bytesWritten: 0,
    statusCodes: [],
  };
}
