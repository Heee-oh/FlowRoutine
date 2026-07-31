import type { MetricsBatch, StartRequest } from "./types";

const metricsEvent = "metrics:batch";

export function startLoad(request: StartRequest) {
  const startLoad = window.go?.main?.App?.StartLoad;
  if (!startLoad) {
    return Promise.reject(new Error("Wails bridge is unavailable"));
  }
  return startLoad({
    config: request.config,
    batchIntervalMs: request.batchIntervalMs,
  });
}

export function preflightLoad(request: StartRequest) {
  const preflightLoad = window.go?.main?.App?.PreflightLoad;
  if (!preflightLoad) {
    return Promise.reject(new Error("Wails bridge is unavailable"));
  }
  return preflightLoad({
    config: request.config,
    batchIntervalMs: request.batchIntervalMs,
  });
}

export function stopLoad() {
  const stopLoad = window.go?.main?.App?.StopLoad;
  if (!stopLoad) {
    return Promise.reject(new Error("Wails bridge is unavailable"));
  }
  return stopLoad();
}

export function importOpenAPI(url: string) {
  const importOpenAPI = window.go?.main?.App?.ImportOpenAPI;
  if (!importOpenAPI) {
    return Promise.reject(new Error("Wails bridge is unavailable"));
  }
  return importOpenAPI(url);
}

export function onMetricsBatch(callback: (batch: MetricsBatch) => void) {
  const eventsOn = window.runtime?.EventsOn;
  if (!eventsOn) {
    return () => undefined;
  }

  return eventsOn(metricsEvent, (...data: unknown[]) => {
    const payload = Array.isArray(data[0]) ? data[0][0] : data[0];
    if (isMetricsBatch(payload)) {
      callback(payload);
    }
  });
}

function isMetricsBatch(value: unknown): value is MetricsBatch {
  if (!value || typeof value !== "object") {
    return false;
  }
  return typeof (value as MetricsBatch).timestampUnixMs === "number";
}
