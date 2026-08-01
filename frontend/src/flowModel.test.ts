import { afterEach, describe, expect, it, vi } from "vitest";

import {
  assessStartSafety,
  buildStartRequestFromGraph,
  createFlowNode,
  createSavedScenario,
  initialFlowEdges,
  initialFlowNodes,
  loadSavedScenarios,
  parseCaptureText,
} from "./flowModel";
import type { FlowNodeData, SavedScenario } from "./flowTypes";
import type { LoadConfig, PreflightResponse, StartRequest } from "./types";

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

describe("scenario secret handling", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("persists placeholders instead of literal node secrets", () => {
    const node = {
      id: "request-1",
      type: "flow",
      position: { x: 0, y: 0 },
      data: {
        ...requestNodeData(),
        validationError: "runtime validation detail",
        executionOrder: 0,
      },
    };

    const scenario = createSavedScenario([node], [], createRequest());
    const serialized = JSON.stringify(scenario);

    expect(serialized).not.toMatch(/saved-(?:url|header|row|body|direct)-secret/);
    expect(serialized).not.toContain("runtime validation detail");
    expect(serialized).not.toContain("executionOrder");
    expect(serialized).toContain("SECRET_ACCESS_TOKEN");
    expect(serialized).toContain("SECRET_AUTHORIZATION");
    expect(serialized).toContain("SECRET_X_API_KEY");
    expect(serialized).toContain("SECRET_PASSWORD");
  });

  it("sanitizes legacy scenarios when loading and still returns them if migration writes fail", () => {
    const legacy: SavedScenario = {
      id: "legacy",
      name: "legacy",
      savedAtUnixMs: 1,
      nodes: [{
        id: "request-1",
        type: "flow",
        position: { x: 0, y: 0 },
        data: requestNodeData(),
      }],
      edges: [],
    };
    const setItem = vi.fn((_key: string, _value: string) => {
      throw new Error("storage full");
    });
    const removeItem = vi.fn();
    vi.stubGlobal("window", {
      localStorage: {
        getItem: () => JSON.stringify([legacy]),
        setItem,
        removeItem,
      },
    });

    const loaded = loadSavedScenarios();
    const serialized = JSON.stringify(loaded);

    expect(loaded).toHaveLength(1);
    expect(serialized).not.toMatch(/saved-(?:url|header|row|body|direct)-secret/);
    expect(setItem).toHaveBeenCalledOnce();
    expect(setItem.mock.calls[0][1]).not.toMatch(/saved-(?:url|header|row|body|direct)-secret/);
    expect(removeItem).toHaveBeenCalledWith("flowroutine:saved-scenarios");
  });

  it("resolves runtime bindings for execution without storing them in nodes", () => {
    const nodes = initialFlowNodes.map((node) => ({
      ...node,
      data: node.data.kind === "request"
        ? {
            ...node.data,
            url: "https://example.com?access_token={{SECRET_ACCESS_TOKEN}}",
            headersMode: "form" as const,
            headerRows: [{ name: "Authorization", value: "{{SECRET_AUTHORIZATION}}" }],
            body: "{\"password\":\"{{SECRET_PASSWORD}}\"}",
          }
        : { ...node.data },
    }));
    const requestNode = nodes.find((node) => node.data.kind === "request");
    expect(requestNode).toBeDefined();

    const request = buildStartRequestFromGraph(
      nodes,
      initialFlowEdges,
      createRequest(),
      {
        [requestNode!.id]: {
          bindings: {
            SECRET_ACCESS_TOKEN: "runtime-url-secret",
            SECRET_AUTHORIZATION: "Bearer runtime-auth-secret",
            SECRET_PASSWORD: "runtime-body-secret",
          },
        },
      },
    );

    expect(request.config.url).toContain("runtime-url-secret");
    expect(request.config.headers[0].value).toBe("Bearer runtime-auth-secret");
    expect(request.config.body).toContain("runtime-body-secret");
    expect(() => buildStartRequestFromGraph(nodes, initialFlowEdges, createRequest(), {})).toThrow(
      "Runtime secret SECRET_ACCESS_TOKEN is required",
    );
  });
});

describe("scenario graph compilation", () => {
  it("builds scenario steps in edge order rather than React node array order", () => {
    const first = createFlowNode("request", 10, { url: "https://first.example", method: "GET" });
    const delay = createFlowNode("delay", 11, { delayMs: 250 });
    const second = createFlowNode("request", 12, { url: "https://second.example", method: "POST" });
    const engine = createFlowNode("engine", 13, null);
    const metrics = createFlowNode("metrics", 14, null);
    const nodes = [metrics, second, engine, first, delay];
    const edges = [
      { id: "engine-metrics", source: engine.id, target: metrics.id },
      { id: "second-engine", source: second.id, target: engine.id },
      { id: "delay-second", source: delay.id, target: second.id },
      { id: "first-delay", source: first.id, target: delay.id },
    ];

    const request = buildStartRequestFromGraph(nodes, edges, createRequest(), {});

    expect(request.config.url).toBe("https://first.example");
    expect(request.config.scenarioSteps.map((step) => step.kind)).toEqual([
      "request",
      "delay",
      "request",
    ]);
    expect(request.config.scenarioSteps.map((step) => step.id)).toEqual([
      first.id,
      delay.id,
      second.id,
    ]);
    expect(request.config.scenarioSteps.map((step) => step.name)).toEqual([
      "GET first.example/",
      "Delay",
      "POST second.example/",
    ]);
    expect(request.config.scenarioSteps[2].url).toBe("https://second.example");
  });

  it("builds and canonicalizes an arrival-rate profile before preflight", () => {
    const nodes = initialFlowNodes.map((node) => node.data.kind === "engine"
      ? {
          ...node,
          data: {
            ...node.data,
            rateLimitRps: 1_000,
            loadProfile: {
              mode: "ramping-arrival-rate" as const,
              startTarget: 100,
              stages: [
                { durationMs: 1_000, target: 500 },
                { durationMs: 2_000, target: 1_000 },
              ],
              preAllocatedVUs: 5,
              maxVUs: 25,
              gracefulStopMs: 1_500,
            },
          },
        }
      : node);

    const request = buildStartRequestFromGraph(nodes, initialFlowEdges, createRequest(), {});

    expect(request.config.profile?.mode).toBe("ramping-arrival-rate");
    expect(request.config.virtualUsers).toBe(25);
    expect(request.config.durationMs).toBe(3_000);
    expect(request.config.rateLimitRps).toBe(0);
    expect(request.config.rampUpMs).toBe(0);
  });
});

describe("parseCaptureText", () => {
  it("makes capture scope and response-status policy explicit", () => {
    expect(parseCaptureText([
      "token=data.token",
      "session@run:2xx=$.sessions[0].id",
      "error:401=error.code",
    ].join("\n"))).toEqual([
      { name: "token", path: "data.token", scope: "iteration", onStatus: "success" },
      { name: "session", path: "$.sessions[0].id", scope: "run", onStatus: "2xx" },
      { name: "error", path: "error.code", scope: "iteration", onStatus: "401" },
    ]);
    expect(() => parseCaptureText("token@global=data.token")).toThrow("Invalid capture scope");
    expect(() => parseCaptureText("token=$..data.token")).toThrow("Invalid capture path");
  });
});

function requestNodeData(): FlowNodeData {
  return {
    label: "Request",
    value: "POST",
    caption: "example.com",
    kind: "request",
    tone: "source",
    url: "https://example.com?access_token=saved-url-secret",
    method: "POST",
    headersText: "Authorization: Bearer saved-header-secret",
    headersMode: "form",
    headerRows: [{ name: "X-API-Key", value: "saved-row-secret" }],
    body: "{\"password\":\"saved-body-secret\"}",
    token: "saved-direct-secret",
  };
}

function createRequest(): StartRequest {
  return {
    config: {
      url: "https://example.com",
      method: "GET",
      headers: [],
      body: "",
      virtualUsers: 1,
      durationMs: 1_000,
      requestTimeoutMs: 1_000,
      maxConnsPerHost: 10,
      readBufferSize: 4_096,
      writeBufferSize: 4_096,
      maxResponseBytes: 1_048_576,
      latencySampleRate: 1,
      rateLimitRps: 0,
      rampUpMs: 0,
      scenarioSteps: [],
    },
    batchIntervalMs: 1_000,
  };
}
