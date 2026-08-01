import { describe, expect, it } from "vitest";

import { buildK6Script } from "./k6Export";
import type { StartRequest } from "./types";

describe("buildK6Script", () => {
  it("exports secrets as k6 environment bindings", () => {
    const request = createRequest();
    request.config.scenarioSteps = [
      {
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
      },
      {
        kind: "assert",
        assertion: {
          type: "header",
          operator: "equals",
          headerName: "Set-Cookie",
          expected: "k6-assertion-secret",
        },
      },
    ];

    const script = buildK6Script(request);

    expect(script).not.toMatch(/k6-(?:url|auth|body|assertion)-secret/);
    expect(script).toContain("__ENV[name]");
    expect(script).toContain("{{SECRET_X_AMZ_SIGNATURE}}");
    expect(script).toContain("{{SECRET_AUTHORIZATION}}");
    expect(script).toContain("{{SECRET_PASSWORD}}");
    expect(script).toContain("{{SECRET_SET_COOKIE}}");
    expect(script).toContain("kept");
  });

  it("preserves declared SECRET placeholders instead of renaming their k6 bindings", () => {
    const request = createRequest();
    request.config.scenarioSteps = [{
      kind: "request",
      method: "GET",
      url: "https://example.com/items?access_token={{SECRET_API_TOKEN}}",
      headers: [{ name: "Authorization", value: "Bearer {{SECRET_API_TOKEN}}" }],
    }];

    const script = buildK6Script(request);

    expect(script).toContain("access_token={{SECRET_API_TOKEN}}");
    expect(script).toContain("Bearer {{SECRET_API_TOKEN}}");
    expect(script).not.toContain("{{SECRET_AUTHORIZATION}}");
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
        method: "POST",
        url: "https://example.com/secure?access_token={{token}}",
        headers: [{ name: "Authorization", value: "Bearer {{token}}" }],
        body: "{\"token\":\"{{token}}\"}",
      },
    ];

    const script = buildK6Script(request);

    expect(script).toContain("const iterationVars = {}");
    expect(script).toContain("const runVars = {}");
    expect(script).toContain("captureAll");
    expect(script).toContain("\"scope\":\"iteration\"");
    expect(script).toContain("\"onStatus\":\"2xx\"");
    expect(script).toContain("access_token={{token}}");
    expect(script).toContain("Bearer {{token}}");
    expect(script).toContain("{\\\"token\\\":\\\"{{token}}\\\"}");
    expect(script).not.toContain("{{SECRET_TOKEN}}");
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

  it("maps load profiles, request timeouts, and inclusive quality thresholds", () => {
    const request = createRequest();
    request.config.virtualUsers = 10;
    request.config.durationMs = 30_000;
    request.config.requestTimeoutMs = 1_500;
    request.config.rateLimitRps = 250;
    request.config.rampUpMs = 5_000;
    request.qualityGate = {
      maxFailureRatePct: 2,
      maxP95LatencyMs: 300,
      maxP99LatencyMs: 700,
      minRps: 100,
    };

    const script = buildK6Script(request);

    expect(script).toContain("executor: \"ramping-vus\"");
    expect(script).toContain("startVUs: 1");
    expect(script).toContain("{ duration: \"5s\", target: 10 }");
    expect(script).toContain("{ duration: \"25s\", target: 10 }");
    expect(script).toContain("gracefulStop: \"1500ms\"");
    expect(script).toContain("rps: 250");
    expect(script).toContain("timeout: \"1500ms\"");
    expect(script).toContain("rate<=0.02");
    expect(script).toContain("p(95)<=300");
    expect(script).toContain("p(99)<=700");
    expect(script).toContain("rate>=100");
    expect(script).toContain("k6 applies rps independently per generator");
  });

  it("maps explicit staged VU profiles to ramping-vus", () => {
    const request = createRequest();
    request.config.virtualUsers = 20;
    request.config.durationMs = 4_000;
    request.config.profile = {
      mode: "ramping-vus",
      startTarget: 0,
      stages: [
        { durationMs: 1_000, target: 10 },
        { durationMs: 2_000, target: 20 },
        { durationMs: 1_000, target: 0 },
      ],
      preAllocatedVUs: 0,
      maxVUs: 0,
      gracefulStopMs: 750,
    };

    const script = buildK6Script(request);

    expect(script).toContain("executor: \"ramping-vus\"");
    expect(script).toContain("startVUs: 0");
    expect(script).toContain("{ duration: \"2s\", target: 20 }");
    expect(script).toContain("gracefulRampDown: \"750ms\"");
    expect(script).toContain("gracefulStop: \"750ms\"");
  });

  it("maps constant and ramping arrival-rate profiles to k6 capacity settings", () => {
    const request = createRequest();
    request.config.virtualUsers = 8;
    request.config.durationMs = 2_000;
    request.config.profile = {
      mode: "constant-arrival-rate",
      startTarget: 500,
      stages: [{ durationMs: 2_000, target: 500 }],
      preAllocatedVUs: 4,
      maxVUs: 8,
      gracefulStopMs: 1_000,
    };

    const constantScript = buildK6Script(request);
    expect(constantScript).toContain("executor: \"constant-arrival-rate\"");
    expect(constantScript).toContain("rate: 500");
    expect(constantScript).toContain("preAllocatedVUs: 4");
    expect(constantScript).toContain("maxVUs: 8");
    expect(constantScript).not.toContain("\n  rps:");

    request.config.durationMs = 3_000;
    request.config.profile = {
      ...request.config.profile,
      mode: "ramping-arrival-rate",
      startTarget: 100,
      stages: [
        { durationMs: 1_000, target: 500 },
        { durationMs: 2_000, target: 1_000 },
      ],
    };
    const rampingScript = buildK6Script(request);
    expect(rampingScript).toContain("executor: \"ramping-arrival-rate\"");
    expect(rampingScript).toContain("startRate: 100");
    expect(rampingScript).toContain("{ duration: \"2s\", target: 1000 }");
    expect(rampingScript).toContain("dropped_iterations");
  });

  it("rejects configurations that cannot be represented safely", () => {
    const request = createRequest();
    request.config.durationMs = 0;
    expect(() => buildK6Script(request)).toThrow(
      "Duration must be a positive integer for k6 export",
    );

    request.config.durationMs = 1_000;
    request.config.rampUpMs = 1_001;
    expect(() => buildK6Script(request)).toThrow(
      "k6 export requires ramp-up (1001ms) to be no longer than duration (1000ms)",
    );

    request.config.rampUpMs = 0;
    request.config.rateLimitRps = 1.5;
    expect(() => buildK6Script(request)).toThrow(
      "Rate limit RPS must be a non-negative integer for k6 export",
    );

    request.config.rateLimitRps = 0;
    request.config.scenarioSteps = [{ kind: "delay", delayMs: -1 }];
    expect(() => buildK6Script(request)).toThrow(
      "Scenario step 1 delay must be a non-negative integer for k6 export",
    );

    request.config.scenarioSteps = [{ kind: "assertStatus", expectedStatus: "2xx" }];
    expect(() => buildK6Script(request)).toThrow(
      "Scenario step 1 assertion requires an earlier request step",
    );
  });

  it("exports header, typed JSON, latency, and stop assertions as k6 checks", () => {
    const request = createRequest();
    request.config.scenarioSteps = [
      { kind: "request", method: "GET", url: "https://example.com/items" },
      {
        kind: "assert",
        assertion: {
          type: "header",
          operator: "equals",
          headerName: "Content-Type",
          expected: "application/json",
          failureMode: "continue",
        },
      },
      {
        kind: "assert",
        assertion: {
          type: "json",
          operator: "equals",
          jsonPath: "$.data.id",
          valueType: "number",
          expected: "42",
          failureMode: "countOnly",
        },
      },
      {
        kind: "assert",
        assertion: { type: "responseLatency", maxLatencyMs: 250, failureMode: "continue" },
      },
      {
        kind: "assert",
        assertion: { type: "stepLatency", maxLatencyMs: 500, failureMode: "stop" },
      },
    ];

    const script = buildK6Script(request);

    expect(script).toContain('headerValue(response, "Content-Type") === render("application/json", iterationVars)');
    expect(script).toContain('assertJSON(response, "$.data.id", "equals", 42)');
    expect(script).toContain("response.timings.duration <= 250");
    expect(script).toContain("lastStepDurationMs <= 500");
    expect(script).toContain("fail(\"FlowRoutine assertion failed: step latency <= 500 ms\")");
  });

  it("matches the execution-parity golden script", () => {
    const request = createRequest();
    request.config.virtualUsers = 4;
    request.config.durationMs = 12_000;
    request.config.requestTimeoutMs = 1_500;
    request.config.rateLimitRps = 25;
    request.config.rampUpMs = 2_000;
    request.qualityGate = {
      maxFailureRatePct: 1,
      maxP95LatencyMs: 250,
      maxP99LatencyMs: 500,
      minRps: 20,
    };
    request.config.scenarioSteps = [
      {
        kind: "request",
        method: "POST",
        url: "https://example.com/login",
        captures: [{
          name: "token",
          path: "$.data.token",
          scope: "iteration",
          onStatus: "2xx",
        }],
      },
      { kind: "delay", delayMs: 250 },
      {
        kind: "request",
        method: "POST",
        url: "https://example.com/items/{{token}}",
        headers: [{ name: "Authorization", value: "Bearer {{SECRET_AUTHORIZATION}}" }],
        body: "{\"token\":\"{{token}}\"}",
      },
      { kind: "assertStatus", expectedStatus: "201" },
    ];

    expect(buildK6Script(request)).toMatchInlineSnapshot(`
      "// Generated by FlowRoutine. Compatibility notes:
      // - k6 checks mirror FlowRoutine assertions without changing HTTP failure-rate thresholds.
      // - k6 samples every request; FlowRoutine sampling, connection buffers, and response-size limits have no direct equivalent.
      // - The k6 global rps option preserves FlowRoutine request pacing on one load generator.
      //   k6 applies rps independently per generator in cloud or distributed runs.
      // - ramping-vus approximates FlowRoutine's discrete per-worker startup schedule.

      import http from "k6/http";
      import { check, sleep } from "k6";

      export const options = {
        scenarios: {
          flowroutine: {
            executor: "ramping-vus",
            startVUs: 1,
            stages: [
              { duration: "2s", target: 4 },
              { duration: "10s", target: 4 },
            ],
            gracefulStop: "1500ms",
          },
        },
        rps: 25,
        thresholds: {
          http_req_failed: ["rate<=0.01"],
          http_req_duration: ["p(95)<=250", "p(99)<=500"],
          http_reqs: ["rate>=20"],
        },
      };

      const runVars = {};
      const owns = (value, name) => Object.prototype.hasOwnProperty.call(value, name);
      const resolve = (name, iterationVars) => {
        if (owns(iterationVars, name)) return iterationVars[name];
        if (owns(runVars, name)) return runVars[name];
        if (name.startsWith("SECRET_") && owns(__ENV, name)) return __ENV[name];
        throw new Error(\`Template variable \${name} is unavailable for this iteration\`);
      };
      const render = (value, iterationVars) => value.replace(/\\{\\{([^}]+)\\}\\}/g, (_match, name) => resolve(name.trim(), iterationVars));
      const matchesStatus = (status, policy) => policy === "any" ||
        (policy === "success" && status >= 200 && status < 400) ||
        (/^[1-5]xx$/.test(policy) && Math.floor(status / 100) === Number(policy[0])) ||
        (/^[1-5][0-9]{2}$/.test(policy) && status === Number(policy));
      const pathSegments = (path) => path === "$" ? [] : path.replace(/^\\$\\.?/, "").replace(/\\[(\\d+)\\]/g, (_match, index) => \`.\${String(Number(index))}\`).split(".").filter(Boolean);
      const pathValue = (document, path) => pathSegments(path).reduce((current, segment) => {
        if (current == null) return undefined;
        if (Array.isArray(current) && /^\\d+$/.test(segment)) return current[Number(segment)];
        return typeof current === "object" && owns(current, segment) ? current[segment] : undefined;
      }, document);
      const headerValue = (response, name) => {
        const entry = Object.entries(response.headers).find(([key]) => key.toLowerCase() === name.toLowerCase());
        return entry ? entry[1] : undefined;
      };
      const assertJSON = (response, path, operator, expected) => {
        try {
          const value = pathValue(response.json(), path);
          return value !== undefined && (operator === "exists" || value === expected);
        } catch (_error) {
          return false;
        }
      };
      const captureAll = (captures, response, iterationVars) => {
        const eligible = captures.filter(({ name, scope, onStatus }) => {
          if (scope === "run" && owns(runVars, name)) return false;
          if (matchesStatus(response.status, onStatus)) return true;
          if (scope === "iteration") delete iterationVars[name];
          return false;
        });
        if (eligible.length === 0) return true;
        try {
          const document = response.json();
          const values = eligible.map(({ path }) => pathValue(document, path));
          if (values.some((value) => value === undefined)) throw new Error("capture path not found");
          eligible.forEach(({ name, scope }, index) => {
            const value = values[index];
            const rendered = typeof value === "string" ? value : value == null ? "" : typeof value === "object" ? JSON.stringify(value) : String(value);
            (scope === "run" ? runVars : iterationVars)[name] = rendered;
          });
          return true;
        } catch (_error) {
          eligible.forEach(({ name, scope }) => { if (scope === "iteration") delete iterationVars[name]; });
          return false;
        }
      };

      export default function () {
        const iterationVars = {};
        let res;
        res = http.request("POST", render("https://example.com/login", iterationVars), null, {
          headers: {},
          timeout: "1500ms",
        });
        check(res, {
          "captures: token": (response) => captureAll([{"name":"token","path":"$.data.token","scope":"iteration","onStatus":"2xx"}], response, iterationVars),
        });
        sleep(0.25);
        res = http.request("POST", render("https://example.com/items/{{token}}", iterationVars), render("{\\"token\\":\\"{{token}}\\"}", iterationVars), {
          headers: {
            "Authorization": render("Bearer {{SECRET_AUTHORIZATION}}", iterationVars),
          },
          timeout: "1500ms",
        });
        check(res, {
          "status is 201": (response) => response != null && response.status === 201,
        });
      }
      "
    `);
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
