import { describe, expect, it } from "vitest";

import { initialFlowEdges, initialFlowNodes } from "./flowModel";
import { compileScenarioGraph } from "./graphCompiler";
import {
  appendImportedRequestsToGraph,
  buildReplacementImportGraph,
  type ImportedRequest,
} from "./importGraph";

const importedRequests: ImportedRequest[] = [
  {
    name: "List tea",
    settings: { method: "GET", url: "https://api.example.com/tea" },
  },
  {
    name: "Create tea",
    settings: { method: "POST", url: "https://api.example.com/tea", body: "{}" },
  },
];

describe("request import graph operations", () => {
  it("builds a deterministic replacement graph", () => {
    const imported = buildReplacementImportGraph(importedRequests);

    expect(imported.nodes.map((node) => node.id)).toEqual([
      "request-0",
      "request-1",
      "engine-2",
      "metrics-3",
      "window-4",
    ]);
    expect(imported.nodes[0].data.label).toBe("List tea");
    expect(imported.nextNodeIndex).toBe(5);
    expect(compileScenarioGraph(imported.nodes, imported.edges).path).toEqual(
      imported.nodes.map((node) => node.id),
    );
  });

  it("appends requests immediately before Engine without mutating the current graph", () => {
    const nodesBefore = JSON.stringify(initialFlowNodes);
    const edgesBefore = JSON.stringify(initialFlowEdges);
    const imported = appendImportedRequestsToGraph(
      initialFlowNodes,
      initialFlowEdges,
      importedRequests,
      4,
    );

    expect(JSON.stringify(initialFlowNodes)).toBe(nodesBefore);
    expect(JSON.stringify(initialFlowEdges)).toBe(edgesBefore);
    expect(imported.selectedNodeId).toBe("request-4");
    expect(imported.nextNodeIndex).toBe(6);
    expect(compileScenarioGraph(imported.nodes, imported.edges).path).toEqual([
      "request-0",
      "request-4",
      "request-5",
      "engine-1",
      "metrics-2",
      "window-3",
    ]);
    expect(imported.edges).not.toContainEqual(initialFlowEdges[0]);
  });

  it("fails before changing state when the current graph has no valid insertion point", () => {
    const nodesBefore = JSON.stringify(initialFlowNodes);
    const edgesBefore = JSON.stringify(initialFlowEdges);

    expect(() => appendImportedRequestsToGraph(
      initialFlowNodes,
      [],
      importedRequests,
      4,
    )).toThrow();
    expect(JSON.stringify(initialFlowNodes)).toBe(nodesBefore);
    expect(JSON.stringify(initialFlowEdges)).toBe(edgesBefore);
  });

  it("requires at least one selected request", () => {
    expect(() => buildReplacementImportGraph([])).toThrow("Select at least one request");
  });
});
