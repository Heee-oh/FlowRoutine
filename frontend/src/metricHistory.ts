import type { MetricsBatch } from "./types";

export const MAX_LIVE_METRIC_POINTS = 2_048;
export const MAX_REPORT_TIMELINE_POINTS = 1_000;

export type MetricHistoryPoint = {
  timestampUnixMs: number;
  rps: number;
  total: number;
  failed: number;
  p95LatencyMs: number;
  p99LatencyMs: number;
};

type MetricBucket = {
  key: number;
  first: MetricHistoryPoint;
  last: MetricHistoryPoint;
  minRps: MetricHistoryPoint;
  maxRps: MetricHistoryPoint;
  minP95: MetricHistoryPoint;
  maxP95: MetricHistoryPoint;
  minP99: MetricHistoryPoint;
  maxP99: MetricHistoryPoint;
};

const maxBucketCandidates = 8;

// Keeps first/last plus RPS, P95, and P99 extrema per bucket. Buckets are
// merged geometrically when the configured point budget would be exceeded.
export class BoundedMetricHistory {
  private readonly maxPoints: number;
  private readonly maxBuckets: number;
  private bucketWidthMs: number;
  private buckets: MetricBucket[] = [];

  constructor(expectedSpanMs: number, maxPoints: number) {
    this.maxPoints = Math.max(maxBucketCandidates, Math.floor(maxPoints));
    this.maxBuckets = Math.max(1, Math.floor(this.maxPoints / maxBucketCandidates));
    this.bucketWidthMs = Math.max(1, Math.ceil(Math.max(1, expectedSpanMs) / this.maxBuckets));
  }

  add(point: MetricHistoryPoint, cutoffUnixMs = Number.NEGATIVE_INFINITY) {
    if (point.timestampUnixMs < cutoffUnixMs) {
      return;
    }
    this.prune(cutoffUnixMs);
    this.addToBucket(point);
    while (this.buckets.length > this.maxBuckets) {
      this.rebucket(this.bucketWidthMs * 2, cutoffUnixMs);
    }
  }

  values(cutoffUnixMs = Number.NEGATIVE_INFINITY): MetricHistoryPoint[] {
    return this.candidateValues(cutoffUnixMs);
  }

  private candidateValues(cutoffUnixMs: number) {
    return this.buckets
      .flatMap(bucketPoints)
      .filter((point) => point.timestampUnixMs >= cutoffUnixMs)
      .sort((left, right) => left.timestampUnixMs - right.timestampUnixMs);
  }

  private addToBucket(point: MetricHistoryPoint) {
    const key = Math.floor(point.timestampUnixMs / this.bucketWidthMs);
    const lastBucket = this.buckets[this.buckets.length - 1];
    if (lastBucket?.key === key) {
      updateBucket(lastBucket, point);
      return;
    }
    if (!lastBucket || lastBucket.key < key) {
      this.buckets.push(createBucket(key, point));
      return;
    }
    const existing = this.buckets.find((bucket) => bucket.key === key);
    if (existing) {
      updateBucket(existing, point);
      return;
    }
    this.buckets.push(createBucket(key, point));
    this.buckets.sort((left, right) => left.key - right.key);
  }

  private prune(cutoffUnixMs: number) {
    if (!Number.isFinite(cutoffUnixMs)) {
      return;
    }
    const firstRetained = this.buckets.findIndex((bucket) => bucket.last.timestampUnixMs >= cutoffUnixMs);
    if (firstRetained < 0) {
      this.buckets = [];
    } else if (firstRetained > 0) {
      this.buckets.splice(0, firstRetained);
    }
  }

  private rebucket(nextWidthMs: number, cutoffUnixMs: number) {
    const retained = this.candidateValues(cutoffUnixMs);
    this.bucketWidthMs = nextWidthMs;
    this.buckets = [];
    for (const point of retained) {
      this.addToBucket(point);
    }
  }
}

export function metricHistoryPoint(batch: MetricsBatch): MetricHistoryPoint {
  return {
    timestampUnixMs: batch.timestampUnixMs,
    rps: batch.rps,
    total: batch.total,
    failed: batch.failed,
    p95LatencyMs: batch.intervalLatency.p95Ms,
    p99LatencyMs: batch.intervalLatency.p99Ms,
  };
}

function createBucket(key: number, point: MetricHistoryPoint): MetricBucket {
  return {
    key,
    first: point,
    last: point,
    minRps: point,
    maxRps: point,
    minP95: point,
    maxP95: point,
    minP99: point,
    maxP99: point,
  };
}

function updateBucket(bucket: MetricBucket, point: MetricHistoryPoint) {
  if (point.timestampUnixMs < bucket.first.timestampUnixMs) {
    bucket.first = point;
  }
  if (point.timestampUnixMs >= bucket.last.timestampUnixMs) {
    bucket.last = point;
  }
  bucket.minRps = lowerPoint(bucket.minRps, point, "rps");
  bucket.maxRps = higherPoint(bucket.maxRps, point, "rps");
  bucket.minP95 = lowerPoint(bucket.minP95, point, "p95LatencyMs");
  bucket.maxP95 = higherPoint(bucket.maxP95, point, "p95LatencyMs");
  bucket.minP99 = lowerPoint(bucket.minP99, point, "p99LatencyMs");
  bucket.maxP99 = higherPoint(bucket.maxP99, point, "p99LatencyMs");
}

function lowerPoint(
  current: MetricHistoryPoint,
  candidate: MetricHistoryPoint,
  key: "rps" | "p95LatencyMs" | "p99LatencyMs",
) {
  return candidate[key] < current[key] ? candidate : current;
}

function higherPoint(
  current: MetricHistoryPoint,
  candidate: MetricHistoryPoint,
  key: "rps" | "p95LatencyMs" | "p99LatencyMs",
) {
  return candidate[key] > current[key] ? candidate : current;
}

function bucketPoints(bucket: MetricBucket) {
  return Array.from(new Set([
    bucket.first,
    bucket.minRps,
    bucket.maxRps,
    bucket.minP95,
    bucket.maxP95,
    bucket.minP99,
    bucket.maxP99,
    bucket.last,
  ])).sort((left, right) => left.timestampUnixMs - right.timestampUnixMs);
}
