import type { MetricsBatch, StartRequest } from "./types";
import { StartLoad, StopLoad } from "../wailsjs/go/main/App";
import type { bridge } from "../wailsjs/go/models";
import { EventsOn } from "../wailsjs/runtime/runtime";

const metricsEvent = "metrics:batch";

export function startLoad(request: StartRequest) {
  if (!window.go?.main?.App?.StartLoad) {
    return Promise.reject(new Error("Wails bridge is unavailable"));
  }
  return StartLoad(request as unknown as bridge.StartRequest);
}

export function stopLoad() {
  if (!window.go?.main?.App?.StopLoad) {
    return Promise.reject(new Error("Wails bridge is unavailable"));
  }
  return StopLoad();
}

export function onMetricsBatch(callback: (batch: MetricsBatch) => void) {
  if (!window.runtime?.EventsOn) {
    return () => undefined;
  }

  return EventsOn(metricsEvent, (...data: unknown[]) => {
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
