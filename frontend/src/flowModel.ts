import type { Edge, Node } from "@xyflow/react";
import { normalizeCaptureDefinition, normalizeCaptureScope } from "./captureValidation";
import { DEFAULT_METRIC_BATCH_INTERVAL_MS, DEFAULT_METRIC_WINDOW_MS } from "./store";
import { formatDuration } from "./format";
import { compileScenarioGraph, type CompiledScenarioGraph } from "./graphCompiler";
import { isArrivalMode, legacyLoadProfile, loadProfileSummary } from "./loadProfile";
import {
  environmentVariableBindings,
  resolveEnvironmentPlaceholders,
} from "./environmentProfiles";
import {
  resolveSecretPlaceholders,
  sanitizeHeaderRows,
  sanitizeHeaderText,
  sanitizeSensitiveURL,
  sanitizeStructuredBody,
} from "./secretSanitization";
import type { AssertionDefinition, Capture, Header, LoadConfig, OpenAPIEndpoint, PreflightResponse, ScenarioStep, StartRequest } from "./types";
import type {
  FlowNodeData,
  FlowNodeKind,
  FlowNodeTemplate,
  HeaderRow,
  RuntimeEnvironment,
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
  { kind: "branch", label: "Branch" },
  { kind: "join", label: "Join" },
  { kind: "loop", label: "Loop" },
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

const scenarioStepNameBytes = 256;
const textEncoder = new TextEncoder();

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
    url: sanitizeSensitiveURL(openAPIEndpointURL(endpoint, sourceURL)),
    method,
    headersText: openAPIHeadersText(method),
    ...openAPIAuthSettings(endpoint),
    body: sanitizeStructuredBody(endpoint.bodySample),
  };
}

export function getHost(url: string) {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

export function getMetricWindowMs(
  nodes: Node<FlowNodeData>[],
  compiledGraph: CompiledScenarioGraph | null,
) {
  const windowNode = compiledGraph?.windowNodeId
    ? nodes.find((node) => node.id === compiledGraph.windowNodeId)
    : undefined;
  return numberValue(windowNode?.data.windowMs, DEFAULT_METRIC_WINDOW_MS);
}

export function buildStartRequestFromGraph(
  nodes: Node<FlowNodeData>[],
  edges: Edge[],
  fallback: StartRequest,
  authSecrets: Record<string, RuntimeAuthSecret>,
  runtimeEnvironment?: RuntimeEnvironment,
): StartRequest {
  const compiled = compileScenarioGraph(nodes, edges);
  const nodesById = new Map(nodes.map((node) => [node.id, node]));
  const requestNode = nodesById.get(compiled.requestNodeId);
  const engineNode = nodesById.get(compiled.engineNodeId);
  const metricsNode = compiled.metricsNodeId ? nodesById.get(compiled.metricsNodeId) : undefined;
  if (!requestNode || !engineNode) {
    throw new Error("Scenario graph changed after validation; validate it again before starting.");
  }
  const captureNames = scenarioCaptureNames(compiled, nodesById);
  const environmentBindings = environmentVariableBindings(runtimeEnvironment?.profile, captureNames);
  const scenarioSteps = compiled.path
    .map((id) => nodesById.get(id))
    .filter(isRunnableScenarioNode)
    .map((node) => nodeToScenarioStep(
      node,
      authSecrets,
      environmentBindings,
      captureNames,
      runtimeEnvironment,
    ))
    .filter((step): step is ScenarioStep => Boolean(step));
  const loadProfile = engineNode.data.loadProfile ?? legacyLoadProfile({
    virtualUsers: numberValue(engineNode.data.virtualUsers, fallback.config.virtualUsers),
    durationMs: numberValue(engineNode.data.durationMs, fallback.config.durationMs),
    rampUpMs: numberValue(engineNode.data.rampUpMs, fallback.config.rampUpMs),
    requestTimeoutMs: numberValue(engineNode.data.requestTimeoutMs, fallback.config.requestTimeoutMs),
  });
  const profileSummary = loadProfileSummary(loadProfile);
  const secretValues = collectRuntimeSecretValues(authSecrets, runtimeEnvironment);

  return {
    config: {
      ...fallback.config,
      url: resolveNodeTemplates(
        requestNode,
        stringValue(requestNode.data.url, fallback.config.url).trim(),
        authSecrets,
        environmentBindings,
        captureNames,
        runtimeEnvironment,
      ),
      method: stringValue(requestNode.data.method, fallback.config.method),
      headers: parseHeaderText(resolveNodeTemplates(
        requestNode,
        headerTextFromNode(requestNode.data, authSecrets[requestNode.id]),
        authSecrets,
        environmentBindings,
        captureNames,
        runtimeEnvironment,
      )),
      body: resolveNodeTemplates(
        requestNode,
        stringValue(requestNode.data.body, fallback.config.body),
        authSecrets,
        environmentBindings,
        captureNames,
        runtimeEnvironment,
      ),
      virtualUsers: profileSummary.maxVirtualUsers,
      durationMs: profileSummary.durationMs,
      requestTimeoutMs: numberValue(engineNode.data.requestTimeoutMs, fallback.config.requestTimeoutMs),
      maxConnsPerHost: numberValue(engineNode.data.maxConnsPerHost, fallback.config.maxConnsPerHost),
      readBufferSize: numberValue(engineNode.data.readBufferSize, fallback.config.readBufferSize),
      writeBufferSize: numberValue(engineNode.data.writeBufferSize, fallback.config.writeBufferSize),
      maxResponseBytes: numberValue(engineNode.data.maxResponseBytes, fallback.config.maxResponseBytes),
      rateLimitRps: isArrivalMode(profileSummary.profile.mode)
        ? 0
        : numberValue(engineNode.data.rateLimitRps, fallback.config.rateLimitRps),
      rampUpMs: 0,
      profile: profileSummary.profile,
      latencySampleRate: numberValue(metricsNode?.data.latencySampleRate, fallback.config.latencySampleRate),
      scenarioSteps,
      executionPlan: compiled.executionPlan ? {
        ...compiled.executionPlan,
        steps: compiled.executionPlan.steps.map((step) => ({
          ...step,
          routes: step.routes?.map((route) => ({
            ...route,
            name: route.name ? sanitizeSensitiveURL(route.name) : route.name,
          })),
        })),
      } : undefined,
    },
    batchIntervalMs: numberValue(metricsNode?.data.batchIntervalMs, fallback.batchIntervalMs),
    qualityGate: qualityGateFromMetricsNode(metricsNode),
    ...(secretValues.length > 0 ? { runtimeSecretValues: secretValues } : {}),
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
      if (data.loadProfile) {
        try {
          const summary = loadProfileSummary(data.loadProfile);
          data.value = `${summary.peakTarget} ${summary.targetUnit}`;
          data.caption = `${summary.profile.stages.length} stage${summary.profile.stages.length === 1 ? "" : "s"} · ${formatDuration(summary.durationMs)}`;
        } catch {
          data.value = "Invalid profile";
          data.caption = "Fix load profile settings";
        }
      } else {
        data.value = `${numberValue(data.virtualUsers, 1)} VUs`;
        data.caption = `${numberValue(data.rateLimitRps, 0) || "unlimited"} RPS cap`;
      }
      break;
    case "assertion":
      ({ value: data.value, caption: data.caption } = assertionDisplay(data));
      break;
    case "delay":
      data.value = `${numberValue(data.delayMs, 0)} ms`;
      data.caption = "think time";
      break;
    case "branch":
      data.value = stringValue(data.branchJoinNodeId, "Set join ID") || "Set join ID";
      data.caption = stringValue(data.branchRoutesText, "").trim() ? "weighted routes" : "equal route weights";
      break;
    case "join":
      data.value = "Merge";
      data.caption = "isolated branch scopes end";
      break;
    case "loop":
      data.value = `${numberValue(data.loopMaxIterations, 1)} iterations`;
      data.caption = stringValue(data.loopBodyTargetId, "Set body target") || "Set body target";
      break;
    case "metrics":
      data.value = gateCaption(data);
      data.caption = `${numberValue(data.batchIntervalMs, DEFAULT_METRIC_BATCH_INTERVAL_MS)}ms updates`;
      break;
    case "window":
      data.value = `${formatDuration(numberValue(data.windowMs, DEFAULT_METRIC_WINDOW_MS))} retained`;
      data.caption = "bounded chart data";
      break;
  }
  return { ...node, data };
}

function gateCaption(data: FlowNodeData) {
  const maxFailureRatePct = numberValue(data.maxFailureRatePct, 1);
  const maxP95LatencyMs = numberValue(data.maxP95LatencyMs, 500);
  const maxP99LatencyMs = numberValue(data.maxP99LatencyMs, 1000);
  const minRps = numberValue(data.minRps, 0);
  if (maxFailureRatePct > 0 || maxP95LatencyMs > 0 || maxP99LatencyMs > 0 || minRps > 0) {
    return "SLO gated";
  }
  return "Batched";
}

function qualityGateFromMetricsNode(metricsNode: Node<FlowNodeData> | undefined) {
  const data = metricsNode?.data;
  return {
    maxFailureRatePct: numberValue(data?.maxFailureRatePct, 1),
    maxP95LatencyMs: numberValue(data?.maxP95LatencyMs, 500),
    maxP99LatencyMs: numberValue(data?.maxP99LatencyMs, 1000),
    minRps: numberValue(data?.minRps, 0),
  };
}

export type ScenarioSnapshotMetadata = {
  id?: string;
  name?: string;
  tags?: string[];
  createdAtUnixMs?: number;
  environmentProfileId?: string;
};

export function createSavedScenario(
  nodes: Node<FlowNodeData>[],
  edges: Edge[],
  request: StartRequest,
  environmentProfileId?: string,
  metadata?: ScenarioSnapshotMetadata,
): SavedScenario {
  const method = request.config.method || "GET";
  const target = persistedTargetName(request.config.url);
  return createScenarioSnapshot(nodes, edges, {
    ...metadata,
    name: metadata?.name?.trim() || `${method} ${target}`,
    environmentProfileId: environmentProfileId ?? metadata?.environmentProfileId,
  });
}

export function createScenarioSnapshot(
  nodes: Node<FlowNodeData>[],
  edges: Edge[],
  metadata: ScenarioSnapshotMetadata = {},
): SavedScenario {
  const now = Date.now();
  return sanitizeSavedScenario({
    schemaVersion: 2,
    id: metadata.id?.trim() || newScenarioID(now),
    name: metadata.name?.trim() || "Untitled scenario",
    tags: metadata.tags ?? [],
    createdAtUnixMs: metadata.createdAtUnixMs ?? now,
    updatedAtUnixMs: now,
    ...(metadata.environmentProfileId ? { environmentProfileId: metadata.environmentProfileId } : {}),
    nodes: nodes.map(stripRuntimeNode),
    edges: edges.map(stripRuntimeEdge),
  });
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

export function assessStartSafety(config: LoadConfig, preflight?: PreflightResponse): SafetyAssessment {
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
  if (preflight) {
    warnings.push(...preflight.warnings.map((warning) => warning.message));
  }
  const uniqueWarnings = Array.from(new Set(warnings));

  return {
    target,
    warnings: uniqueWarnings,
    confirmationRequired: uniqueWarnings.length > 0,
    estimatedMemoryBytes: preflight?.estimate.memoryBytes ?? 0,
    estimatedConnections: preflight?.estimate.connections ?? 0,
    targetHosts: preflight?.estimate.targetHosts ?? 0,
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
    case "engine": {
      const legacyProfile = legacyLoadProfile({
        virtualUsers: numberValue(settings?.virtualUsers, 128),
        durationMs: numberValue(settings?.durationMs, 10_000),
        rampUpMs: numberValue(settings?.rampUpMs, 1_000),
        requestTimeoutMs: numberValue(settings?.requestTimeoutMs, 1_000),
      });
      return {
        label: "Engine",
        value: `${settings?.virtualUsers ?? 128} VUs`,
        caption: "fasthttp keep-alive pool",
        tone: "engine",
        virtualUsers: settings?.virtualUsers ?? 128,
        durationMs: settings?.durationMs ?? 10_000,
        requestTimeoutMs: settings?.requestTimeoutMs ?? 1_000,
        maxConnsPerHost: settings?.maxConnsPerHost ?? 10_000,
        readBufferSize: settings?.readBufferSize ?? 4_096,
        writeBufferSize: settings?.writeBufferSize ?? 4_096,
        maxResponseBytes: settings?.maxResponseBytes ?? 1_048_576,
        rateLimitRps: settings?.rateLimitRps ?? 1_000,
        rampUpMs: settings?.rampUpMs ?? 1_000,
        loadProfile: settings?.loadProfile ?? legacyProfile,
      };
    }
    case "assertion":
      return {
        label: "Assert",
        value: "2xx",
        caption: "status check",
        tone: "assertion",
        expectedStatus: settings?.expectedStatus ?? "2xx",
        assertionType: settings?.assertionType ?? "status",
        assertionOperator: settings?.assertionOperator ?? "equals",
        assertionHeaderName: settings?.assertionHeaderName ?? "Content-Type",
        assertionJSONPath: settings?.assertionJSONPath ?? "$.data.id",
        assertionExpected: settings?.assertionExpected ?? "",
        assertionValueType: settings?.assertionValueType ?? "string",
        assertionMaxLatencyMs: settings?.assertionMaxLatencyMs ?? 500,
        assertionFailureMode: settings?.assertionFailureMode ?? "continue",
      };
    case "delay":
      return {
        label: "Delay",
        value: "100 ms",
        caption: "think time",
        tone: "delay",
        delayMs: 100,
      };
    case "branch":
      return {
        label: "Branch",
        value: settings?.branchJoinNodeId ?? "Set join ID",
        caption: "equal route weights",
        tone: "branch",
        branchJoinNodeId: settings?.branchJoinNodeId ?? "",
        branchRoutesText: settings?.branchRoutesText ?? "",
      };
    case "join":
      return {
        label: "Join",
        value: "Merge",
        caption: "isolated branch scopes end",
        tone: "join",
      };
    case "loop":
      return {
        label: "Loop",
        value: `${settings?.loopMaxIterations ?? 1} iterations`,
        caption: settings?.loopBodyTargetId ?? "Set body target",
        tone: "loop",
        loopBodyTargetId: settings?.loopBodyTargetId ?? "",
        loopMaxIterations: settings?.loopMaxIterations ?? 1,
      };
    case "metrics":
      return {
        label: "Metrics",
        value: "SLO gated",
        caption: `${settings?.batchIntervalMs ?? 150}ms updates`,
        tone: "metrics",
        batchIntervalMs: settings?.batchIntervalMs ?? 150,
        latencySampleRate: settings?.latencySampleRate ?? 1,
        maxFailureRatePct: settings?.maxFailureRatePct ?? 1,
        maxP95LatencyMs: settings?.maxP95LatencyMs ?? 500,
        maxP99LatencyMs: settings?.maxP99LatencyMs ?? 1000,
        minRps: settings?.minRps ?? 0,
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
  return node !== undefined && (
    node.data.kind === "request" ||
    node.data.kind === "delay" ||
    node.data.kind === "assertion"
  );
}

function nodeToScenarioStep(
  node: Node<FlowNodeData>,
  authSecrets: Record<string, RuntimeAuthSecret>,
  environmentBindings: Readonly<Record<string, string>>,
  captureNames: ReadonlySet<string>,
  runtimeEnvironment: RuntimeEnvironment | undefined,
): ScenarioStep | null {
  switch (node.data.kind) {
    case "request": {
      const rawURL = stringValue(node.data.url, "");
      const displayURL = resolveNodeTemplates(
        node,
        rawURL,
        authSecrets,
        environmentBindings,
        captureNames,
        { ...runtimeEnvironment, resolveSecrets: false },
      );
      const url = resolveNodeTemplates(
        node,
        rawURL,
        authSecrets,
        environmentBindings,
        captureNames,
        runtimeEnvironment,
      );
      const method = stringValue(node.data.method, "GET");
      return {
        id: node.id,
        name: requestStepName(method, displayURL),
        kind: "request",
        url,
        method,
        headers: parseHeaderText(resolveNodeTemplates(
          node,
          headerTextFromNode(node.data, authSecrets[node.id]),
          authSecrets,
          environmentBindings,
          captureNames,
          runtimeEnvironment,
        )),
        body: resolveNodeTemplates(
          node,
          stringValue(node.data.body, ""),
          authSecrets,
          environmentBindings,
          captureNames,
          runtimeEnvironment,
        ),
        captures: parseCaptureText(stringValue(node.data.capturesText, "")),
      };
    }
    case "delay":
      return {
        id: node.id,
        name: "Delay",
        kind: "delay",
        delayMs: numberValue(node.data.delayMs, 0),
      };
    case "assertion":
      const assertion = assertionFromNode(node.data);
      if (assertion.expected !== undefined) {
        assertion.expected = resolveNodeTemplates(
          node,
          assertion.expected,
          authSecrets,
          environmentBindings,
          captureNames,
          runtimeEnvironment,
        );
      }
      return {
        id: node.id,
        name: assertionStepName(node.data),
        kind: "assert",
        assertion,
      };
    default:
      return null;
  }
}

function assertionFromNode(data: FlowNodeData): AssertionDefinition {
  const type = data.assertionType ?? "status";
  const failureMode = data.assertionFailureMode ?? "continue";
  switch (type) {
    case "status":
      return {
        type,
        expected: stringValue(data.expectedStatus, "2xx"),
        failureMode,
      };
    case "header":
      return {
        type,
        operator: data.assertionOperator ?? "exists",
        headerName: stringValue(data.assertionHeaderName, "Content-Type"),
        expected: stringValue(data.assertionExpected, ""),
        failureMode,
      };
    case "json":
      return {
        type,
        operator: data.assertionOperator ?? "equals",
        jsonPath: stringValue(data.assertionJSONPath, "$.data.id"),
        expected: stringValue(data.assertionExpected, ""),
        valueType: data.assertionValueType ?? "string",
        failureMode,
      };
    case "responseLatency":
    case "stepLatency":
      return {
        type,
        maxLatencyMs: numberValue(data.assertionMaxLatencyMs, 500),
        failureMode,
      };
  }
}

function assertionStepName(data: FlowNodeData) {
  switch (data.assertionType ?? "status") {
    case "status":
      return "Assert status";
    case "header":
      return "Assert header";
    case "json":
      return "Assert JSON";
    case "responseLatency":
      return "Assert response latency";
    case "stepLatency":
      return "Assert step latency";
  }
}

function assertionDisplay(data: FlowNodeData) {
  const mode = data.assertionFailureMode ?? "continue";
  switch (data.assertionType ?? "status") {
    case "status":
      return { value: stringValue(data.expectedStatus, "2xx"), caption: `status · ${mode}` };
    case "header":
      return {
        value: stringValue(data.assertionHeaderName, "Content-Type"),
        caption: `header ${data.assertionOperator ?? "exists"} · ${mode}`,
      };
    case "json":
      return {
        value: stringValue(data.assertionJSONPath, "$.data.id"),
        caption: `JSON ${data.assertionOperator ?? "equals"} · ${mode}`,
      };
    case "responseLatency":
      return { value: `≤ ${numberValue(data.assertionMaxLatencyMs, 500)} ms`, caption: `response latency · ${mode}` };
    case "stepLatency":
      return { value: `≤ ${numberValue(data.assertionMaxLatencyMs, 500)} ms`, caption: `step latency · ${mode}` };
  }
}

function requestStepName(method: string, rawURL: string) {
  const safeURL = sanitizeSensitiveURL(rawURL);
  try {
    const target = new URL(safeURL);
    return truncateUTF8(`${method} ${target.host}${target.pathname}`, scenarioStepNameBytes);
  } catch {
    return truncateUTF8(`${method} ${safeURL}`, scenarioStepNameBytes);
  }
}

function truncateUTF8(value: string, maximumBytes: number) {
  if (textEncoder.encode(value).length <= maximumBytes) {
    return value;
  }
  let bytes = 0;
  let result = "";
  for (const character of value) {
    const size = textEncoder.encode(character).length;
    if (bytes + size > maximumBytes) {
      break;
    }
    bytes += size;
    result += character;
  }
  return result;
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

export function parseCaptureText(raw: string): Capture[] {
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const splitAt = line.indexOf("=");
      if (splitAt < 1) {
        throw new Error(`Invalid capture: ${line}`);
      }
      const descriptor = line.slice(0, splitAt).trim();
      const path = line.slice(splitAt + 1).trim();
      let nameAndScope = descriptor;
      let onStatus = "success";
      const statusAt = descriptor.lastIndexOf(":");
      if (statusAt >= 0) {
        nameAndScope = descriptor.slice(0, statusAt).trim();
        onStatus = descriptor.slice(statusAt + 1).trim().toLowerCase();
      }
      let name = nameAndScope;
      let scope: Capture["scope"] = "iteration";
      const scopeAt = nameAndScope.lastIndexOf("@");
      if (scopeAt >= 0) {
        name = nameAndScope.slice(0, scopeAt).trim();
        const rawScope = nameAndScope.slice(scopeAt + 1).trim().toLowerCase();
        if (!rawScope) {
          throw new Error("Invalid capture scope: (empty)");
        }
        scope = normalizeCaptureScope(rawScope);
      }
      if (!name || !path || !onStatus) {
        throw new Error(`Invalid capture: ${line}`);
      }
      return normalizeCaptureDefinition({
        name,
        path,
        scope,
        onStatus,
      });
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

export function stripRuntimeNode(node: Node<FlowNodeData>): Node<FlowNodeData> {
  const {
    executionOrder: _executionOrder,
    onDelete: _onDelete,
    validationError: _validationError,
    ...data
  } = node.data;
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
  const sanitized: FlowNodeData = { ...safeData };
  sanitized.label = sanitizeSensitiveURL(sanitized.label);
  sanitized.caption = sanitizeSensitiveURL(sanitized.caption);
  if (typeof sanitized.url === "string") {
    sanitized.url = sanitizeSensitiveURL(sanitized.url);
  }
  if (typeof sanitized.headersText === "string") {
    sanitized.headersText = sanitizeHeaderText(sanitized.headersText);
  }
  if (Array.isArray(sanitized.headerRows)) {
    sanitized.headerRows = sanitizeHeaderRows(sanitized.headerRows.filter(isHeaderRow));
  }
  if (typeof sanitized.body === "string") {
    sanitized.body = sanitizeStructuredBody(sanitized.body);
  }
  return sanitized;
}

export function sanitizeSavedScenario(scenario: SavedScenario): SavedScenario {
  const environmentProfileId = typeof scenario.environmentProfileId === "string"
    ? scenario.environmentProfileId.trim()
    : "";
  return {
    schemaVersion: 2,
    id: scenario.id,
    name: sanitizeSensitiveURL(scenario.name),
    tags: scenario.tags.slice(),
    createdAtUnixMs: scenario.createdAtUnixMs,
    updatedAtUnixMs: scenario.updatedAtUnixMs,
    ...(environmentProfileId ? { environmentProfileId } : {}),
    nodes: scenario.nodes.map(stripRuntimeNode),
    edges: scenario.edges.map(stripRuntimeEdge),
  };
}

function resolveNodeTemplates(
  node: Node<FlowNodeData>,
  value: string,
  authSecrets: Record<string, RuntimeAuthSecret>,
  environmentBindings: Readonly<Record<string, string>>,
  captureNames: ReadonlySet<string>,
  runtimeEnvironment: RuntimeEnvironment | undefined,
) {
  const resolvedEnvironment = resolveEnvironmentPlaceholders(value, environmentBindings, captureNames);
  if (runtimeEnvironment?.resolveSecrets === false) {
    return resolvedEnvironment;
  }
  return resolveSecretPlaceholders(resolvedEnvironment, {
    ...runtimeEnvironment?.secretBindings,
    ...authSecrets[node.id]?.bindings,
  });
}

function scenarioCaptureNames(
  compiled: CompiledScenarioGraph,
  nodesById: ReadonlyMap<string, Node<FlowNodeData>>,
) {
  return new Set(compiled.path.flatMap((id) => {
    const node = nodesById.get(id);
    return node?.data.kind === "request"
      ? parseCaptureText(stringValue(node.data.capturesText, "")).map((capture) => capture.name)
      : [];
  }));
}

function collectRuntimeSecretValues(
  authSecrets: Readonly<Record<string, RuntimeAuthSecret>>,
  runtimeEnvironment: RuntimeEnvironment | undefined,
) {
  if (runtimeEnvironment?.resolveSecrets === false) {
    return [];
  }
  const values = new Set<string>();
  for (const value of Object.values(runtimeEnvironment?.secretBindings ?? {})) {
    if (value) {
      values.add(value);
    }
  }
  for (const secret of Object.values(authSecrets)) {
    for (const value of [
      secret.token,
      secret.cookieValue,
      secret.apiKeyValue,
      ...Object.values(secret.bindings ?? {}),
    ]) {
      if (value) {
        values.add(value);
      }
    }
  }
  return Array.from(values).sort((left, right) => right.length - left.length);
}

export function stripRuntimeEdge(edge: Edge): Edge {
  return {
    id: edge.id,
    source: edge.source,
    target: edge.target,
    sourceHandle: edge.sourceHandle,
    targetHandle: edge.targetHandle,
  };
}

function isHeaderRow(value: unknown): value is HeaderRow {
  return Boolean(value) &&
    typeof value === "object" &&
    typeof (value as HeaderRow).name === "string" &&
    typeof (value as HeaderRow).value === "string";
}

function newScenarioID(now: number) {
  return typeof globalThis.crypto?.randomUUID === "function"
    ? `scenario-${globalThis.crypto.randomUUID()}`
    : `scenario-${now}`;
}

function isLocalHost(host: string) {
  return host === "localhost" || host === "127.0.0.1" || host === "::1" || host.endsWith(".localhost");
}

function persistedTargetName(rawURL: string) {
  try {
    return new URL(rawURL).host || "target";
  } catch {
    return "custom target";
  }
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
