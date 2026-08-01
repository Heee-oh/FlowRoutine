import { describe, expect, it } from "vitest";

import { assessStartSafety } from "./flowModel";
import type { LoadConfig, PreflightResponse } from "./types";

describe("assessStartSafety", () => {
  it("presents backend resource warnings and estimates", () => {
    const config: LoadConfig = {
      url: "http://127.0.0.1:8080",
      method: "GET",
      headers: [],
      body: "",
      virtualUsers: 100,
      durationMs: 10_000,
      requestTimeoutMs: 1_000,
      maxConnsPerHost: 1_000,
      readBufferSize: 4_096,
      writeBufferSize: 4_096,
      maxResponseBytes: 1_048_576,
      latencySampleRate: 1,
      rateLimitRps: 1_000,
      rampUpMs: 0,
      scenarioSteps: [],
    };
    const preflight: PreflightResponse = {
      effectiveConfig: {
        virtualUsers: config.virtualUsers,
        durationMs: config.durationMs,
        requestTimeoutMs: config.requestTimeoutMs,
        maxConnsPerHost: config.maxConnsPerHost,
        readBufferSize: config.readBufferSize,
        writeBufferSize: config.writeBufferSize,
        maxResponseBytes: config.maxResponseBytes,
        latencySampleRate: config.latencySampleRate,
        rateLimitRps: config.rateLimitRps,
        rampUpMs: config.rampUpMs,
      },
      effectiveBatchIntervalMs: 150,
      estimate: {
        memoryBytes: 600 * 1_048_576,
        connections: 8_000,
        targetHosts: 2,
      },
      warnings: [{
        code: "high_memory",
        message: "Reduce virtual users or response size.",
      }],
    };

    const safety = assessStartSafety(config, preflight);

    expect(safety.confirmationRequired).toBe(true);
    expect(safety.warnings).toContain("Reduce virtual users or response size.");
    expect(safety.estimatedMemoryBytes).toBe(preflight.estimate.memoryBytes);
    expect(safety.estimatedConnections).toBe(8_000);
    expect(safety.targetHosts).toBe(2);
  });
});
