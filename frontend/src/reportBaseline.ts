import type { RunReport } from "./report";
import { redactSensitiveURL } from "./secretSanitization";
import type { StartRequest } from "./types";

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
  schemaVersion: 3;
  key: string;
  savedAtUnixMs: number;
  targetUrl: string;
  method: string;
  averageRps: number;
  failureRate: number;
  p95LatencyMs: number;
  p99LatencyMs: number;
};

const baselineStorageKey = "flowroutine:run-baselines:v3";
const legacyBaselineStorageKeys = [
  "flowroutine:run-baselines:v1",
  "flowroutine:run-baselines:v2",
];
const maxBaselines = 24;

export function applyBaselineComparison(report: RunReport, _request: StartRequest): RunReport {
  if (typeof window === "undefined") {
    return report;
  }
  try {
    const baselines = readBaselines();
    const key = report.baseline.key;
    const previous = baselines[key];
    const current = buildBaselineSnapshot(key, report);
    const baseline = previous ? compareBaseline(previous, current) : report.baseline;
    writeBaselines({ ...baselines, [key]: current });
    return { ...report, baseline };
  } catch {
    return report;
  }
}

export function purgeLegacyRunBaselines() {
  if (typeof window === "undefined") {
    return;
  }
  try {
    removeLegacyBaselines();
  } catch {
    // Storage can be unavailable in restricted browser contexts.
  }
}

function buildBaselineSnapshot(key: string, report: RunReport): BaselineSnapshot {
  return {
    schemaVersion: 3,
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
  removeLegacyBaselines();
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
      .map(([key, value]) => [key, sanitizeBaselineSnapshot(key, value as BaselineSnapshot)]),
  );
}

function writeBaselines(baselines: Record<string, BaselineSnapshot>) {
  removeLegacyBaselines();
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
  return snapshot.schemaVersion === 3 &&
    typeof snapshot.key === "string" &&
    typeof snapshot.savedAtUnixMs === "number" &&
    typeof snapshot.targetUrl === "string" &&
    typeof snapshot.method === "string" &&
    typeof snapshot.averageRps === "number" &&
    typeof snapshot.failureRate === "number" &&
    typeof snapshot.p95LatencyMs === "number" &&
    typeof snapshot.p99LatencyMs === "number";
}

function sanitizeBaselineSnapshot(key: string, snapshot: BaselineSnapshot): BaselineSnapshot {
  return {
    schemaVersion: 3,
    key,
    savedAtUnixMs: snapshot.savedAtUnixMs,
    targetUrl: redactSensitiveURL(snapshot.targetUrl),
    method: snapshot.method,
    averageRps: snapshot.averageRps,
    failureRate: snapshot.failureRate,
    p95LatencyMs: snapshot.p95LatencyMs,
    p99LatencyMs: snapshot.p99LatencyMs,
  };
}

function removeLegacyBaselines() {
  for (const key of legacyBaselineStorageKeys) {
    window.localStorage.removeItem(key);
  }
}
