import { afterEach, describe, expect, it, vi } from "vitest";

import { importOpenAPI, preflightLoad } from "./wails";
import type {
  OpenAPIImportRequest,
  OpenAPIImportResponse,
  PreflightResponse,
  StartRequest,
} from "./types";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("preflightLoad", () => {
  it("sends only backend load settings and returns the estimate", async () => {
    const request: StartRequest = {
      config: {
        url: "http://127.0.0.1:8080",
        method: "GET",
        headers: [],
        body: "",
        virtualUsers: 10,
        durationMs: 1_000,
        requestTimeoutMs: 1_000,
        maxConnsPerHost: 10,
        readBufferSize: 4_096,
        writeBufferSize: 4_096,
        maxResponseBytes: 1_048_576,
        latencySampleRate: 1,
        rateLimitRps: 100,
        rampUpMs: 0,
        scenarioSteps: [],
      },
      batchIntervalMs: 150,
      qualityGate: {
        maxFailureRatePct: 1,
        maxP95LatencyMs: 500,
        maxP99LatencyMs: 1_000,
        minRps: 0,
      },
    };
    const response: PreflightResponse = {
      effectiveConfig: {
        virtualUsers: 10,
        durationMs: 1_000,
        requestTimeoutMs: 1_000,
        maxConnsPerHost: 10,
        readBufferSize: 4_096,
        writeBufferSize: 4_096,
        maxResponseBytes: 1_048_576,
        latencySampleRate: 1,
        rateLimitRps: 100,
        rampUpMs: 0,
      },
      effectiveBatchIntervalMs: 150,
      estimate: {
        memoryBytes: 11_264_000,
        connections: 10,
        targetHosts: 1,
      },
      warnings: [],
    };
    const bridge = vi.fn().mockResolvedValue(response);
    vi.stubGlobal("window", {
      go: {
        main: {
          App: {
            PreflightLoad: bridge,
          },
        },
      },
    });

    await expect(preflightLoad(request)).resolves.toEqual(response);
    expect(bridge).toHaveBeenCalledWith({
      config: request.config,
      batchIntervalMs: request.batchIntervalMs,
    });
  });
});

describe("importOpenAPI", () => {
  it("forwards explicit network consent without adding document payloads", async () => {
    const request: OpenAPIImportRequest = {
      url: "http://localhost:8080/openapi.yaml",
      allowPrivateNetwork: true,
      allowRedirects: false,
      allowExternalRefs: true,
    };
    const response: OpenAPIImportResponse = {
      sourceUrl: request.url,
      openapi: "3.1.0",
      title: "Demo",
      version: "1.0.0",
      servers: [],
      endpoints: [],
    };
    const bridge = vi.fn().mockResolvedValue(response);
    vi.stubGlobal("window", {
      go: {
        main: {
          App: {
            ImportOpenAPI: bridge,
          },
        },
      },
    });

    await expect(importOpenAPI(request)).resolves.toEqual(response);
    expect(bridge).toHaveBeenCalledWith(request);
    expect(response).not.toHaveProperty("rawJson");
  });
});
