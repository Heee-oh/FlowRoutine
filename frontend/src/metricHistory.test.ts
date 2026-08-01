import { describe, expect, it } from "vitest";
import {
  BoundedMetricHistory,
  MAX_LIVE_METRIC_POINTS,
  MAX_REPORT_TIMELINE_POINTS,
  type MetricHistoryPoint,
} from "./metricHistory";

describe("BoundedMetricHistory", () => {
  it("bounds one-hour histories while retaining extrema and endpoints", () => {
    const durationMs = 3_600_000;
    const intervalMs = 100;
    const history = new BoundedMetricHistory(durationMs, MAX_REPORT_TIMELINE_POINTS);
    const special = new Map<number, Partial<MetricHistoryPoint>>([
      [5_000, { rps: 0 }],
      [10_000, { rps: 1_000_000 }],
      [15_000, { p95LatencyMs: 0 }],
      [20_000, { p95LatencyMs: 20_000 }],
      [25_000, { p99LatencyMs: 0 }],
      [30_000, { p99LatencyMs: 30_000 }],
    ]);

    for (let timestampUnixMs = 0; timestampUnixMs <= durationMs; timestampUnixMs += intervalMs) {
      history.add(point(timestampUnixMs, special.get(timestampUnixMs)));
    }

    const values = history.values();
    const timestamps = new Set(values.map((value) => value.timestampUnixMs));
    expect(values.length).toBeLessThanOrEqual(MAX_REPORT_TIMELINE_POINTS);
    expect(values[0].timestampUnixMs).toBe(0);
    expect(values[values.length - 1]?.timestampUnixMs).toBe(durationMs);
    for (const timestamp of special.keys()) {
      expect(timestamps.has(timestamp)).toBe(true);
    }
  });

  it("keeps rolling chart memory bounded to the selected window", () => {
    const windowMs = 3_600_000;
    const history = new BoundedMetricHistory(windowMs, MAX_LIVE_METRIC_POINTS);
    const end = windowMs * 2;
    for (let timestampUnixMs = 0; timestampUnixMs <= end; timestampUnixMs += 100) {
      history.add(point(timestampUnixMs), timestampUnixMs - windowMs);
    }

    const values = history.values(end - windowMs);
    expect(values.length).toBeLessThanOrEqual(MAX_LIVE_METRIC_POINTS);
    expect(values.every((value) => value.timestampUnixMs >= end - windowMs)).toBe(true);
    expect(values[values.length - 1]?.timestampUnixMs).toBe(end);
  });
});

function point(timestampUnixMs: number, overrides: Partial<MetricHistoryPoint> = {}): MetricHistoryPoint {
  return {
    timestampUnixMs,
    rps: 100,
    total: timestampUnixMs,
    failed: 0,
    p95LatencyMs: 50,
    p99LatencyMs: 75,
    ...overrides,
  };
}
