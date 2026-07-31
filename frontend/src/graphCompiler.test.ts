import { describe, expect, it } from "vitest";
import type { Edge, Node } from "@xyflow/react";

import {
  compileScenarioGraph,
  ScenarioGraphValidationError,
  validateScenarioGraph,
} from "./graphCompiler";
import type { FlowNodeData, FlowNodeKind } from "./flowTypes";

describe("validateScenarioGraph", () => {
  it("compiles one stable path regardless of node and edge array order", () => {
    const nodes = [
      flowNode("window-z", "window"),
      flowNode("request-c", "request"),
      flowNode("engine-x", "engine"),
      flowNode("request-a", "request"),
      flowNode("metrics-y", "metrics"),
      flowNode("delay-b", "delay"),
    ];
    const edges = chainEdges([
      "request-a",
      "delay-b",
      "request-c",
      "engine-x",
      "metrics-y",
      "window-z",
    ]).reverse();

    const first = validateScenarioGraph(nodes, edges);
    const second = validateScenarioGraph(nodes.slice().reverse(), edges.slice().reverse());

    expect(first.issues).toEqual([]);
    expect(first.compiled).toEqual({
      path: ["request-a", "delay-b", "request-c", "engine-x", "metrics-y", "window-z"],
      requestNodeId: "request-a",
      engineNodeId: "engine-x",
      metricsNodeId: "metrics-y",
      windowNodeId: "window-z",
    });
    expect(second.compiled).toEqual(first.compiled);
  });

  it("reports branch and merge ambiguity on the responsible nodes", () => {
    const nodes = [
      flowNode("request", "request"),
      flowNode("delay-a", "delay"),
      flowNode("delay-b", "delay"),
      flowNode("engine", "engine"),
    ];
    const validation = validateScenarioGraph(nodes, [
      edge("request", "delay-a"),
      edge("request", "delay-b"),
      edge("delay-a", "engine"),
      edge("delay-b", "engine"),
    ]);

    expect(validation.compiled).toBeNull();
    expect(validation.issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: "branch", nodeId: "request" }),
      expect.objectContaining({ code: "merge", nodeId: "engine" }),
    ]));
  });

  it("rejects cycles and disconnected required nodes with node IDs", () => {
    const nodes = [
      flowNode("request", "request"),
      flowNode("delay", "delay"),
      flowNode("engine", "engine"),
    ];
    const cyclic = validateScenarioGraph(nodes, [
      edge("request", "delay"),
      edge("delay", "request"),
    ]);

    expect(cyclic.compiled).toBeNull();
    expect(cyclic.issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: "cycle", nodeId: "request" }),
      expect.objectContaining({ code: "cycle", nodeId: "delay" }),
    ]));

    const disconnected = validateScenarioGraph([
      flowNode("request", "request"),
      flowNode("engine", "engine"),
    ], []);
    expect(disconnected.issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: "disconnected_start", nodeId: "request" }),
      expect.objectContaining({ code: "disconnected_start", nodeId: "engine" }),
    ]));
  });

  it("rejects duplicate control nodes and unsupported execution order", () => {
    const duplicateControls = validateScenarioGraph([
      flowNode("request", "request"),
      flowNode("engine-a", "engine"),
      flowNode("engine-b", "engine"),
      flowNode("metrics-a", "metrics"),
      flowNode("metrics-b", "metrics"),
    ], chainEdges(["request", "engine-a", "engine-b", "metrics-a", "metrics-b"]));
    expect(duplicateControls.issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: "multiple_engines", nodeId: "engine-a" }),
      expect.objectContaining({ code: "multiple_engines", nodeId: "engine-b" }),
      expect.objectContaining({ code: "multiple_metrics", nodeId: "metrics-a" }),
      expect.objectContaining({ code: "multiple_metrics", nodeId: "metrics-b" }),
    ]));

    const wrongOrder = validateScenarioGraph([
      flowNode("request", "request"),
      flowNode("metrics", "metrics"),
      flowNode("engine", "engine"),
    ], chainEdges(["request", "metrics", "engine"]));
    expect(wrongOrder.compiled).toBeNull();
    expect(wrongOrder.issues).toEqual(expect.arrayContaining([
      expect.objectContaining({ code: "invalid_order", nodeId: "metrics" }),
    ]));
  });

  it("uses iterative linear traversal for a large supported chain", () => {
    const delayCount = 10_000;
    const nodes: Node<FlowNodeData>[] = [flowNode("request", "request")];
    const ids = ["request"];
    for (let index = 0; index < delayCount; index += 1) {
      const id = `delay-${index}`;
      nodes.push(flowNode(id, "delay"));
      ids.push(id);
    }
    nodes.push(flowNode("engine", "engine"));
    ids.push("engine");

    const compiled = compileScenarioGraph(nodes.reverse(), chainEdges(ids).reverse());

    expect(compiled.path).toHaveLength(delayCount + 2);
    expect(compiled.path[0]).toBe("request");
    expect(compiled.path[compiled.path.length - 1]).toBe("engine");
  });

  it("throws a typed validation error for invalid graphs", () => {
    expect(() => compileScenarioGraph([], [])).toThrow(ScenarioGraphValidationError);
  });
});

const tones: Record<FlowNodeKind, FlowNodeData["tone"]> = {
  request: "source",
  engine: "engine",
  assertion: "assertion",
  delay: "delay",
  metrics: "metrics",
  window: "window",
};

function flowNode(id: string, kind: FlowNodeKind): Node<FlowNodeData> {
  return {
    id,
    type: "flowNode",
    position: { x: 0, y: 0 },
    data: {
      label: kind,
      value: id,
      caption: "",
      kind,
      tone: tones[kind],
    },
  };
}

function chainEdges(ids: string[]) {
  const edges: Edge[] = [];
  for (let index = 1; index < ids.length; index += 1) {
    edges.push(edge(ids[index - 1], ids[index]));
  }
  return edges;
}

function edge(source: string, target: string): Edge {
  return { id: `${source}-${target}`, source, target };
}
