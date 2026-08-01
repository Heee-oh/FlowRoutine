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
