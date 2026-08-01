import { normalizeCaptureDefinition, validateCapturePath } from "./captureValidation";
import type { AssertionDefinition, Header, QualityGate, ScenarioStep, StartRequest } from "./types";
import {
  collectSecretPlaceholderNames,
  isSensitiveHeaderName,
  isSensitiveHeaderValue,
  sanitizeHeaderRows,
  sanitizeSensitiveURL,
  sanitizeStructuredBody,
  secretPlaceholder,
} from "./secretSanitization";
import { isArrivalMode, legacyLoadProfile, normalizeLoadProfile } from "./loadProfile";
import type { LoadProfile } from "./types";

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
  if (request.config.executionPlan) {
    throw new Error("k6 export does not yet support Branch, Join, or Loop execution plans");
  }
  const steps = scenarioSteps(request);
  validateK6Compatibility(request, steps);
  validateTemplateOrder(steps);
  const trackStepLatency = steps.some((step) => step.assertion?.type === "stepLatency");
  const usesStopAssertions = steps.some((step) => step.assertion?.failureMode === "stop");
  const lines = [
    ...renderParityNotes(request),
    "import http from \"k6/http\";",
    `import { check, ${usesStopAssertions ? "fail, " : ""}sleep } from \"k6\";`,
    "",
    "export const options = {",
    ...renderExecutionOptions(request),
    ...renderThresholds(request.qualityGate),
    "};",
    "",
  ];

  lines.push(
    "const runVars = {};",
    "const owns = (value, name) => Object.prototype.hasOwnProperty.call(value, name);",
    "const resolve = (name, iterationVars) => {",
    "  if (owns(iterationVars, name)) return iterationVars[name];",
    "  if (owns(runVars, name)) return runVars[name];",
    "  if (name.startsWith(\"SECRET_\") && owns(__ENV, name)) return __ENV[name];",
    "  throw new Error(`Template variable ${name} is unavailable for this iteration`);",
    "};",
    "const render = (value, iterationVars) => value.replace(/\\{\\{([^}]+)\\}\\}/g, (_match, name) => resolve(name.trim(), iterationVars));",
    "const matchesStatus = (status, policy) => policy === \"any\" ||",
    "  (policy === \"success\" && status >= 200 && status < 400) ||",
    "  (/^[1-5]xx$/.test(policy) && Math.floor(status / 100) === Number(policy[0])) ||",
    "  (/^[1-5][0-9]{2}$/.test(policy) && status === Number(policy));",
    "const pathSegments = (path) => path === \"$\" ? [] : path.replace(/^\\$\\.?/, \"\").replace(/\\[(\\d+)\\]/g, (_match, index) => `.${String(Number(index))}`).split(\".\").filter(Boolean);",
    "const pathValue = (document, path) => pathSegments(path).reduce((current, segment) => {",
    "  if (current == null) return undefined;",
    "  if (Array.isArray(current) && /^\\d+$/.test(segment)) return current[Number(segment)];",
    "  return typeof current === \"object\" && owns(current, segment) ? current[segment] : undefined;",
    "}, document);",
    "const headerValue = (response, name) => {",
    "  const entry = Object.entries(response.headers).find(([key]) => key.toLowerCase() === name.toLowerCase());",
    "  return entry ? entry[1] : undefined;",
    "};",
    "const assertJSON = (response, path, operator, expected) => {",
    "  try {",
    "    const value = pathValue(response.json(), path);",
    "    return value !== undefined && (operator === \"exists\" || value === expected);",
    "  } catch (_error) {",
    "    return false;",
    "  }",
    "};",
    "const captureAll = (captures, response, iterationVars) => {",
    "  const eligible = captures.filter(({ name, scope, onStatus }) => {",
    "    if (scope === \"run\" && owns(runVars, name)) return false;",
    "    if (matchesStatus(response.status, onStatus)) return true;",
    "    if (scope === \"iteration\") delete iterationVars[name];",
    "    return false;",
    "  });",
    "  if (eligible.length === 0) return true;",
    "  try {",
    "    const document = response.json();",
    "    const values = eligible.map(({ path }) => pathValue(document, path));",
    "    if (values.some((value) => value === undefined)) throw new Error(\"capture path not found\");",
    "    eligible.forEach(({ name, scope }, index) => {",
    "      const value = values[index];",
    "      const rendered = typeof value === \"string\" ? value : value == null ? \"\" : typeof value === \"object\" ? JSON.stringify(value) : String(value);",
    "      (scope === \"run\" ? runVars : iterationVars)[name] = rendered;",
    "    });",
    "    return true;",
    "  } catch (_error) {",
    "    eligible.forEach(({ name, scope }) => { if (scope === \"iteration\") delete iterationVars[name]; });",
    "    return false;",
    "  }",
    "};",
    "",
  );

  lines.push(
    "export default function () {",
    "  const iterationVars = {};",
    "  let res;",
    ...(trackStepLatency ? ["  let lastStepStartedAt = 0;", "  let lastStepDurationMs = 0;"] : []),
  );
  const requestTimeout = formatK6Duration(request.config.requestTimeoutMs);
  for (const step of steps) {
    lines.push(...renderStep(step, requestTimeout, trackStepLatency));
  }
  lines.push("}", "");
  return `${lines.join("\n")}`;
}

function renderParityNotes(request: StartRequest) {
  const notes = [
    "// Generated by FlowRoutine. Compatibility notes:",
    "// - k6 checks mirror FlowRoutine assertions without changing HTTP failure-rate thresholds.",
    "// - k6 samples every request; FlowRoutine sampling, connection buffers, and response-size limits have no direct equivalent.",
  ];
  if (request.config.rateLimitRps > 0) {
    notes.push(
      "// - The k6 global rps option preserves FlowRoutine request pacing on one load generator.",
      "//   k6 applies rps independently per generator in cloud or distributed runs.",
    );
  }
  if (request.config.profile) {
    notes.push("// - The selected FlowRoutine execution profile maps directly to the equivalent k6 executor.");
    if (isArrivalMode(request.config.profile.mode)) {
      notes.push("// - FlowRoutine dropped iterations correspond to k6 dropped_iterations when max VU capacity is exhausted.");
    }
  } else if (request.config.rampUpMs > 0 && request.config.virtualUsers > 1) {
    notes.push("// - ramping-vus approximates FlowRoutine's discrete per-worker startup schedule.");
  } else if (request.config.rampUpMs > 0) {
    notes.push("// - Ramp-up is omitted because FlowRoutine starts its only VU immediately.");
  }
  notes.push("");
  return notes;
}

function renderExecutionOptions(request: StartRequest) {
  const profile = executionProfile(request);
  const { rateLimitRps } = request.config;
  const scenario = [
    "  scenarios: {",
    "    flowroutine: {",
  ];
  switch (profile.mode) {
    case "constant-vus":
      scenario.push(
        "      executor: \"constant-vus\",",
        `      vus: ${profile.startTarget},`,
        `      duration: ${JSON.stringify(formatK6Duration(profile.stages[0].durationMs))},`,
      );
      break;
    case "ramping-vus":
      scenario.push(
        "      executor: \"ramping-vus\",",
        `      startVUs: ${profile.startTarget},`,
        "      stages: [",
        ...profile.stages.map((stage) =>
          `        { duration: ${JSON.stringify(formatK6Duration(stage.durationMs))}, target: ${stage.target} },`),
        "      ],",
      );
      if (request.config.profile) {
        scenario.push(`      gracefulRampDown: ${JSON.stringify(formatK6Duration(profile.gracefulStopMs))},`);
      }
      break;
    case "constant-arrival-rate":
      scenario.push(
        "      executor: \"constant-arrival-rate\",",
        `      rate: ${profile.startTarget},`,
        "      timeUnit: \"1s\",",
        `      duration: ${JSON.stringify(formatK6Duration(profile.stages[0].durationMs))},`,
        `      preAllocatedVUs: ${profile.preAllocatedVUs},`,
        `      maxVUs: ${profile.maxVUs},`,
      );
      break;
    case "ramping-arrival-rate":
      scenario.push(
        "      executor: \"ramping-arrival-rate\",",
        `      startRate: ${profile.startTarget},`,
        "      timeUnit: \"1s\",",
        "      stages: [",
        ...profile.stages.map((stage) =>
          `        { duration: ${JSON.stringify(formatK6Duration(stage.durationMs))}, target: ${stage.target} },`),
        "      ],",
        `      preAllocatedVUs: ${profile.preAllocatedVUs},`,
        `      maxVUs: ${profile.maxVUs},`,
      );
      break;
  }
  scenario.push(
    `      gracefulStop: ${JSON.stringify(formatK6Duration(profile.gracefulStopMs))},`,
    "    },",
    "  },",
  );
  if (!isArrivalMode(profile.mode) && rateLimitRps > 0) {
    scenario.push(`  rps: ${rateLimitRps},`);
  }
  return scenario;
}

function executionProfile(request: StartRequest): LoadProfile {
  return normalizeLoadProfile(request.config.profile ?? legacyLoadProfile({
    virtualUsers: request.config.virtualUsers,
    durationMs: request.config.durationMs,
    rampUpMs: request.config.rampUpMs,
    requestTimeoutMs: request.config.requestTimeoutMs,
  }));
}

function renderThresholds(gate: QualityGate | undefined) {
  const thresholds: string[] = [];
  const maxFailureRatePct = nonNegative(gate?.maxFailureRatePct, 1);
  const maxP95LatencyMs = nonNegative(gate?.maxP95LatencyMs, 500);
  const maxP99LatencyMs = nonNegative(gate?.maxP99LatencyMs, 1000);
  const minRps = nonNegative(gate?.minRps, 0);
  if (maxFailureRatePct > 0) {
    thresholds.push(`    http_req_failed: [${JSON.stringify(`rate<=${maxFailureRatePct / 100}`)}],`);
  }
  const latencyThresholds = [];
  if (maxP95LatencyMs > 0) {
    latencyThresholds.push(`p(95)<=${maxP95LatencyMs}`);
  }
  if (maxP99LatencyMs > 0) {
    latencyThresholds.push(`p(99)<=${maxP99LatencyMs}`);
  }
  if (latencyThresholds.length > 0) {
    thresholds.push(`    http_req_duration: [${latencyThresholds.map((item) => JSON.stringify(item)).join(", ")}],`);
  }
  if (minRps > 0) {
    thresholds.push(`    http_reqs: [${JSON.stringify(`rate>=${minRps}`)}],`);
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
  const steps = request.config.scenarioSteps.length > 0
    ? request.config.scenarioSteps
    : [{
        kind: "request" as const,
        url: request.config.url,
        method: request.config.method,
        headers: request.config.headers,
        body: request.config.body,
      }];
  const captureNames = new Set(
    steps.flatMap((step) => (step.captures ?? []).map((capture) => capture.name.trim())),
  );
  const preservedTemplateNames = new Set([
    ...captureNames,
    ...collectSecretPlaceholderNames(steps.flatMap((step) => [
      step.url,
      step.body,
      step.assertion?.expected,
      ...(step.headers ?? []).map((header) => header.value),
    ])),
  ]);
  return steps.map((step) => sanitizeScenarioStep(step, preservedTemplateNames));
}

function sanitizeScenarioStep(
  step: ScenarioStep,
  captureNames: ReadonlySet<string>,
): ScenarioStep {
  if (step.kind === "assert" && step.assertion) {
    const assertion = step.assertion;
    const sensitiveHeader = assertion.type === "header" && (
      isSensitiveHeaderName(assertion.headerName ?? "") || isSensitiveHeaderValue(assertion.expected ?? "")
    );
    const sensitiveJSON = assertion.type === "json" &&
      (assertion.valueType ?? "string") === "string" &&
      (isSensitiveHeaderName(assertion.jsonPath ?? "") || isSensitiveHeaderValue(assertion.expected ?? ""));
    if (
      (sensitiveHeader || sensitiveJSON) &&
      assertion.expected &&
      !isRuntimeSecretTemplate(assertion.expected)
    ) {
      return {
        ...step,
        assertion: {
          ...assertion,
          expected: secretPlaceholder(assertion.type === "header"
            ? assertion.headerName ?? "assertion"
            : assertion.jsonPath ?? "assertion"),
        },
      };
    }
    return step;
  }
  if (step.kind !== "request") {
    return step;
  }
  return {
    ...step,
    url: sanitizeSensitiveURL(step.url ?? "", captureNames),
    headers: sanitizeHeaderRows(step.headers ?? [], captureNames),
    body: sanitizeStructuredBody(step.body ?? "", captureNames),
  };
}

function isRuntimeSecretTemplate(value: string) {
  return /^\s*(?:(?:basic|bearer|digest|hawk|token)\s+)?\{\{\s*SECRET_[A-Z0-9_]+\s*\}\}\s*$/i.test(value);
}

function renderStep(step: ScenarioStep, requestTimeout: string, trackStepLatency: boolean) {
  switch (step.kind) {
    case "request":
      return renderRequestStep(step, requestTimeout, trackStepLatency);
    case "delay":
      return [`  sleep(${secondsLiteral(step.delayMs ?? 0)});`];
    case "assertStatus":
      return renderStatusCheck(step.expectedStatus ?? "2xx");
    case "assert":
      return renderAssertionCheck(step.assertion ?? { type: "status", expected: "2xx" });
  }
}

function renderRequestStep(step: ScenarioStep, requestTimeout: string, trackStepLatency: boolean) {
  const method = step.method || "GET";
  const body = step.body ? `render(${JSON.stringify(step.body)}, iterationVars)` : "null";
  const lines = [
    ...(trackStepLatency ? ["  lastStepStartedAt = Date.now();"] : []),
    `  res = http.request(${JSON.stringify(method)}, render(${JSON.stringify(step.url || "")}, iterationVars), ${body}, {`,
    `    headers: ${renderHeaders(step.headers ?? [], "    ")},`,
    `    timeout: ${JSON.stringify(requestTimeout)},`,
    "  });",
  ];
  const captures = (step.captures ?? []).map(normalizeCaptureDefinition);
  if (captures.length > 0) {
    const label = `captures: ${captures.map((capture) => capture.name).join(", ")}`;
    lines.push(
      "  check(res, {",
      `    ${JSON.stringify(label)}: (response) => captureAll(${JSON.stringify(captures)}, response, iterationVars),`,
      "  });",
    );
  }
  if (trackStepLatency) {
    lines.push("  lastStepDurationMs = Date.now() - lastStepStartedAt;");
  }
  return lines;
}

function renderHeaders(headers: Header[], baseIndent: string) {
  const activeHeaders = headers.filter((header) => header.name.trim());
  if (activeHeaders.length === 0) {
    return "{}";
  }
  const entries = activeHeaders.map((header) => {
    return `${baseIndent}  ${JSON.stringify(header.name)}: render(${JSON.stringify(header.value)}, iterationVars),`;
  });
  return `{\n${entries.join("\n")}\n${baseIndent}}`;
}

function validateTemplateOrder(steps: ScenarioStep[]) {
  const available = new Map<string, "iteration" | "run">();
  for (const [index, step] of steps.entries()) {
    if (step.kind !== "request") {
      continue;
    }
    if (templateNames(step.method ?? "").length > 0) {
      throw new Error(`Scenario step ${index + 1} method cannot contain templates`);
    }
    if (templateNames(urlAuthority(step.url ?? "")).length > 0) {
      throw new Error(`Scenario step ${index + 1} host cannot contain templates`);
    }
    for (const header of step.headers ?? []) {
      if (templateNames(header.name).length > 0) {
        throw new Error(`Scenario step ${index + 1} header names cannot contain templates`);
      }
    }
    for (const value of [
      step.url ?? "",
      step.body ?? "",
      ...(step.headers ?? []).map((header) => header.value),
    ]) {
      for (const name of templateNames(value)) {
        if (!name.startsWith("SECRET_") && !available.has(name)) {
          throw new Error(`Scenario step ${index + 1} template variable ${name} is not defined by an earlier capture`);
        }
      }
    }
    const stepNames = new Set<string>();
    for (const rawCapture of step.captures ?? []) {
      const capture = normalizeCaptureDefinition(rawCapture);
      if (stepNames.has(capture.name)) {
        throw new Error(`Capture name ${capture.name} is duplicated in scenario step ${index + 1}`);
      }
      stepNames.add(capture.name);
      const scope = capture.scope;
      const existingScope = available.get(capture.name);
      if (existingScope && existingScope !== scope) {
        throw new Error(`Capture ${capture.name} changes scope from ${existingScope} to ${scope}`);
      }
      available.set(capture.name, scope);
    }
  }
}

function validateK6Compatibility(request: StartRequest, steps: ScenarioStep[]) {
  const {
    durationMs,
    rampUpMs,
    rateLimitRps,
    requestTimeoutMs,
    virtualUsers,
  } = request.config;
  requirePositiveSafeInteger("Virtual users", virtualUsers);
  requirePositiveSafeInteger("Duration", durationMs);
  requirePositiveSafeInteger("Request timeout", requestTimeoutMs);
  requireNonNegativeSafeInteger("Ramp-up", rampUpMs);
  requireNonNegativeSafeInteger("Rate limit RPS", rateLimitRps);
  if (rampUpMs > durationMs) {
    throw new Error(
      `k6 export requires ramp-up (${rampUpMs}ms) to be no longer than duration (${durationMs}ms)`,
    );
  }
  const profile = executionProfile(request);
  if (isArrivalMode(profile.mode) && rateLimitRps > 0) {
    throw new Error("Rate limit RPS cannot be combined with an arrival-rate profile for k6 export");
  }

  let requestSteps = 0;
  for (const [index, step] of steps.entries()) {
    switch (step.kind) {
      case "request":
        requestSteps += 1;
        if (!(step.url ?? "").trim()) {
          throw new Error(`Scenario step ${index + 1} request URL is required for k6 export`);
        }
        break;
      case "delay":
        requireNonNegativeSafeInteger(`Scenario step ${index + 1} delay`, step.delayMs ?? 0);
        break;
      case "assertStatus":
        if (requestSteps === 0) {
          throw new Error(`Scenario step ${index + 1} assertion requires an earlier request step`);
        }
        if (!/^(?:[1-5]xx|[1-5][0-9]{2})$/.test((step.expectedStatus ?? "").trim())) {
          throw new Error(`Scenario step ${index + 1} expected status must be 100-599 or a class such as 2xx`);
        }
        break;
      case "assert":
        if (requestSteps === 0) {
          throw new Error(`Scenario step ${index + 1} assertion requires an earlier request step`);
        }
        validateAssertion(step.assertion, index);
        break;
      default:
        throw new Error(
          `Scenario step ${index + 1} has unsupported kind ${String((step as { kind?: unknown }).kind)}`,
        );
    }
  }
  if (requestSteps === 0) {
    throw new Error("k6 export requires at least one request step");
  }
}

function requirePositiveSafeInteger(label: string, value: number) {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`${label} must be a positive integer for k6 export`);
  }
}

function requireNonNegativeSafeInteger(label: string, value: number) {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error(`${label} must be a non-negative integer for k6 export`);
  }
}

function urlAuthority(rawURL: string) {
  return rawURL.match(/^[a-z][a-z0-9+.-]*:\/\/([^/?#]*)/i)?.[1] ?? "";
}

function templateNames(value: string) {
  const names: string[] = [];
  let offset = 0;
  while (offset < value.length) {
    const start = value.indexOf("{{", offset);
    const unexpectedClose = value.indexOf("}}", offset);
    if (start < 0) {
      if (unexpectedClose >= 0) {
        throw new Error("Template contains an unexpected closing delimiter");
      }
      break;
    }
    if (unexpectedClose >= 0 && unexpectedClose < start) {
      throw new Error("Template contains an unexpected closing delimiter");
    }
    const end = value.indexOf("}}", start + 2);
    if (end < 0) {
      throw new Error("Template contains an unclosed variable");
    }
    const name = value.slice(start + 2, end).trim();
    if (!name) {
      throw new Error("Template variable name is required");
    }
    if (!/^[a-z_][a-z0-9_.-]*$/i.test(name)) {
      throw new Error(`Invalid template variable name: ${name}`);
    }
    names.push(name);
    offset = end + 2;
  }
  return names;
}

function renderStatusCheck(expectedStatus: string) {
  const expected = expectedStatus.trim() || "2xx";
  if (/^[1-5]xx$/.test(expected)) {
    const lower = Number(expected[0]) * 100;
    const upper = lower + 100;
    return [
      "  check(res, {",
      `    ${JSON.stringify(`status is ${expected}`)}: (response) => response != null && response.status >= ${lower} && response.status < ${upper},`,
      "  });",
    ];
  }
  if (/^\d{3}$/.test(expected)) {
    return [
      "  check(res, {",
      `    ${JSON.stringify(`status is ${expected}`)}: (response) => response != null && response.status === ${Number(expected)},`,
      "  });",
    ];
  }
  return [
    "  check(res, {",
    `    ${JSON.stringify(`status is ${expected}`)}: (response) => response != null && String(response.status) === ${JSON.stringify(expected)},`,
    "  });",
  ];
}

function renderAssertionCheck(assertion: AssertionDefinition) {
  const failureMode = assertion.failureMode ?? "continue";
  const label = assertionLabel(assertion);
  const expression = assertionExpression(assertion);
  const checkLines = [
    "  check(res, {",
    `    ${JSON.stringify(label)}: (response) => ${expression},`,
    "  });",
  ];
  if (failureMode !== "stop") {
    return checkLines;
  }
  return [
    `  if (!check(res, { ${JSON.stringify(label)}: (response) => ${expression} })) {`,
    `    fail(${JSON.stringify(`FlowRoutine assertion failed: ${label}`)});`,
    "  }",
  ];
}

function assertionLabel(assertion: AssertionDefinition) {
  switch (assertion.type) {
    case "status":
      return `status is ${(assertion.expected ?? "2xx").trim()}`;
    case "header":
      return assertion.operator === "equals"
        ? `header ${assertion.headerName ?? ""} equals ${assertion.expected ?? ""}`
        : `header ${assertion.headerName ?? ""} exists`;
    case "json":
      return assertion.operator === "exists"
        ? `JSON ${assertion.jsonPath ?? ""} exists`
        : `JSON ${assertion.jsonPath ?? ""} equals ${assertion.expected ?? ""} (${assertion.valueType ?? "string"})`;
    case "responseLatency":
      return `response latency <= ${assertion.maxLatencyMs ?? 0} ms`;
    case "stepLatency":
      return `step latency <= ${assertion.maxLatencyMs ?? 0} ms`;
  }
}

function assertionExpression(assertion: AssertionDefinition) {
  switch (assertion.type) {
    case "status":
      return statusExpression((assertion.expected ?? "2xx").trim());
    case "header": {
      const value = `headerValue(response, ${JSON.stringify(assertion.headerName ?? "")})`;
      return assertion.operator === "equals"
        ? `${value} === render(${JSON.stringify(assertion.expected ?? "")}, iterationVars)`
        : `${value} !== undefined`;
    }
    case "json":
      return `assertJSON(response, ${JSON.stringify(assertion.jsonPath ?? "")}, ${JSON.stringify(assertion.operator ?? "equals")}, ${jsonExpectedLiteral(assertion)})`;
    case "responseLatency":
      return `response != null && response.timings.duration <= ${assertion.maxLatencyMs ?? 0}`;
    case "stepLatency":
      return `response != null && lastStepDurationMs <= ${assertion.maxLatencyMs ?? 0}`;
  }
}

function statusExpression(expected: string) {
  if (/^[1-5]xx$/.test(expected)) {
    const lower = Number(expected[0]) * 100;
    return `response != null && response.status >= ${lower} && response.status < ${lower + 100}`;
  }
  return `response != null && response.status === ${Number(expected)}`;
}

function jsonExpectedLiteral(assertion: AssertionDefinition) {
  switch (assertion.valueType ?? "string") {
    case "number":
      return String(Number(assertion.expected));
    case "boolean":
      return String(assertion.expected).trim().toLowerCase();
    case "null":
      return "null";
    default:
      return `render(${JSON.stringify(assertion.expected ?? "")}, iterationVars)`;
  }
}

function validateAssertion(assertion: AssertionDefinition | undefined, index: number) {
  if (!assertion) {
    throw new Error(`Scenario step ${index + 1} assertion definition is required for k6 export`);
  }
  const failureMode = assertion.failureMode ?? "continue";
  if (!(["continue", "stop", "countOnly"] as const).includes(failureMode)) {
    throw new Error(`Scenario step ${index + 1} assertion failure mode is invalid`);
  }
  switch (assertion.type) {
    case "status":
      if (!/^(?:[1-5]xx|[1-5][0-9]{2})$/.test((assertion.expected ?? "").trim())) {
        throw new Error(`Scenario step ${index + 1} expected status must be 100-599 or a class such as 2xx`);
      }
      return;
    case "header":
      if (!(assertion.headerName ?? "").trim()) {
        throw new Error(`Scenario step ${index + 1} assertion header name is required`);
      }
      if (!/^[!#$%&'*+.^_`|~0-9a-z-]+$/i.test((assertion.headerName ?? "").trim())) {
        throw new Error(`Scenario step ${index + 1} assertion header name is invalid`);
      }
      validateAssertionOperator(assertion.operator ?? "exists", index);
      return;
    case "json": {
      if (!(assertion.jsonPath ?? "").trim()) {
        throw new Error(`Scenario step ${index + 1} assertion JSON path is required`);
      }
      try {
        validateCapturePath((assertion.jsonPath ?? "").trim());
      } catch {
        throw new Error(`Scenario step ${index + 1} assertion JSON path is invalid`);
      }
      validateAssertionOperator(assertion.operator ?? "equals", index);
      if ((assertion.operator ?? "equals") === "equals") {
        const type = assertion.valueType ?? "string";
        if (type === "number") {
          const expected = (assertion.expected ?? "").trim();
          if (!expected || !Number.isFinite(Number(expected))) {
            throw new Error(`Scenario step ${index + 1} assertion JSON number must be finite`);
          }
        }
        if (type === "boolean" && !/^(?:true|false)$/i.test((assertion.expected ?? "").trim())) {
          throw new Error(`Scenario step ${index + 1} assertion JSON boolean must be true or false`);
        }
      }
      return;
    }
    case "responseLatency":
    case "stepLatency":
      requirePositiveSafeInteger(`Scenario step ${index + 1} maximum latency`, assertion.maxLatencyMs ?? 0);
      return;
    default:
      throw new Error(`Scenario step ${index + 1} assertion type is unsupported`);
  }
}

function validateAssertionOperator(operator: string, index: number) {
  if (operator !== "exists" && operator !== "equals") {
    throw new Error(`Scenario step ${index + 1} assertion operator must be exists or equals`);
  }
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

function nonNegative(value: number | undefined, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) ? Math.max(0, value) : fallback;
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
