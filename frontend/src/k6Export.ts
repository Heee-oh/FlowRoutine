import type { Header, QualityGate, ScenarioStep, StartRequest } from "./types";

export function downloadK6Script(request: StartRequest) {
  const blob = new Blob([buildK6Script(request)], { type: "text/javascript" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = k6Filename(request.config.url);
  anchor.click();
  URL.revokeObjectURL(url);
}

export function buildK6Script(request: StartRequest) {
  const steps = scenarioSteps(request);
  const usesEnv = steps.some((step) => step.kind === "request" && (step.headers ?? []).some((header) => isSensitiveHeader(header.name)));
  const lines = [
    "import http from \"k6/http\";",
    "import { check, sleep } from \"k6\";",
    "",
    "export const options = {",
    `  vus: ${integerOption(request.config.virtualUsers, 1)},`,
    `  duration: ${JSON.stringify(formatK6Duration(request.config.durationMs))},`,
    ...renderThresholds(request.qualityGate),
    "};",
    "",
  ];

  if (usesEnv) {
    lines.push("const env = (name, fallback = \"\") => __ENV[name] || fallback;", "");
  }
  lines.push(
    "const vars = {};",
    "const render = (value) => value.replace(/\\{\\{([^}]+)\\}\\}/g, (_match, name) => vars[name] || \"\");",
    "const capture = (name, path, response) => {",
    "  const value = path.split(\".\").reduce((current, segment) => current == null ? undefined : current[segment], response.json());",
    "  if (value !== undefined && value !== null) vars[name] = String(value);",
    "};",
    "",
  );

  lines.push("export default function () {", "  let res;");
  for (const step of steps) {
    lines.push(...renderStep(step));
  }
  lines.push("}", "");
  return `${lines.join("\n")}`;
}

function renderThresholds(gate: QualityGate | undefined) {
  const thresholds: string[] = [];
  const maxFailureRatePct = nonNegative(gate?.maxFailureRatePct, 1);
  const maxP95LatencyMs = nonNegative(gate?.maxP95LatencyMs, 500);
  const maxP99LatencyMs = nonNegative(gate?.maxP99LatencyMs, 1000);
  const minRps = nonNegative(gate?.minRps, 0);
  if (maxFailureRatePct > 0) {
    thresholds.push(`    http_req_failed: [${JSON.stringify(`rate<${maxFailureRatePct / 100}`)}],`);
  }
  const latencyThresholds = [];
  if (maxP95LatencyMs > 0) {
    latencyThresholds.push(`p(95)<${maxP95LatencyMs}`);
  }
  if (maxP99LatencyMs > 0) {
    latencyThresholds.push(`p(99)<${maxP99LatencyMs}`);
  }
  if (latencyThresholds.length > 0) {
    thresholds.push(`    http_req_duration: [${latencyThresholds.map((item) => JSON.stringify(item)).join(", ")}],`);
  }
  if (minRps > 0) {
    thresholds.push(`    http_reqs: [${JSON.stringify(`rate>${minRps}`)}],`);
  }
  if (thresholds.length === 0) {
    return [];
  }
  return [
    "  thresholds: {",
    ...thresholds,
    "  },",
  ];
}

function scenarioSteps(request: StartRequest): ScenarioStep[] {
  if (request.config.scenarioSteps.length > 0) {
    return request.config.scenarioSteps;
  }
  return [{
    kind: "request",
    url: request.config.url,
    method: request.config.method,
    headers: request.config.headers,
    body: request.config.body,
  }];
}

function renderStep(step: ScenarioStep) {
  switch (step.kind) {
    case "request":
      return renderRequestStep(step);
    case "delay":
      return [`  sleep(${secondsLiteral(step.delayMs ?? 0)});`];
    case "assertStatus":
      return renderStatusCheck(step.expectedStatus ?? "2xx");
  }
}

function renderRequestStep(step: ScenarioStep) {
  const method = step.method || "GET";
  const body = step.body ? `render(${JSON.stringify(step.body)})` : "null";
  const lines = [
    `  res = http.request(${JSON.stringify(method)}, render(${JSON.stringify(step.url || "")}), ${body}, {`,
    `    headers: ${renderHeaders(step.headers ?? [], "    ")},`,
    "  });",
  ];
  for (const capture of step.captures ?? []) {
    lines.push(`  capture(${JSON.stringify(capture.name)}, ${JSON.stringify(capture.path)}, res);`);
  }
  return lines;
}

function renderHeaders(headers: Header[], baseIndent: string) {
  const activeHeaders = headers.filter((header) => header.name.trim());
  if (activeHeaders.length === 0) {
    return "{}";
  }
  const entries = activeHeaders.map((header) => {
    const value = isSensitiveHeader(header.name)
      ? `env(${JSON.stringify(envNameForHeader(header.name))})`
      : `render(${JSON.stringify(header.value)})`;
    return `${baseIndent}  ${JSON.stringify(header.name)}: ${value},`;
  });
  return `{\n${entries.join("\n")}\n${baseIndent}}`;
}

function renderStatusCheck(expectedStatus: string) {
  const expected = expectedStatus.trim() || "2xx";
  if (/^[1-5]xx$/.test(expected)) {
    const lower = Number(expected[0]) * 100;
    const upper = lower + 100;
    return [
      "  check(res, {",
      `    ${JSON.stringify(`status is ${expected}`)}: (response) => response.status >= ${lower} && response.status < ${upper},`,
      "  });",
    ];
  }
  if (/^\d{3}$/.test(expected)) {
    return [
      "  check(res, {",
      `    ${JSON.stringify(`status is ${expected}`)}: (response) => response.status === ${Number(expected)},`,
      "  });",
    ];
  }
  return [
    "  check(res, {",
    `    ${JSON.stringify(`status is ${expected}`)}: (response) => String(response.status) === ${JSON.stringify(expected)},`,
    "  });",
  ];
}

function formatK6Duration(valueMs: number) {
  const ms = Math.max(1, Math.floor(valueMs));
  if (ms % 60_000 === 0) {
    return `${ms / 60_000}m`;
  }
  if (ms % 1_000 === 0) {
    return `${ms / 1_000}s`;
  }
  return `${ms}ms`;
}

function secondsLiteral(valueMs: number) {
  return Number((Math.max(0, valueMs) / 1_000).toFixed(3));
}

function integerOption(value: number, fallback: number) {
  return Number.isFinite(value) && value > 0 ? Math.floor(value) : fallback;
}

function nonNegative(value: number | undefined, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) ? Math.max(0, value) : fallback;
}

function isSensitiveHeader(name: string) {
  return /authorization|cookie|token|secret|api[-_]?key/i.test(name);
}

function envNameForHeader(name: string) {
  const normalized = name.replace(/[^a-z0-9]+/gi, "_").replace(/^_+|_+$/g, "").toUpperCase();
  return normalized || "SECRET_HEADER";
}

function k6Filename(rawURL: string) {
  const host = safeHost(rawURL);
  return `flowroutine-${host}.k6.js`;
}

function safeHost(rawURL: string) {
  try {
    return new URL(rawURL).host.replace(/[^a-z0-9.-]+/gi, "-").slice(0, 64) || "scenario";
  } catch {
    return "scenario";
  }
}
