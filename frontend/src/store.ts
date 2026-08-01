import { create } from "zustand";
import { applyBaselineComparison, buildRunReport, type RunReport } from "./report";
import type { FlowSettings, Header, MetricsBatch, StartRequest } from "./types";

export const DEFAULT_METRIC_WINDOW_MS = 60_000;

const defaultSettings: FlowSettings = {
  url: "http://127.0.0.1:8080",
  method: "GET",
  headersText: "Content-Type: application/json",
  body: "",
  virtualUsers: 128,
  durationMs: 10_000,
  requestTimeoutMs: 1_000,
  batchIntervalMs: 150,
  maxConnsPerHost: 10_000,
  readBufferSize: 4_096,
  writeBufferSize: 4_096,
  maxResponseBytes: 1_048_576,
  latencySampleRate: 1,
  rateLimitRps: 1_000,
  rampUpMs: 1_000,
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
  points: MetricsBatch[];
  latest: MetricsBatch | null;
  latestReport: RunReport | null;
  metricWindowMs: number;
  beginRun: (request: StartRequest) => void;
  pushBatch: (batch: MetricsBatch) => void;
  setMetricWindowMs: (metricWindowMs: number) => void;
  reset: () => void;
  activeRequest: StartRequest | null;
  runPoints: MetricsBatch[];
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
  runPoints: [],
  beginRun: (request) => set({ points: [], latest: null, latestReport: null, activeRequest: request, runPoints: [] }),
  pushBatch: (batch) =>
    set((state) => {
      const cutoff = batch.timestampUnixMs - state.metricWindowMs;
      const runPoints = state.activeRequest ? state.runPoints.concat(batch) : state.runPoints;
      const completedReport = state.activeRequest && !batch.running
        ? applyBaselineComparison(buildRunReport(state.activeRequest, runPoints), state.activeRequest)
        : null;
      return {
        latest: batch,
        points: state.points.concat(batch).filter((point) => point.timestampUnixMs >= cutoff),
        runPoints,
        latestReport: completedReport ?? state.latestReport,
        activeRequest: completedReport ? null : state.activeRequest,
      };
    }),
  setMetricWindowMs: (metricWindowMs) =>
    set((state) => {
      const nextMetricWindowMs = Math.max(1_000, Math.floor(metricWindowMs));
      const latestTimestamp = state.latest?.timestampUnixMs;
      return {
        metricWindowMs: nextMetricWindowMs,
        points: latestTimestamp === undefined
          ? state.points
          : state.points.filter((point) => point.timestampUnixMs >= latestTimestamp - nextMetricWindowMs),
      };
    }),
  reset: () => set({ points: [], latest: null, activeRequest: null, runPoints: [] }),
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
