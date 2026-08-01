import type { Edge, Node } from "@xyflow/react";

import { createFlowNode } from "./flowModel";
import type { FlowNodeData } from "./flowTypes";
import { compileScenarioGraph } from "./graphCompiler";

export type ImportedRequest = {
  name: string;
  settings: Partial<FlowNodeData>;
};

export type RequestImportMode = "append" | "replace";
export type RequestImportSource = "HAR" | "Postman";

export type RequestImportPreview = {
  id: number;
  fileName: string;
  fileSize: number;
  requests: ImportedRequest[];
  source: RequestImportSource;
};

export type ImportedGraph = {
  edges: Edge[];
  nextNodeIndex: number;
  nodes: Node<FlowNodeData>[];
  selectedNodeId: string;
};

const horizontalGap = 240;

export function buildReplacementImportGraph(
  requests: ImportedRequest[],
  onDelete?: (id: string) => void,
): ImportedGraph {
  assertSelectedRequests(requests);
  const requestNodes = createRequestNodes(requests, 0, (index) => ({
    x: 20 + index * horizontalGap,
    y: 80,
  }), onDelete);
  const engineIndex = requestNodes.length;
  const metricsIndex = engineIndex + 1;
  const windowIndex = engineIndex + 2;
  const engineNode = createFlowNode(
    "engine",
    engineIndex,
    null,
    { x: 20 + engineIndex * horizontalGap, y: 80 },
    onDelete,
  );
  const metricsNode = createFlowNode(
    "metrics",
    metricsIndex,
    null,
    { x: 20 + metricsIndex * horizontalGap, y: 80 },
    onDelete,
  );
  const windowNode = createFlowNode(
    "window",
    windowIndex,
    null,
    { x: 20 + metricsIndex * horizontalGap, y: 215 },
    onDelete,
  );
  const nodes = requestNodes.concat(engineNode, metricsNode, windowNode);

  return {
    nodes,
    edges: connectLinearNodes(nodes),
    selectedNodeId: requestNodes[0].id,
    nextNodeIndex: nodes.length,
  };
}

export function appendImportedRequestsToGraph(
  currentNodes: Node<FlowNodeData>[],
  currentEdges: Edge[],
  requests: ImportedRequest[],
  startNodeIndex: number,
  onDelete?: (id: string) => void,
): ImportedGraph {
  assertSelectedRequests(requests);
  const compiled = compileScenarioGraph(currentNodes, currentEdges);
  const enginePathIndex = compiled.path.indexOf(compiled.engineNodeId);
  const predecessorId = compiled.path[enginePathIndex - 1];
  const predecessor = currentNodes.find((node) => node.id === predecessorId);
  const engine = currentNodes.find((node) => node.id === compiled.engineNodeId);
  const replacedEdge = currentEdges.find((edge) => (
    edge.source === predecessorId && edge.target === compiled.engineNodeId
  ));
  if (!predecessor || !engine || !replacedEdge) {
    throw new Error("The current graph has no valid insertion point before Engine");
  }

  const insertX = Math.max(engine.position.x, predecessor.position.x + horizontalGap);
  const shiftBy = insertX - engine.position.x + requests.length * horizontalGap;
  const trailingNodeIds = new Set(compiled.path.slice(enginePathIndex));
  const shiftedNodes = currentNodes.map((node) => (
    trailingNodeIds.has(node.id)
      ? { ...node, position: { ...node.position, x: node.position.x + shiftBy } }
      : node
  ));
  const requestNodes = createRequestNodes(requests, startNodeIndex, (index) => ({
    x: insertX + index * horizontalGap,
    y: predecessor.position.y,
  }), onDelete);
  const insertedPath = [predecessor, ...requestNodes, engine];
  const edges = currentEdges
    .filter((edge) => edge.id !== replacedEdge.id)
    .concat(connectLinearNodes(insertedPath));

  return {
    nodes: shiftedNodes.concat(requestNodes),
    edges,
    selectedNodeId: requestNodes[0].id,
    nextNodeIndex: startNodeIndex + requestNodes.length,
  };
}

function createRequestNodes(
  requests: ImportedRequest[],
  startNodeIndex: number,
  positionForIndex: (index: number) => { x: number; y: number },
  onDelete?: (id: string) => void,
) {
  return requests.map((request, index) => {
    const node = createFlowNode(
      "request",
      startNodeIndex + index,
      request.settings,
      positionForIndex(index),
      onDelete,
    );
    return {
      ...node,
      data: {
        ...node.data,
        label: request.name.slice(0, 42) || "Request",
      },
    };
  });
}

function connectLinearNodes(nodes: Node<FlowNodeData>[]): Edge[] {
  return nodes.slice(0, -1).map((node, index) => ({
    id: `${node.id}-${nodes[index + 1].id}`,
    source: node.id,
    target: nodes[index + 1].id,
  }));
}

function assertSelectedRequests(requests: ImportedRequest[]) {
  if (requests.length === 0) {
    throw new Error("Select at least one request to import");
  }
}
