import type { Edge, Node } from "@xyflow/react";

import { sanitizeSavedScenario } from "./flowModel";
import type { FlowNodeData, SavedScenario, ScenarioDraft } from "./flowTypes";

export const SCENARIO_SCHEMA_VERSION = 2 as const;
export const MAX_SCENARIO_FILE_BYTES = 5 * 1024 * 1024;

const scenarioLibraryKey = "flowroutine:scenario-library:v2";
const legacyScenariosKey = "flowroutine:saved-scenarios";
const scenarioDraftKey = "flowroutine:scenario-draft:v2";
const maxScenarios = 50;
const maxTags = 12;
const maxTagLength = 32;
const maxScenarioNameLength = 120;
const maxScenarioNodes = 1_024;
const maxScenarioEdges = 2_048;

type ScenarioLibraryEnvelope = {
  schemaVersion: 2;
  scenarios: SavedScenario[];
};

type ScenarioFileEnvelope = {
  schemaVersion: 2;
  exportedAtUnixMs: number;
  scenario: SavedScenario;
};

export type ScenarioDraftInput = {
  activeScenarioId?: string;
  name: string;
  tags: string[];
  createdAtUnixMs: number;
  environmentProfileId?: string;
  nodes: Node<FlowNodeData>[];
  edges: Edge[];
};

export function loadScenarioLibrary(): SavedScenario[] {
  if (typeof window === "undefined") {
    return [];
  }
  try {
    const currentRaw = window.localStorage.getItem(scenarioLibraryKey);
    const legacyRaw = currentRaw ? null : window.localStorage.getItem(legacyScenariosKey);
    const raw = currentRaw ?? legacyRaw;
    if (!raw) {
      return [];
    }
    const parsed: unknown = JSON.parse(raw);
    const values = libraryValues(parsed);
    const scenarios = normalizeLibrary(values.flatMap((value) => {
      const migrated = migrateScenario(value);
      return migrated ? [migrated] : [];
    }));
    const persisted = persistLibrary(scenarios);
    if (legacyRaw !== null && (persisted || containsSensitiveLegacyData(raw, scenarios))) {
      try {
        window.localStorage.removeItem(legacyScenariosKey);
      } catch {
        // Return the sanitized in-memory migration when cleanup is unavailable.
      }
    }
    return scenarios;
  } catch {
    return [];
  }
}

export function saveScenario(
  scenario: SavedScenario,
  current: SavedScenario[] = loadScenarioLibrary(),
) {
  const sanitized = migrateScenarioOrThrow(scenario);
  const scenarios = normalizeLibrary([
    sanitized,
    ...current.filter((item) => item.id !== sanitized.id),
  ]);
  persistLibrary(scenarios);
  return scenarios;
}

export function deleteScenario(scenarioId: string, current: SavedScenario[]) {
  const scenarios = current.filter((scenario) => scenario.id !== scenarioId);
  persistLibrary(scenarios);
  return scenarios;
}

export function loadScenarioDraft(): ScenarioDraft | null {
  if (typeof window === "undefined") {
    return null;
  }
  try {
    const raw = window.localStorage.getItem(scenarioDraftKey);
    if (!raw) {
      return null;
    }
    const draft = migrateDraft(JSON.parse(raw));
    if (!draft) {
      window.localStorage.removeItem(scenarioDraftKey);
      return null;
    }
    persistDraft(draft);
    return draft;
  } catch {
    return null;
  }
}

export function saveScenarioDraft(input: ScenarioDraftInput): ScenarioDraft {
  const now = Date.now();
  const scenario = normalizeScenario({
    schemaVersion: SCENARIO_SCHEMA_VERSION,
    id: input.activeScenarioId || "draft",
    name: input.name || "Untitled scenario",
    tags: input.tags,
    createdAtUnixMs: input.createdAtUnixMs,
    updatedAtUnixMs: now,
    ...(input.environmentProfileId ? { environmentProfileId: input.environmentProfileId } : {}),
    nodes: input.nodes,
    edges: input.edges,
  });
  const draft: ScenarioDraft = {
    schemaVersion: SCENARIO_SCHEMA_VERSION,
    ...(input.activeScenarioId ? { activeScenarioId: input.activeScenarioId } : {}),
    name: scenario.name,
    tags: scenario.tags,
    createdAtUnixMs: scenario.createdAtUnixMs,
    updatedAtUnixMs: scenario.updatedAtUnixMs,
    ...(scenario.environmentProfileId ? { environmentProfileId: scenario.environmentProfileId } : {}),
    nodes: scenario.nodes,
    edges: scenario.edges,
  };
  persistDraft(draft);
  return draft;
}

export function serializeScenarioFile(scenario: SavedScenario) {
  const envelope: ScenarioFileEnvelope = {
    schemaVersion: SCENARIO_SCHEMA_VERSION,
    exportedAtUnixMs: Date.now(),
    scenario: migrateScenarioOrThrow(scenario),
  };
  return `${JSON.stringify(envelope, null, 2)}\n`;
}

export function parseScenarioFile(raw: string) {
  if (new TextEncoder().encode(raw).length > MAX_SCENARIO_FILE_BYTES) {
    throw new Error("Scenario file exceeds the 5 MiB limit");
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error("Scenario file is not valid JSON");
  }
  if (isRecord(parsed) && "scenario" in parsed) {
    assertSupportedSchema(parsed.schemaVersion);
    return migrateScenarioOrThrow(parsed.scenario);
  }
  return migrateScenarioOrThrow(parsed);
}

export function downloadScenarioFile(scenario: SavedScenario) {
  const blob = new Blob([serializeScenarioFile(scenario)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `${scenarioFilename(scenario.name)}.flowroutine.json`;
  anchor.click();
  URL.revokeObjectURL(url);
}

export function parseScenarioTags(raw: string) {
  return normalizeTags(raw.split(","));
}

export function formatScenarioTags(tags: readonly string[]) {
  return tags.join(", ");
}

function libraryValues(value: unknown): unknown[] {
  if (Array.isArray(value)) {
    return value;
  }
  if (!isRecord(value)) {
    return [];
  }
  assertSupportedSchema(value.schemaVersion);
  return Array.isArray(value.scenarios) ? value.scenarios : [];
}

function migrateScenario(value: unknown): SavedScenario | null {
  try {
    return migrateScenarioOrThrow(value);
  } catch {
    return null;
  }
}

function migrateScenarioOrThrow(value: unknown): SavedScenario {
  if (!isRecord(value)) {
    throw new Error("Scenario document must be an object");
  }
  assertSupportedSchema(value.schemaVersion);
  if (typeof value.id !== "string" || !value.id.trim()) {
    throw new Error("Scenario id is required");
  }
  if (typeof value.name !== "string") {
    throw new Error("Scenario name is required");
  }
  const nodes = readNodes(value.nodes);
  const edges = readEdges(value.edges);
  const legacyTimestamp = finiteTimestamp(value.savedAtUnixMs) ?? Date.now();
  const createdAtUnixMs = finiteTimestamp(value.createdAtUnixMs) ?? legacyTimestamp;
  const updatedAtUnixMs = Math.max(
    createdAtUnixMs,
    finiteTimestamp(value.updatedAtUnixMs) ?? legacyTimestamp,
  );
  return normalizeScenario({
    schemaVersion: SCENARIO_SCHEMA_VERSION,
    id: value.id,
    name: value.name,
    tags: Array.isArray(value.tags)
      ? value.tags.filter((tag): tag is string => typeof tag === "string")
      : [],
    createdAtUnixMs,
    updatedAtUnixMs,
    ...(typeof value.environmentProfileId === "string" && value.environmentProfileId.trim()
      ? { environmentProfileId: value.environmentProfileId }
      : {}),
    nodes,
    edges,
  });
}

function migrateDraft(value: unknown): ScenarioDraft | null {
  if (!isRecord(value)) {
    return null;
  }
  try {
    assertSupportedSchema(value.schemaVersion);
    const scenario = migrateScenarioOrThrow({
      ...value,
      id: typeof value.activeScenarioId === "string" && value.activeScenarioId.trim()
        ? value.activeScenarioId
        : "draft",
    });
    return {
      schemaVersion: SCENARIO_SCHEMA_VERSION,
      ...(typeof value.activeScenarioId === "string" && value.activeScenarioId.trim()
        ? { activeScenarioId: value.activeScenarioId.trim() }
        : {}),
      name: scenario.name,
      tags: scenario.tags,
      createdAtUnixMs: scenario.createdAtUnixMs,
      updatedAtUnixMs: scenario.updatedAtUnixMs,
      ...(scenario.environmentProfileId ? { environmentProfileId: scenario.environmentProfileId } : {}),
      nodes: scenario.nodes,
      edges: scenario.edges,
    };
  } catch {
    return null;
  }
}

function normalizeScenario(scenario: SavedScenario): SavedScenario {
  const name = scenario.name.trim().slice(0, maxScenarioNameLength) || "Untitled scenario";
  const createdAtUnixMs = finiteTimestamp(scenario.createdAtUnixMs) ?? Date.now();
  const updatedAtUnixMs = Math.max(
    createdAtUnixMs,
    finiteTimestamp(scenario.updatedAtUnixMs) ?? Date.now(),
  );
  return sanitizeSavedScenario({
    ...scenario,
    id: scenario.id.trim().slice(0, 128),
    name,
    tags: normalizeTags(scenario.tags),
    createdAtUnixMs,
    updatedAtUnixMs,
  });
}

function normalizeLibrary(scenarios: SavedScenario[]) {
  const unique = new Map<string, SavedScenario>();
  for (const scenario of scenarios) {
    if (!unique.has(scenario.id)) {
      unique.set(scenario.id, normalizeScenario(scenario));
    }
  }
  return Array.from(unique.values())
    .sort((left, right) => right.updatedAtUnixMs - left.updatedAtUnixMs)
    .slice(0, maxScenarios);
}

function normalizeTags(tags: readonly string[]) {
  const seen = new Set<string>();
  const normalized: string[] = [];
  for (const raw of tags) {
    const tag = raw.trim().slice(0, maxTagLength);
    const key = tag.toLocaleLowerCase();
    if (!tag || seen.has(key)) {
      continue;
    }
    seen.add(key);
    normalized.push(tag);
    if (normalized.length >= maxTags) {
      break;
    }
  }
  return normalized;
}

function persistLibrary(scenarios: SavedScenario[]) {
  if (typeof window === "undefined") {
    return false;
  }
  const envelope: ScenarioLibraryEnvelope = {
    schemaVersion: SCENARIO_SCHEMA_VERSION,
    scenarios,
  };
  try {
    window.localStorage.setItem(scenarioLibraryKey, JSON.stringify(envelope));
    return true;
  } catch {
    return false;
  }
}

function persistDraft(draft: ScenarioDraft) {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(scenarioDraftKey, JSON.stringify(draft));
  } catch {
    // Draft persistence must not block editing or execution.
  }
}

function readNodes(value: unknown): Node<FlowNodeData>[] {
  if (!Array.isArray(value) || value.length > maxScenarioNodes || !value.every(isScenarioNode)) {
    throw new Error(`Scenario nodes must be a valid array of at most ${maxScenarioNodes} nodes`);
  }
  return value;
}

function readEdges(value: unknown): Edge[] {
  if (!Array.isArray(value) || value.length > maxScenarioEdges || !value.every(isScenarioEdge)) {
    throw new Error(`Scenario edges must be a valid array of at most ${maxScenarioEdges} edges`);
  }
  return value;
}

function isScenarioNode(value: unknown): value is Node<FlowNodeData> {
  if (!isRecord(value) || typeof value.id !== "string" || !isRecord(value.position) || !isRecord(value.data)) {
    return false;
  }
  const data = value.data;
  return typeof value.position.x === "number" && Number.isFinite(value.position.x) &&
    typeof value.position.y === "number" && Number.isFinite(value.position.y) &&
    typeof data.label === "string" &&
    typeof data.value === "string" &&
    typeof data.caption === "string" &&
    typeof data.kind === "string" &&
    typeof data.tone === "string";
}

function isScenarioEdge(value: unknown): value is Edge {
  return isRecord(value) &&
    typeof value.id === "string" &&
    typeof value.source === "string" &&
    typeof value.target === "string";
}

function assertSupportedSchema(value: unknown) {
  if (value === undefined || value === 1 || value === SCENARIO_SCHEMA_VERSION) {
    return;
  }
  if (typeof value === "number" && value > SCENARIO_SCHEMA_VERSION) {
    throw new Error(`Scenario schema ${value} is newer than supported version ${SCENARIO_SCHEMA_VERSION}`);
  }
  throw new Error(`Unsupported scenario schema: ${String(value)}`);
}

function finiteTimestamp(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : null;
}

function scenarioFilename(name: string) {
  return name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 80) || "scenario";
}

function containsSensitiveLegacyData(raw: string, scenarios: SavedScenario[]) {
  return raw !== JSON.stringify(scenarios);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
