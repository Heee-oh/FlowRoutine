import { describe, expect, it } from "vitest";

import { buildK6Script } from "./k6Export";
import type { StartRequest } from "./types";

describe("buildK6Script", () => {
  it("exports secrets as k6 environment bindings", () => {
    const request = createRequest();
    request.config.scenarioSteps = [{
      kind: "request",
      method: "POST",
      url: "https://example.com/items?X-Amz-Signature=k6-url-secret",
      headers: [
        { name: "Authorization", value: "Bearer k6-auth-secret" },
        { name: "Accept", value: "application/json" },
      ],
      body: JSON.stringify({
        password: "k6-body-secret",
        safe: "kept",
      }),
    }];

    const script = buildK6Script(request);

    expect(script).not.toMatch(/k6-(?:url|auth|body)-secret/);
    expect(script).toContain("__ENV[name]");
    expect(script).toContain("{{SECRET_X_AMZ_SIGNATURE}}");
    expect(script).toContain("{{SECRET_AUTHORIZATION}}");
    expect(script).toContain("{{SECRET_PASSWORD}}");
    expect(script).toContain("kept");
  });

  it("exports iteration-safe capture and template semantics", () => {
    const request = createRequest();
    request.config.scenarioSteps = [
      {
        kind: "request",
        method: "POST",
        url: "https://example.com/login",
        captures: [{
          name: "token",
          path: "$.items[0].token",
          scope: "iteration",
          onStatus: "2xx",
        }],
      },
      {
        kind: "request",
        method: "GET",
        url: "https://example.com/secure/{{token}}",
      },
    ];

    const script = buildK6Script(request);

    expect(script).toContain("const iterationVars = {}");
    expect(script).toContain("const runVars = {}");
    expect(script).toContain("captureAll");
    expect(script).toContain("\"scope\":\"iteration\"");
    expect(script).toContain("\"onStatus\":\"2xx\"");
    expect(script).toContain("Template variable ${name} is unavailable");
    const parsable = script
      .replace(/^import .*;$/gm, "")
      .replace("export const options =", "const options =")
      .replace("export default function ()", "function scenario()");
    expect(() => new Function(parsable)).not.toThrow();
  });

  it("rejects templates without an earlier capture", () => {
    const request = createRequest();
    request.config.scenarioSteps = [{
      kind: "request",
      method: "GET",
      url: "https://example.com/secure/{{missing}}",
    }];

    expect(() => buildK6Script(request)).toThrow(
      "Scenario step 1 template variable missing is not defined by an earlier capture",
    );
  });

  it("normalizes capture policies and preserves numeric array indexes", () => {
    const request = createRequest();
    request.config.scenarioSteps = [
      {
        kind: "request",
        method: "GET",
        url: "https://example.com/login",
        captures: [{
          name: "token",
          path: "$.items.01.token",
          scope: "iteration",
          onStatus: "2XX",
        }],
      },
    ];

    const script = buildK6Script(request);

    expect(script).toContain("\"onStatus\":\"2xx\"");
    expect(script).toContain("Array.isArray(current)");
    expect(script).toContain("String(Number(index))");
    expect(() => {
      request.config.scenarioSteps[0].captures![0].path = "$..items[0].token";
      buildK6Script(request);
    }).toThrow("Invalid capture path");
  });
});

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
