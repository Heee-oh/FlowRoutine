import type { Edge, Node } from "@xyflow/react";
import { refreshNodeDisplay } from "./flowModel";
import {
  sanitizeHeaderRows,
  sanitizeHeaderText,
  sanitizeSensitiveURL,
  sanitizeStructuredBody,
} from "./secretSanitization";
import type { FlowNodeData, HeaderRow, SavedScenario } from "./flowTypes";
import type { StartRequest } from "./types";

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

function persistedTargetName(rawURL: string) {
  try {
    return new URL(rawURL).host || "target";
  } catch {
    return "custom target";
  }
}
