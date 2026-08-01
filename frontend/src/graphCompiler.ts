import type { Edge, Node } from "@xyflow/react";

import type { FlowNodeData, FlowNodeKind } from "./flowTypes";

export type ScenarioGraphIssueCode =
  | "duplicate_node"
  | "unknown_node_kind"
  | "dangling_edge"
  | "duplicate_edge"
  | "self_cycle"
  | "cycle"
  | "missing_request"
  | "missing_engine"
  | "multiple_engines"
  | "multiple_metrics"
  | "multiple_windows"
  | "branch"
  | "merge"
  | "disconnected_start"
  | "disconnected_end"
  | "invalid_entry"
  | "invalid_order";

export type ScenarioGraphIssue = {
  code: ScenarioGraphIssueCode;
  message: string;
  nodeId?: string;
  edgeId?: string;
};

export type CompiledScenarioGraph = {
  path: string[];
  requestNodeId: string;
  engineNodeId: string;
  metricsNodeId?: string;
  windowNodeId?: string;
};

export type ScenarioGraphValidation = {
  compiled: CompiledScenarioGraph | null;
  issues: ScenarioGraphIssue[];
};

export class ScenarioGraphValidationError extends Error {
  readonly issues: ScenarioGraphIssue[];

  constructor(issues: ScenarioGraphIssue[]) {
    super(issues[0]?.message ?? "Scenario graph is invalid");
    this.name = "ScenarioGraphValidationError";
    this.issues = issues;
  }
}

const knownKinds = new Set<FlowNodeKind>([
  "request",
  "engine",
  "assertion",
  "delay",
  "metrics",
  "window",
]);

const runnableKinds = new Set<FlowNodeKind>(["request", "assertion", "delay"]);

export function compileScenarioGraph(
  nodes: Node<FlowNodeData>[],
  edges: Edge[],
): CompiledScenarioGraph {
  const validation = validateScenarioGraph(nodes, edges);
  if (!validation.compiled) {
    throw new ScenarioGraphValidationError(validation.issues);
  }
  return validation.compiled;
}

export function validateScenarioGraph(
  nodes: Node<FlowNodeData>[],
  edges: Edge[],
): ScenarioGraphValidation {
  const issues: ScenarioGraphIssue[] = [];
  const issueKeys = new Set<string>();
  const addIssue = (issue: ScenarioGraphIssue) => {
    const key = [issue.code, issue.nodeId ?? "", issue.edgeId ?? "", issue.message].join("\u0000");
    if (!issueKeys.has(key)) {
      issueKeys.add(key);
      issues.push(issue);
    }
  };

  const nodesById = new Map<string, Node<FlowNodeData>>();
  for (const node of nodes) {
    if (nodesById.has(node.id)) {
      addIssue({
        code: "duplicate_node",
        nodeId: node.id,
        message: `Node ID ${node.id} is duplicated. Every node needs a unique ID.`,
      });
      continue;
    }
    nodesById.set(node.id, node);
    if (!knownKinds.has(node.data.kind)) {
      addIssue({
        code: "unknown_node_kind",
        nodeId: node.id,
        message: `${nodeName(node)} has unsupported kind ${String(node.data.kind)}.`,
      });
    }
  }

  const incoming = new Map<string, number>();
  const outgoing = new Map<string, string[]>();
  for (const id of nodesById.keys()) {
    incoming.set(id, 0);
    outgoing.set(id, []);
  }

  const seenEdges = new Set<string>();
  for (const edge of edges) {
    const source = nodesById.get(edge.source);
    const target = nodesById.get(edge.target);
    if (!source || !target) {
      const node = source ?? target;
      addIssue({
        code: "dangling_edge",
        nodeId: node?.id,
        edgeId: edge.id,
        message: `Edge ${edge.id || `${edge.source} -> ${edge.target}`} references a missing node.`,
      });
      continue;
    }

    const edgeKey = `${edge.source}\u0000${edge.target}`;
    if (seenEdges.has(edgeKey)) {
      addIssue({
        code: "duplicate_edge",
        nodeId: source.id,
        edgeId: edge.id,
        message: `${nodeName(source)} has a duplicate connection to ${nodeName(target)}.`,
      });
      continue;
    }
    seenEdges.add(edgeKey);
    outgoing.get(source.id)?.push(target.id);
    incoming.set(target.id, (incoming.get(target.id) ?? 0) + 1);
    if (source.id === target.id) {
      addIssue({
        code: "self_cycle",
        nodeId: source.id,
        edgeId: edge.id,
        message: `${nodeName(source)} cannot connect to itself.`,
      });
    }
  }

  const requestNodes = nodesOfKind(nodesById, "request");
  const engineNodes = nodesOfKind(nodesById, "engine");
  const metricsNodes = nodesOfKind(nodesById, "metrics");
  const windowNodes = nodesOfKind(nodesById, "window");
  if (requestNodes.length === 0) {
    addIssue({ code: "missing_request", message: "Scenario requires at least one Request node." });
  }
  if (engineNodes.length === 0) {
    addIssue({ code: "missing_engine", message: "Scenario requires one Engine node." });
  }
  if (engineNodes.length > 1) {
    for (const node of engineNodes) {
      addIssue({
        code: "multiple_engines",
        nodeId: node.id,
        message: `${nodeName(node)} is ambiguous because a scenario supports exactly one Engine node.`,
      });
    }
  }
  if (metricsNodes.length > 1) {
    for (const node of metricsNodes) {
      addIssue({
        code: "multiple_metrics",
        nodeId: node.id,
        message: `${nodeName(node)} is ambiguous because a scenario supports at most one Metrics node.`,
      });
    }
  }
  if (windowNodes.length > 1) {
    for (const node of windowNodes) {
      addIssue({
        code: "multiple_windows",
        nodeId: node.id,
        message: `${nodeName(node)} is ambiguous because a scenario supports at most one Window node.`,
      });
    }
  }

  for (const [id, targets] of outgoing) {
    if (targets.length > 1) {
      const node = nodesById.get(id);
      addIssue({
        code: "branch",
        nodeId: id,
        message: `${nodeName(node)} has multiple outgoing connections. Branching is not supported yet.`,
      });
    }
  }
  for (const [id, count] of incoming) {
    if (count > 1) {
      const node = nodesById.get(id);
      addIssue({
        code: "merge",
        nodeId: id,
        message: `${nodeName(node)} has multiple incoming connections. Merging paths is ambiguous.`,
      });
    }
  }

  markCycleIssues(nodesById, incoming, outgoing, addIssue);

  const entries = Array.from(nodesById.keys()).filter((id) => incoming.get(id) === 0);
  const terminals = Array.from(nodesById.keys()).filter((id) => outgoing.get(id)?.length === 0);
  if (nodesById.size > 0 && entries.length !== 1) {
    if (entries.length === 0) {
      addIssue({
        code: "disconnected_start",
        message: "Scenario needs one connected entry Request node.",
      });
    } else {
      for (const id of entries) {
        addIssue({
          code: "disconnected_start",
          nodeId: id,
          message: `${nodeName(nodesById.get(id))} starts a separate graph component. Connect every node into one path.`,
        });
      }
    }
  }
  if (nodesById.size > 0 && terminals.length !== 1) {
    if (terminals.length === 0) {
      addIssue({
        code: "disconnected_end",
        message: "Scenario needs one connected terminal node.",
      });
    } else {
      for (const id of terminals) {
        addIssue({
          code: "disconnected_end",
          nodeId: id,
          message: `${nodeName(nodesById.get(id))} ends a separate graph component. Connect every node into one path.`,
        });
      }
    }
  }

  const path = entries.length === 1 ? followUniquePath(entries[0], outgoing, nodesById.size) : [];
  const hasUniqueTopology = path.length === nodesById.size && terminals.length === 1;
  if (hasUniqueTopology) {
    validatePathOrder(path, nodesById, engineNodes, metricsNodes, windowNodes, addIssue);
  }

  if (issues.length > 0 || !hasUniqueTopology || engineNodes.length !== 1 || requestNodes.length === 0) {
    return { compiled: null, issues };
  }

  return {
    compiled: {
      path,
      requestNodeId: path[0],
      engineNodeId: engineNodes[0].id,
      metricsNodeId: metricsNodes[0]?.id,
      windowNodeId: windowNodes[0]?.id,
    },
    issues: [],
  };
}

function nodesOfKind(
  nodesById: Map<string, Node<FlowNodeData>>,
  kind: FlowNodeKind,
) {
  return Array.from(nodesById.values()).filter((node) => node.data.kind === kind);
}

function markCycleIssues(
  nodesById: Map<string, Node<FlowNodeData>>,
  incoming: Map<string, number>,
  outgoing: Map<string, string[]>,
  addIssue: (issue: ScenarioGraphIssue) => void,
) {
  const remainingIncoming = new Map(incoming);
  const queue = Array.from(nodesById.keys()).filter((id) => remainingIncoming.get(id) === 0);
  let cursor = 0;
  while (cursor < queue.length) {
    const id = queue[cursor];
    cursor += 1;
    for (const target of outgoing.get(id) ?? []) {
      const next = (remainingIncoming.get(target) ?? 0) - 1;
      remainingIncoming.set(target, next);
      if (next === 0) {
        queue.push(target);
      }
    }
  }
  if (queue.length === nodesById.size) {
    return;
  }
  for (const [id, count] of remainingIncoming) {
    if (count > 0) {
      addIssue({
        code: "cycle",
        nodeId: id,
        message: `${nodeName(nodesById.get(id))} is part of or downstream from a cycle.`,
      });
    }
  }
}

function followUniquePath(
  entry: string,
  outgoing: Map<string, string[]>,
  maximumNodes: number,
) {
  const path: string[] = [];
  const visited = new Set<string>();
  let current: string | undefined = entry;
  while (current !== undefined && !visited.has(current) && path.length < maximumNodes) {
    path.push(current);
    visited.add(current);
    const targets: string[] = outgoing.get(current) ?? [];
    current = targets.length === 1 ? targets[0] : undefined;
  }
  return path;
}

function validatePathOrder(
  path: string[],
  nodesById: Map<string, Node<FlowNodeData>>,
  engineNodes: Node<FlowNodeData>[],
  metricsNodes: Node<FlowNodeData>[],
  windowNodes: Node<FlowNodeData>[],
  addIssue: (issue: ScenarioGraphIssue) => void,
) {
  const entry = nodesById.get(path[0]);
  if (entry?.data.kind !== "request") {
    addIssue({
      code: "invalid_entry",
      nodeId: entry?.id,
      message: `${nodeName(entry)} is the entry node, but a scenario must start with Request.`,
    });
  }

  if (engineNodes.length !== 1) {
    return;
  }
  const engineIndex = path.indexOf(engineNodes[0].id);
  const metricsIndex = metricsNodes.length === 1 ? path.indexOf(metricsNodes[0].id) : -1;
  for (let index = 0; index < path.length; index += 1) {
    const node = nodesById.get(path[index]);
    if (!node) {
      continue;
    }
    if (runnableKinds.has(node.data.kind) && index > engineIndex) {
      addIssue({
        code: "invalid_order",
        nodeId: node.id,
        message: `${nodeName(node)} must execute before Engine.`,
      });
    }
    if (node.data.kind === "metrics" && index !== engineIndex + 1) {
      addIssue({
        code: "invalid_order",
        nodeId: node.id,
        message: `${nodeName(node)} must immediately follow Engine.`,
      });
    }
    if (node.data.kind === "window" && (metricsIndex < 0 || index !== metricsIndex + 1 || index !== path.length - 1)) {
      addIssue({
        code: "invalid_order",
        nodeId: node.id,
        message: `${nodeName(node)} must be the final node immediately after Metrics.`,
      });
    }
  }

  const expectedTerminal = windowNodes[0] ?? metricsNodes[0] ?? engineNodes[0];
  const terminal = nodesById.get(path[path.length - 1]);
  if (terminal?.id !== expectedTerminal.id) {
    addIssue({
      code: "invalid_order",
      nodeId: terminal?.id,
      message: `${nodeName(terminal)} cannot terminate the path; expected ${nodeName(expectedTerminal)}.`,
    });
  }
}

function nodeName(node: Node<FlowNodeData> | undefined) {
  if (!node) {
    return "Unknown node";
  }
  const label = typeof node.data.label === "string" && node.data.label.trim()
    ? node.data.label.trim()
    : String(node.data.kind);
  return `${label} (${node.id})`;
}
