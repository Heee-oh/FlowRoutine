import type { Edge, Node } from "@xyflow/react";
import { DEFAULT_METRIC_WINDOW_MS } from "./store";
import { formatDuration } from "./format";
import type { Capture, Header, LoadConfig, OpenAPIEndpoint, ScenarioStep, StartRequest } from "./types";
import type {
  FlowNodeData,
  FlowNodeKind,
  FlowNodeTemplate,
  HeaderRow,
  RuntimeAuthSecret,
  SafetyAssessment,
  SavedScenario,
  TargetProfile,
} from "./flowTypes";

export const nodePalette: Array<{ kind: FlowNodeKind; label: string }> = [
  { kind: "request", label: "Request" },
  { kind: "engine", label: "Engine" },
  { kind: "assertion", label: "Assert" },
  { kind: "delay", label: "Delay" },
  { kind: "metrics", label: "Metrics" },
  { kind: "window", label: "Window" },
];

export const commonHeaderNames = [
  "Accept",
  "Accept-Encoding",
  "Accept-Language",
  "Authorization",
  "Cache-Control",
  "Content-Type",
  "Cookie",
  "Origin",
  "Referer",
  "User-Agent",
  "X-Api-Key",
  "X-Request-Id",
];

const savedScenariosKey = "flowroutine:saved-scenarios";
const maxSavedScenarios = 12;

export const initialFlowEdges: Edge[] = [
  { id: "input-engine", source: "request-0", target: "engine-1" },
  { id: "engine-metrics", source: "engine-1", target: "metrics-2" },
  { id: "metrics-window", source: "metrics-2", target: "window-3" },
];

export const initialFlowNodes: Node<FlowNodeData>[] = [
  createFlowNode("request", 0, null, { x: 20, y: 80 }),
  createFlowNode("engine", 1, null, { x: 290, y: 80 }),
  createFlowNode("metrics", 2, null, { x: 560, y: 80 }),
  createFlowNode("window", 3, null, { x: 560, y: 215 }),
];

export function createFlowNode(
  kind: FlowNodeKind,
  index: number,
  settings: Partial<FlowNodeData> | null,
  position?: { x: number; y: number },
  onDelete?: (id: string) => void,
): Node<FlowNodeData> {
  const template = nodeTemplate(kind, settings);
  const data: FlowNodeData = { ...template, kind, onDelete };
  return refreshNodeDisplay({
    id: `${kind}-${index}`,
    type: "flowNode",
    position: position ?? { x: 40 + (index % 4) * 220, y: 60 + Math.floor(index / 4) * 130 },
    data,
  });
}

export function openAPIEndpointToRequestSettings(endpoint: OpenAPIEndpoint, sourceURL: string): Partial<FlowNodeData> {
  const method = endpoint.method.toUpperCase();
  return {
    url: openAPIEndpointURL(endpoint, sourceURL),
    method,
    headersText: openAPIHeadersText(method),
    ...openAPIAuthSettings(endpoint),
    body: endpoint.bodySample,
  };
}

export function getHost(url: string) {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

export function getMetricWindowMs(nodes: Node<FlowNodeData>[], edges: Edge[]) {
  const metricsNode = nodes.find((node) => node.data.kind === "metrics");
  const connectedWindowId = metricsNode
    ? edges.find((edge) => edge.source === metricsNode.id && nodes.find((node) => node.id === edge.target)?.data.kind === "window")?.target
    : undefined;
  const windowNode = nodes.find((node) => node.id === connectedWindowId) ?? nodes.find((node) => node.data.kind === "window");
  return numberValue(windowNode?.data.windowMs, DEFAULT_METRIC_WINDOW_MS);
}

export function buildStartRequestFromGraph(
  nodes: Node<FlowNodeData>[],
  edges: Edge[],
  fallback: StartRequest,
  authSecrets: Record<string, RuntimeAuthSecret>,
): StartRequest {
  const requestNode = nodes.find((node) => node.data.kind === "request");
  const engineNode = nodes.find((node) => node.data.kind === "engine");
  const metricsNode = nodes.find((node) => node.data.kind === "metrics");
  if (!requestNode) {
    throw new Error("Scenario requires a Request node");
  }
  if (!engineNode) {
    throw new Error("Scenario requires an Engine node");
  }
  const scenarioPath = pathToScenarioTarget(requestNode.id, engineNode.id, metricsNode?.id, edges, nodes);
  if (scenarioPath.length === 0) {
    throw new Error("Scenario requires a path from Request through Engine");
  }
  const nodesById = new Map(nodes.map((node) => [node.id, node]));
  const scenarioSteps = scenarioPath
    .map((id) => nodesById.get(id))
    .filter(isRunnableScenarioNode)
    .map((node) => nodeToScenarioStep(node, authSecrets))
    .filter((step): step is ScenarioStep => Boolean(step));

  return {
    config: {
      ...fallback.config,
      url: stringValue(requestNode.data.url, fallback.config.url).trim(),
      method: stringValue(requestNode.data.method, fallback.config.method),
      headers: parseHeaderText(headerTextFromNode(requestNode.data, authSecrets[requestNode.id])),
      body: stringValue(requestNode.data.body, fallback.config.body),
      virtualUsers: numberValue(engineNode.data.virtualUsers, fallback.config.virtualUsers),
      durationMs: numberValue(engineNode.data.durationMs, fallback.config.durationMs),
      requestTimeoutMs: numberValue(engineNode.data.requestTimeoutMs, fallback.config.requestTimeoutMs),
      maxConnsPerHost: numberValue(engineNode.data.maxConnsPerHost, fallback.config.maxConnsPerHost),
      rateLimitRps: numberValue(engineNode.data.rateLimitRps, fallback.config.rateLimitRps),
      rampUpMs: numberValue(engineNode.data.rampUpMs, fallback.config.rampUpMs),
      latencySampleRate: numberValue(metricsNode?.data.latencySampleRate, fallback.config.latencySampleRate),
      scenarioSteps,
    },
    batchIntervalMs: numberValue(metricsNode?.data.batchIntervalMs, fallback.batchIntervalMs),
  };
}

export function refreshNodeDisplay(node: Node<FlowNodeData>): Node<FlowNodeData> {
  const data = { ...node.data };
  switch (data.kind) {
    case "request":
      data.value = stringValue(data.method, "GET");
      data.caption = getHost(stringValue(data.url, ""));
      break;
    case "engine":
      data.value = `${numberValue(data.virtualUsers, 1)} VUs`;
      data.caption = `${numberValue(data.rateLimitRps, 0) || "unlimited"} RPS cap`;
      break;
    case "assertion":
      data.value = stringValue(data.expectedStatus, "2xx");
      data.caption = "status check";
      break;
    case "delay":
      data.value = `${numberValue(data.delayMs, 0)} ms`;
      data.caption = "think time";
      break;
    case "metrics":
      data.value = "Batched";
      data.caption = `${numberValue(data.batchIntervalMs, 150)}ms updates`;
      break;
    case "window":
      data.value = `${formatDuration(numberValue(data.windowMs, DEFAULT_METRIC_WINDOW_MS))} retained`;
      data.caption = "bounded chart data";
      break;
  }
  return { ...node, data };
}

export function loadSavedScenarios(): SavedScenario[] {
  if (typeof window === "undefined") {
    return [];
  }
  try {
    const raw = window.localStorage.getItem(savedScenariosKey);
    const parsed: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed.filter(isSavedScenario).slice(0, maxSavedScenarios) : [];
  } catch {
    return [];
  }
}

export function saveScenario(scenario: SavedScenario) {
  const scenarios = [scenario, ...loadSavedScenarios().filter((item) => item.id !== scenario.id)].slice(0, maxSavedScenarios);
  if (typeof window !== "undefined") {
    try {
      window.localStorage.setItem(savedScenariosKey, JSON.stringify(scenarios));
    } catch {
      // Running should not depend on history persistence.
    }
  }
  return scenarios;
}

export function createSavedScenario(nodes: Node<FlowNodeData>[], edges: Edge[], request: StartRequest): SavedScenario {
  const method = request.config.method || "GET";
  const target = getHost(request.config.url);
  return {
    id: `${Date.now()}`,
    name: `${method} ${target}`,
    savedAtUnixMs: Date.now(),
    nodes: nodes.map(stripRuntimeNode),
    edges: edges.map(stripRuntimeEdge),
  };
}

export function reviveSavedNodes(nodes: Node<FlowNodeData>[], onDelete: (id: string) => void) {
  return nodes.map((node) => refreshNodeDisplay({
    ...node,
    data: {
      ...node.data,
      onDelete,
    },
  }));
}

export function nextNodeIndexFromNodes(nodes: Node<FlowNodeData>[]) {
  return nodes.reduce((maxIndex, node) => {
    const splitAt = node.id.lastIndexOf("-");
    const index = splitAt >= 0 ? Number(node.id.slice(splitAt + 1)) : Number.NaN;
    return Number.isInteger(index) ? Math.max(maxIndex, index + 1) : maxIndex;
  }, nodes.length);
}

export function assessStartSafety(config: LoadConfig): SafetyAssessment {
  const target = classifyTarget(config.url);
  const warnings: string[] = [];
  if (target.tone === "public") {
    warnings.push("Public target traffic leaves this machine.");
  }
  if (target.tone === "invalid") {
    warnings.push("Target URL is invalid.");
  }
  if (config.rateLimitRps === 0) {
    warnings.push("RPS is unlimited.");
  }
  if (config.durationMs > 300_000) {
    warnings.push("Run duration is longer than 5 minutes.");
  }
  if (config.virtualUsers >= 1_000) {
    warnings.push("Virtual users are set to 1,000 or more.");
  }

  return {
    target,
    warnings,
    confirmationRequired: warnings.length > 0,
  };
}

export function classifyTarget(rawUrl: string): TargetProfile {
  try {
    const url = new URL(rawUrl);
    const host = url.hostname.toLowerCase();
    if (isLocalHost(host)) {
      return { label: "Localhost", tone: "local", host: url.host };
    }
    if (isPrivateHost(host)) {
      return { label: "Private", tone: "private", host: url.host };
    }
    return { label: "Public", tone: "public", host: url.host };
  } catch {
    return { label: "Invalid", tone: "invalid", host: rawUrl || "No target" };
  }
}

export function toNumber(value: string, fallback: number) {
  const next = Number(value);
  return Number.isFinite(next) ? next : fallback;
}

export function numberValue(value: unknown, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

export function stringValue(value: unknown, fallback: string) {
  return typeof value === "string" ? value : fallback;
}

export function headerRowsFromNode(data: FlowNodeData) {
  if (Array.isArray(data.headerRows)) {
    return data.headerRows.filter(isHeaderRow);
  }
  return parseHeaderRows(stringValue(data.headersText, ""));
}

export function formatHeaderRows(rows: HeaderRow[]) {
  return rows
    .map((row) => ({ name: row.name.trim(), value: row.value.trim() }))
    .filter((row) => row.name)
    .map((row) => `${row.name}: ${row.value}`)
    .join("\n");
}

function nodeTemplate(kind: FlowNodeKind, settings: Partial<FlowNodeData> | null): FlowNodeTemplate {
  switch (kind) {
    case "request":
      return {
        label: "Request",
        value: settings?.method ?? "GET",
        caption: settings?.url ? getHost(settings.url) : "127.0.0.1:8080",
        tone: "source",
        url: settings?.url ?? "http://127.0.0.1:8080",
        method: settings?.method ?? "GET",
        headersText: settings?.headersText ?? "Content-Type: application/json",
        authType: "none",
        authCookieName: "session",
        authApiKeyName: "X-Api-Key",
        body: settings?.body ?? "",
        capturesText: settings?.capturesText ?? "",
      };
    case "engine":
      return {
        label: "Engine",
        value: `${settings?.virtualUsers ?? 128} VUs`,
        caption: "fasthttp keep-alive pool",
        tone: "engine",
        virtualUsers: settings?.virtualUsers ?? 128,
        durationMs: settings?.durationMs ?? 10_000,
        requestTimeoutMs: settings?.requestTimeoutMs ?? 1_000,
        maxConnsPerHost: settings?.maxConnsPerHost ?? 10_000,
        rateLimitRps: settings?.rateLimitRps ?? 1_000,
        rampUpMs: settings?.rampUpMs ?? 1_000,
      };
    case "assertion":
      return {
        label: "Assert",
        value: "2xx",
        caption: "status check",
        tone: "assertion",
        expectedStatus: "2xx",
      };
    case "delay":
      return {
        label: "Delay",
        value: "100 ms",
        caption: "think time",
        tone: "delay",
        delayMs: 100,
      };
    case "metrics":
      return {
        label: "Metrics",
        value: "Batched",
        caption: `${settings?.batchIntervalMs ?? 150}ms updates`,
        tone: "metrics",
        batchIntervalMs: settings?.batchIntervalMs ?? 150,
        latencySampleRate: settings?.latencySampleRate ?? 1,
      };
    case "window":
      return {
        label: "Window",
        value: `${formatDuration(DEFAULT_METRIC_WINDOW_MS)} retained`,
        caption: "bounded chart data",
        tone: "window",
        windowMs: DEFAULT_METRIC_WINDOW_MS,
      };
  }
}

function openAPIEndpointURL(endpoint: OpenAPIEndpoint, sourceURL: string) {
  const serverURL = endpoint.serverUrl || sourceURL;
  const path = openAPIPathWithSamples(endpoint);
  try {
    const base = new URL(serverURL, sourceURL);
    const url = new URL(path.replace(/^\/+/, ""), normalizedBaseURL(base));
    for (const parameter of endpoint.parameters ?? []) {
      if (parameter.in === "query" && parameter.name) {
        url.searchParams.set(parameter.name, parameter.sample || "string");
      }
    }
    return url.toString();
  } catch {
    const query = openAPIQueryString(endpoint);
    return `${serverURL.replace(/\/+$/, "")}/${path.replace(/^\/+/, "")}${query}`;
  }
}

function openAPIPathWithSamples(endpoint: OpenAPIEndpoint) {
  const pathParameters = new Map(
    (endpoint.parameters ?? [])
      .filter((parameter) => parameter.in === "path")
      .map((parameter) => [parameter.name, parameter.sample || "1"]),
  );
  return endpoint.path.replace(/\{([^}]+)\}/g, (_match, name: string) => encodeURIComponent(pathParameters.get(name) ?? "1"));
}

function openAPIQueryString(endpoint: OpenAPIEndpoint) {
  const params = new URLSearchParams();
  for (const parameter of endpoint.parameters ?? []) {
    if (parameter.in === "query" && parameter.name) {
      params.set(parameter.name, parameter.sample || "string");
    }
  }
  const query = params.toString();
  return query ? `?${query}` : "";
}

function normalizedBaseURL(url: URL) {
  if (!url.pathname.endsWith("/")) {
    url.pathname = `${url.pathname}/`;
  }
  return url;
}

function openAPIHeadersText(method: string) {
  const headers = ["Accept: application/json"];
  if (method !== "GET" && method !== "HEAD") {
    headers.push("Content-Type: application/json");
  }
  return headers.join("\n");
}

function openAPIAuthSettings(endpoint: OpenAPIEndpoint): Partial<FlowNodeData> {
  switch (endpoint.auth.type) {
    case "bearer":
      return { authType: "bearer" };
    case "apiKey":
      return { authType: "apiKey", authApiKeyName: endpoint.auth.name || "X-Api-Key" };
    case "cookie":
      return { authType: "cookie", authCookieName: endpoint.auth.name || "session" };
    default:
      return { authType: "none" };
  }
}

function isRunnableScenarioNode(node: Node<FlowNodeData> | undefined): node is Node<FlowNodeData> {
  return node !== undefined && node.data.kind !== "engine";
}

function pathToScenarioTarget(
  source: string,
  requiredEngine: string,
  metricsTarget: string | undefined,
  edges: Edge[],
  nodes: Node<FlowNodeData>[],
) {
  const target = metricsTarget ?? requiredEngine;
  const path = bestPathTo(source, target, edges, nodes, requiredEngine);
  if (path.length > 0) {
    return path;
  }
  return bestPathTo(source, requiredEngine, edges, nodes);
}

function bestPathTo(source: string, target: string, edges: Edge[], nodes: Node<FlowNodeData>[], requiredNode?: string) {
  const nextBySource = new Map<string, string[]>();
  for (const edge of edges) {
    const next = nextBySource.get(edge.source) ?? [];
    next.push(edge.target);
    nextBySource.set(edge.source, next);
  }

  const nodesById = new Map(nodes.map((node) => [node.id, node]));
  let bestPath: string[] = [];
  let bestScore = -1;
  const visit = (id: string, path: string[]) => {
    if (id === target) {
      if (requiredNode && !path.includes(requiredNode)) {
        return;
      }
      const score = path.reduce((total, nodeId) => total + scenarioPathScore(nodesById.get(nodeId)), 0);
      if (score > bestScore || (score === bestScore && path.length > bestPath.length)) {
        bestScore = score;
        bestPath = path;
      }
      return;
    }
    for (const next of nextBySource.get(id) ?? []) {
      if (!path.includes(next)) {
        visit(next, path.concat(next));
      }
    }
  };
  visit(source, [source]);
  return bestPath;
}

function scenarioPathScore(node: Node<FlowNodeData> | undefined) {
  if (!node) {
    return 0;
  }
  return node.data.kind === "request" || node.data.kind === "delay" || node.data.kind === "assertion" ? 1 : 0;
}

function nodeToScenarioStep(node: Node<FlowNodeData>, authSecrets: Record<string, RuntimeAuthSecret>): ScenarioStep | null {
  switch (node.data.kind) {
    case "request":
      return {
        kind: "request",
        url: stringValue(node.data.url, ""),
        method: stringValue(node.data.method, "GET"),
        headers: parseHeaderText(headerTextFromNode(node.data, authSecrets[node.id])),
        body: stringValue(node.data.body, ""),
        captures: parseCaptureText(stringValue(node.data.capturesText, "")),
      };
    case "delay":
      return {
        kind: "delay",
        delayMs: numberValue(node.data.delayMs, 0),
      };
    case "assertion":
      return {
        kind: "assertStatus",
        expectedStatus: stringValue(node.data.expectedStatus, "2xx"),
      };
    default:
      return null;
  }
}

function headerTextFromNode(data: FlowNodeData, authSecret?: RuntimeAuthSecret) {
  const authRows = authHeaderRowsFromNode(data, authSecret);
  if (data.headersMode === "form") {
    return formatHeaderRows(headerRowsFromNode(data).concat(authRows));
  }
  return [stringValue(data.headersText, ""), formatHeaderRows(authRows)].filter(Boolean).join("\n");
}

function authHeaderRowsFromNode(data: FlowNodeData, authSecret?: RuntimeAuthSecret): HeaderRow[] {
  switch (data.authType) {
    case "bearer":
      return authSecret?.token ? [{ name: "Authorization", value: `Bearer ${authSecret.token}` }] : [];
    case "cookie": {
      const cookieName = stringValue(data.authCookieName, "session").trim();
      return cookieName && authSecret?.cookieValue ? [{ name: "Cookie", value: `${cookieName}=${authSecret.cookieValue}` }] : [];
    }
    case "apiKey": {
      const headerName = stringValue(data.authApiKeyName, "X-Api-Key").trim();
      return headerName && authSecret?.apiKeyValue ? [{ name: headerName, value: authSecret.apiKeyValue }] : [];
    }
    default:
      return [];
  }
}

function parseCaptureText(raw: string): Capture[] {
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const splitAt = line.indexOf("=");
      if (splitAt < 1) {
        throw new Error(`Invalid capture: ${line}`);
      }
      return {
        name: line.slice(0, splitAt).trim(),
        path: line.slice(splitAt + 1).trim(),
      };
    })
    .filter((capture) => capture.name && capture.path);
}

function parseHeaderRows(raw: string): HeaderRow[] {
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const splitAt = line.indexOf(":");
      if (splitAt < 1) {
        return { name: line, value: "" };
      }
      return {
        name: line.slice(0, splitAt).trim(),
        value: line.slice(splitAt + 1).trim(),
      };
    });
}

function parseHeaderText(raw: string): Header[] {
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const splitAt = line.indexOf(":");
      if (splitAt < 1) {
        throw new Error(`Invalid header: ${line}`);
      }
      return {
        name: line.slice(0, splitAt).trim(),
        value: line.slice(splitAt + 1).trim(),
      };
    });
}

function stripRuntimeNode(node: Node<FlowNodeData>): Node<FlowNodeData> {
  const { onDelete: _onDelete, ...data } = node.data;
  return {
    id: node.id,
    type: node.type,
    position: node.position,
    data: stripSecretFields(data),
  };
}

function stripSecretFields(data: FlowNodeData): FlowNodeData {
  const {
    token: _token,
    cookieValue: _cookieValue,
    apiKeyValue: _apiKeyValue,
    ...safeData
  } = data;
  return safeData;
}

function stripRuntimeEdge(edge: Edge): Edge {
  return {
    id: edge.id,
    source: edge.source,
    target: edge.target,
    sourceHandle: edge.sourceHandle,
    targetHandle: edge.targetHandle,
  };
}

function isSavedScenario(value: unknown): value is SavedScenario {
  if (!value || typeof value !== "object") {
    return false;
  }
  const candidate = value as SavedScenario;
  return typeof candidate.id === "string" &&
    typeof candidate.name === "string" &&
    typeof candidate.savedAtUnixMs === "number" &&
    Array.isArray(candidate.nodes) &&
    Array.isArray(candidate.edges);
}

function isHeaderRow(value: unknown): value is HeaderRow {
  return Boolean(value) &&
    typeof value === "object" &&
    typeof (value as HeaderRow).name === "string" &&
    typeof (value as HeaderRow).value === "string";
}

function isLocalHost(host: string) {
  return host === "localhost" || host === "127.0.0.1" || host === "::1" || host.endsWith(".localhost");
}

function isPrivateHost(host: string) {
  const parts = host.split(".").map((part) => Number(part));
  if (parts.length === 4 && parts.every((part) => Number.isInteger(part) && part >= 0 && part <= 255)) {
    return parts[0] === 10 ||
      (parts[0] === 172 && parts[1] >= 16 && parts[1] <= 31) ||
      (parts[0] === 192 && parts[1] === 168) ||
      (parts[0] === 169 && parts[1] === 254);
  }
  return host.endsWith(".local") || host.startsWith("fc") || host.startsWith("fd");
}
