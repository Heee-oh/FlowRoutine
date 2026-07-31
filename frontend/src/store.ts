import { create } from "zustand";
import { applyBaselineComparison, buildRunReport, type RunReport } from "./report";
import {
  BoundedMetricHistory,
  MAX_LIVE_METRIC_POINTS,
  MAX_REPORT_TIMELINE_POINTS,
  metricHistoryPoint,
  type MetricHistoryPoint,
} from "./metricHistory";
import type { FlowSettings, Header, MetricsBatch, StartRequest } from "./types";
import { legacyLoadProfile } from "./loadProfile";

export const DEFAULT_METRIC_WINDOW_MS = 60_000;
export const DEFAULT_METRIC_BATCH_INTERVAL_MS = 150;
export const MIN_METRIC_BATCH_INTERVAL_MS = 100;
export const MAX_METRIC_BATCH_INTERVAL_MS = 5_000;

const defaultSettings: FlowSettings = {
  url: "http://127.0.0.1:8080",
  method: "GET",
  headersText: "Content-Type: application/json",
  body: "",
  virtualUsers: 128,
  durationMs: 10_000,
  requestTimeoutMs: 1_000,
  batchIntervalMs: DEFAULT_METRIC_BATCH_INTERVAL_MS,
  maxConnsPerHost: 10_000,
  readBufferSize: 4_096,
  writeBufferSize: 4_096,
  maxResponseBytes: 1_048_576,
  latencySampleRate: 1,
  rateLimitRps: 1_000,
  rampUpMs: 1_000,
  profile: legacyLoadProfile({
    virtualUsers: 128,
    durationMs: 10_000,
    rampUpMs: 1_000,
    requestTimeoutMs: 1_000,
  }),
};

type LoadState = {
  settings: FlowSettings;
  running: boolean;
  stopping: boolean;
  error: string;
  updateSetting: <K extends keyof FlowSettings>(key: K, value: FlowSettings[K]) => void;
  setRunning: (running: boolean) => void;
  setStopping: (stopping: boolean) => void;
  setError: (error: string) => void;
  buildStartRequest: () => StartRequest;
};

type MetricsState = {
  points: MetricHistoryPoint[];
  latest: MetricsBatch | null;
  latestReport: RunReport | null;
  metricWindowMs: number;
  beginRun: (request: StartRequest) => void;
  pushBatch: (batch: MetricsBatch) => void;
  setMetricWindowMs: (metricWindowMs: number) => void;
  reset: () => void;
  activeRequest: StartRequest | null;
  liveHistory: BoundedMetricHistory;
  reportHistory: BoundedMetricHistory | null;
  runBatchCount: number;
};

export const useLoadStore = create<LoadState>((set, get) => ({
  settings: defaultSettings,
  running: false,
  stopping: false,
  error: "",
  updateSetting: (key, value) =>
    set((state) => ({
      settings: {
        ...state.settings,
        [key]: value,
      },
    })),
  setRunning: (running) => set({ running }),
  setStopping: (stopping) => set({ stopping }),
  setError: (error) => set({ error }),
  buildStartRequest: () => {
    const settings = get().settings;
    return {
      config: {
        url: settings.url.trim(),
        method: settings.method,
        headers: parseHeaders(settings.headersText),
        body: settings.body,
        virtualUsers: settings.virtualUsers,
        durationMs: settings.durationMs,
        requestTimeoutMs: settings.requestTimeoutMs,
        maxConnsPerHost: settings.maxConnsPerHost,
        readBufferSize: settings.readBufferSize,
        writeBufferSize: settings.writeBufferSize,
        maxResponseBytes: settings.maxResponseBytes,
        latencySampleRate: settings.latencySampleRate,
        rateLimitRps: settings.rateLimitRps,
        rampUpMs: settings.rampUpMs,
        profile: settings.profile,
        scenarioSteps: [],
      },
      batchIntervalMs: settings.batchIntervalMs,
    };
  },
}));

export const useMetricsStore = create<MetricsState>((set) => ({
  points: [],
  latest: null,
  latestReport: null,
  metricWindowMs: DEFAULT_METRIC_WINDOW_MS,
  activeRequest: null,
  liveHistory: new BoundedMetricHistory(DEFAULT_METRIC_WINDOW_MS, MAX_LIVE_METRIC_POINTS),
  reportHistory: null,
  runBatchCount: 0,
  beginRun: (request) => set((state) => ({
    points: [],
    latest: null,
    latestReport: null,
    activeRequest: request,
    liveHistory: new BoundedMetricHistory(state.metricWindowMs, MAX_LIVE_METRIC_POINTS),
    reportHistory: new BoundedMetricHistory(request.config.durationMs, MAX_REPORT_TIMELINE_POINTS),
    runBatchCount: 0,
  })),
  pushBatch: (batch) =>
    set((state) => {
      const nextBatch = batch.stepMetrics === undefined && state.latest?.stepMetrics
        ? { ...batch, stepMetrics: state.latest.stepMetrics }
        : batch;
      const cutoff = nextBatch.timestampUnixMs - state.metricWindowMs;
      const point = metricHistoryPoint(nextBatch);
      state.liveHistory.add(point, cutoff);
      let reportHistory = state.reportHistory;
      let runBatchCount = state.runBatchCount;
      let completedReport: RunReport | null = null;
      if (state.activeRequest) {
        reportHistory ??= new BoundedMetricHistory(
          state.activeRequest.config.durationMs,
          MAX_REPORT_TIMELINE_POINTS,
        );
        reportHistory.add(point);
        runBatchCount += 1;
        if (!nextBatch.running) {
          completedReport = applyBaselineComparison(buildRunReport(state.activeRequest, {
            finalBatch: nextBatch,
            timeline: reportHistory.values(),
            batchCount: runBatchCount,
          }), state.activeRequest);
        }
      }
      return {
        latest: nextBatch,
        points: state.liveHistory.values(cutoff),
        latestReport: completedReport ?? state.latestReport,
        activeRequest: completedReport ? null : state.activeRequest,
        reportHistory: completedReport ? null : reportHistory,
        runBatchCount: completedReport ? 0 : runBatchCount,
      };
    }),
  setMetricWindowMs: (metricWindowMs) =>
    set((state) => {
      const nextMetricWindowMs = Math.max(1_000, Math.floor(metricWindowMs));
      const latestTimestamp = state.latest?.timestampUnixMs;
      const liveHistory = new BoundedMetricHistory(nextMetricWindowMs, MAX_LIVE_METRIC_POINTS);
      const cutoff = latestTimestamp === undefined
        ? Number.NEGATIVE_INFINITY
        : latestTimestamp - nextMetricWindowMs;
      for (const point of state.points) {
        liveHistory.add(point, cutoff);
      }
      return {
        metricWindowMs: nextMetricWindowMs,
        liveHistory,
        points: liveHistory.values(cutoff),
      };
    }),
  reset: () => set((state) => ({
    points: [],
    latest: null,
    activeRequest: null,
    liveHistory: new BoundedMetricHistory(state.metricWindowMs, MAX_LIVE_METRIC_POINTS),
    reportHistory: null,
    runBatchCount: 0,
  })),
}));

function parseHeaders(raw: string): Header[] {
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const splitAt = line.indexOf(":");
      if (splitAt < 1) {
        throw new Error(`Invalid header: ${line}`);
      }
      return {
        name: line.slice(0, splitAt).trim(),
        value: line.slice(splitAt + 1).trim(),
      };
    });
}
