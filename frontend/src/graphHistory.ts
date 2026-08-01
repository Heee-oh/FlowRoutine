import type { Edge, Node } from "@xyflow/react";

import type { FlowNodeData, RuntimeAuthSecret } from "./flowTypes";

export type WorkspaceScenarioState = {
  activeScenarioId: string | null;
  name: string;
  tagsText: string;
  createdAtUnixMs: number;
  activeEnvironmentId: string | null;
};

export type GraphSnapshot = {
  nodes: Node<FlowNodeData>[];
  edges: Edge[];
  authSecrets: Record<string, RuntimeAuthSecret>;
  selectedNodeId: string | null;
  nextNodeIndex: number;
  scenario: WorkspaceScenarioState;
};

export type HistoryEntry<T> = {
  label: string;
  value: T;
};

export class BoundedHistory<T> {
  private readonly limit: number;
  private undoEntries: HistoryEntry<T>[] = [];
  private redoEntries: HistoryEntry<T>[] = [];

  constructor(limit = 50) {
    this.limit = Math.max(1, Math.floor(limit));
  }

  record(value: T, label: string) {
    this.undoEntries.push({ label, value });
    if (this.undoEntries.length > this.limit) {
      this.undoEntries.splice(0, this.undoEntries.length - this.limit);
    }
    this.redoEntries = [];
  }

  clearRedo() {
    const changed = this.redoEntries.length > 0;
    this.redoEntries = [];
    return changed;
  }

  undo(current: T) {
    const entry = this.undoEntries.pop();
    if (!entry) {
      return null;
    }
    this.redoEntries.push({ label: entry.label, value: current });
    return entry;
  }

  redo(current: T) {
    const entry = this.redoEntries.pop();
    if (!entry) {
      return null;
    }
    this.undoEntries.push({ label: entry.label, value: current });
    return entry;
  }

  get canUndo() {
    return this.undoEntries.length > 0;
  }

  get canRedo() {
    return this.redoEntries.length > 0;
  }

  get undoLabel() {
    return this.undoEntries[this.undoEntries.length - 1]?.label ?? "";
  }

  get redoLabel() {
    return this.redoEntries[this.redoEntries.length - 1]?.label ?? "";
  }
}

export function createGraphSnapshot(snapshot: GraphSnapshot): GraphSnapshot {
  const nodes = snapshot.nodes.map((node) => {
    const { onDelete: _onDelete, ...data } = node.data;
    return { ...node, data };
  });
  return structuredClone({
    ...snapshot,
    nodes,
  });
}
