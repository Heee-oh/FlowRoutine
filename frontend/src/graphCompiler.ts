import type { Edge, Node } from "@xyflow/react";

import type { FlowNodeData, FlowNodeKind } from "./flowTypes";
import type { ExecutionPlan, ExecutionPlanStep, ExecutionRoute } from "./types";

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
  | "invalid_branch"
  | "invalid_join"
  | "invalid_loop"
  | "ambiguous_assertion"
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
  executionPlan?: ExecutionPlan;
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
  "branch",
  "join",
  "loop",
  "metrics",
  "window",
]);
const executableKinds = new Set<FlowNodeKind>([
  "request",
  "assertion",
  "delay",
  "branch",
  "join",
  "loop",
]);
const controlKinds = new Set<FlowNodeKind>(["branch", "join", "loop"]);
const maxLoopIterations = 1_000;

type AddIssue = (issue: ScenarioGraphIssue) => void;

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
  const addIssue: AddIssue = (issue) => {
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

  const incoming = new Map<string, string[]>();
  const outgoing = new Map<string, string[]>();
  for (const id of nodesById.keys()) {
    incoming.set(id, []);
    outgoing.set(id, []);
  }
  const seenEdges = new Set<string>();
  for (const edge of edges) {
    const source = nodesById.get(edge.source);
    const target = nodesById.get(edge.target);
    if (!source || !target) {
      addIssue({
        code: "dangling_edge",
        nodeId: (source ?? target)?.id,
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
    incoming.get(target.id)?.push(source.id);
    if (source.id === target.id) {
      addIssue({
        code: "self_cycle",
        nodeId: source.id,
        edgeId: edge.id,
        message: `${nodeName(source)} cannot connect to itself.`,
      });
    }
  }
  for (const targets of outgoing.values()) {
    targets.sort();
  }
  for (const sources of incoming.values()) {
    sources.sort();
  }

  const requestNodes = nodesOfKind(nodesById, "request");
  const engineNodes = nodesOfKind(nodesById, "engine");
  const metricsNodes = nodesOfKind(nodesById, "metrics");
  const windowNodes = nodesOfKind(nodesById, "window");
  validateRequiredNodes(requestNodes, engineNodes, metricsNodes, windowNodes, addIssue);

  const structured = Array.from(nodesById.values()).some((node) => controlKinds.has(node.data.kind));
  validateConnectionCardinality(nodesById, incoming, outgoing, structured, addIssue);

  const loopBodyTargets = new Map<string, string>();
  const reducedOutgoing = cloneConnections(outgoing);
  if (structured) {
    validateLoopDefinitions(nodesById, outgoing, reducedOutgoing, loopBodyTargets, addIssue);
  }
  markCycleIssues(nodesById, incoming, reducedOutgoing, addIssue);

  const entries = Array.from(nodesById.keys()).filter((id) => (incoming.get(id)?.length ?? 0) === 0);
  const terminals = Array.from(nodesById.keys()).filter((id) => (outgoing.get(id)?.length ?? 0) === 0);
  validateGraphEndpoints(nodesById, entries, terminals, addIssue);

  if (entries.length === 1) {
    const reachable = forwardReachable(entries[0], outgoing);
    for (const id of nodesById.keys()) {
      if (!reachable.has(id)) {
        addIssue({
          code: "disconnected_start",
          nodeId: id,
          message: `${nodeName(nodesById.get(id))} is disconnected from the scenario entry.`,
        });
      }
    }
  }

  if (!structured) {
    return compileLinearGraph(
      nodesById,
      entries,
      terminals,
      engineNodes,
      metricsNodes,
      windowNodes,
      issues,
      addIssue,
      outgoing,
    );
  }

  if (engineNodes.length === 1) {
    validateStructuredOrder(
      nodesById,
      outgoing,
      engineNodes[0],
      metricsNodes[0],
      windowNodes[0],
      terminals,
      addIssue,
    );
  }
  validateLoopBodies(nodesById, reducedOutgoing, loopBodyTargets, addIssue);
  validateBranchDefinitions(nodesById, reducedOutgoing, outgoing, addIssue);

  if (
    issues.length > 0 ||
    entries.length !== 1 ||
    terminals.length !== 1 ||
    engineNodes.length !== 1 ||
    requestNodes.length === 0
  ) {
    return { compiled: null, issues };
  }

  const entry = entries[0];
  const engine = engineNodes[0];
  const beforeEngine = reverseReachable(engine.id, incoming);
  beforeEngine.delete(engine.id);
  const executionNodeIDs = [
    entry,
    ...Array.from(beforeEngine).filter((id) => id !== entry).sort(),
  ];
  const assertionRequests = resolveAssertionRequests(executionNodeIDs, nodesById, incoming, addIssue);
  const executionPlan = buildExecutionPlan(
    executionNodeIDs,
    nodesById,
    outgoing,
    loopBodyTargets,
    assertionRequests,
    engine.id,
    addIssue,
  );
  if (issues.length > 0 || !executionPlan) {
    return { compiled: null, issues };
  }

  const path = executionNodeIDs.concat([
    engine.id,
    ...(metricsNodes[0] ? [metricsNodes[0].id] : []),
    ...(windowNodes[0] ? [windowNodes[0].id] : []),
  ]);
  return {
    compiled: {
      path,
      requestNodeId: entry,
      engineNodeId: engine.id,
      metricsNodeId: metricsNodes[0]?.id,
      windowNodeId: windowNodes[0]?.id,
      executionPlan,
    },
    issues: [],
  };
}

function validateRequiredNodes(
  requestNodes: Node<FlowNodeData>[],
  engineNodes: Node<FlowNodeData>[],
  metricsNodes: Node<FlowNodeData>[],
  windowNodes: Node<FlowNodeData>[],
  addIssue: AddIssue,
) {
  if (requestNodes.length === 0) {
    addIssue({ code: "missing_request", message: "Scenario requires at least one Request node." });
  }
  if (engineNodes.length === 0) {
    addIssue({ code: "missing_engine", message: "Scenario requires one Engine node." });
  }
  for (const [nodes, code, label] of [
    [engineNodes, "multiple_engines", "Engine"],
    [metricsNodes, "multiple_metrics", "Metrics"],
    [windowNodes, "multiple_windows", "Window"],
  ] as const) {
    if (nodes.length > 1) {
      for (const node of nodes) {
        addIssue({
          code,
          nodeId: node.id,
          message: `${nodeName(node)} is ambiguous because a scenario supports ${label === "Engine" ? "exactly" : "at most"} one ${label} node.`,
        });
      }
    }
  }
}

function validateConnectionCardinality(
  nodesById: Map<string, Node<FlowNodeData>>,
  incoming: Map<string, string[]>,
  outgoing: Map<string, string[]>,
  structured: boolean,
  addIssue: AddIssue,
) {
  for (const [id, targets] of outgoing) {
    const node = nodesById.get(id);
    if (!node) {
      continue;
    }
    const allowed = node.data.kind === "branch"
      ? targets.length >= 2
      : node.data.kind === "loop"
        ? targets.length === 2
        : targets.length <= 1;
    if (!allowed) {
      const control = node.data.kind === "branch" || node.data.kind === "loop";
      addIssue({
        code: control ? (node.data.kind === "branch" ? "invalid_branch" : "invalid_loop") : "branch",
        nodeId: id,
        message: control
          ? `${nodeName(node)} has an invalid number of outgoing connections.`
          : `${nodeName(node)} has multiple outgoing connections. Add an explicit Branch node.`,
      });
    }
  }
  for (const [id, sources] of incoming) {
    const node = nodesById.get(id);
    if (!node) {
      continue;
    }
    const allowsMerge = structured && (node.data.kind === "join" || node.data.kind === "loop");
    if (sources.length > 1 && !allowsMerge) {
      addIssue({
        code: "merge",
        nodeId: id,
        message: `${nodeName(node)} has multiple incoming connections. Add an explicit Join node.`,
      });
    }
    if (node.data.kind === "join" && sources.length < 2) {
      addIssue({
        code: "invalid_join",
        nodeId: id,
        message: `${nodeName(node)} requires at least two incoming branch paths.`,
      });
    }
  }
}

function validateLoopDefinitions(
  nodesById: Map<string, Node<FlowNodeData>>,
  outgoing: Map<string, string[]>,
  reducedOutgoing: Map<string, string[]>,
  loopBodyTargets: Map<string, string>,
  addIssue: AddIssue,
) {
  for (const loop of nodesOfKind(nodesById, "loop")) {
    const bodyTarget = stringData(loop.data.loopBodyTargetId).trim();
    const targets = outgoing.get(loop.id) ?? [];
    const maximum = loop.data.loopMaxIterations;
    if (!bodyTarget || !targets.includes(bodyTarget)) {
      addIssue({
        code: "invalid_loop",
        nodeId: loop.id,
        message: `${nodeName(loop)} body target must match one of its outgoing node IDs.`,
      });
      continue;
    }
    if (!Number.isSafeInteger(maximum) || (maximum ?? 0) < 1 || (maximum ?? 0) > maxLoopIterations) {
      addIssue({
        code: "invalid_loop",
        nodeId: loop.id,
        message: `${nodeName(loop)} maximum iterations must be an integer from 1 to ${maxLoopIterations}.`,
      });
    }
    loopBodyTargets.set(loop.id, bodyTarget);
    reducedOutgoing.set(loop.id, targets.filter((target) => target !== bodyTarget));
  }
}

function validateGraphEndpoints(
  nodesById: Map<string, Node<FlowNodeData>>,
  entries: string[],
  terminals: string[],
  addIssue: AddIssue,
) {
  if (nodesById.size > 0 && entries.length !== 1) {
    if (entries.length === 0) {
      addIssue({ code: "disconnected_start", message: "Scenario needs one connected entry Request node." });
    } else {
      for (const id of entries) {
        addIssue({
          code: "disconnected_start",
          nodeId: id,
          message: `${nodeName(nodesById.get(id))} starts a separate graph component.`,
        });
      }
    }
  }
  if (nodesById.size > 0 && terminals.length !== 1) {
    if (terminals.length === 0) {
      addIssue({ code: "disconnected_end", message: "Scenario needs one connected terminal node." });
    } else {
      for (const id of terminals) {
        addIssue({
          code: "disconnected_end",
          nodeId: id,
          message: `${nodeName(nodesById.get(id))} ends a separate graph component.`,
        });
      }
    }
  }
}

function compileLinearGraph(
  nodesById: Map<string, Node<FlowNodeData>>,
  entries: string[],
  terminals: string[],
  engineNodes: Node<FlowNodeData>[],
  metricsNodes: Node<FlowNodeData>[],
  windowNodes: Node<FlowNodeData>[],
  issues: ScenarioGraphIssue[],
  addIssue: AddIssue,
  outgoing: Map<string, string[]>,
): ScenarioGraphValidation {
  const path = entries.length === 1 ? followUniquePath(entries[0], outgoing, nodesById.size) : [];
  const hasUniqueTopology = path.length === nodesById.size && terminals.length === 1;
  if (hasUniqueTopology) {
    validateLinearOrder(path, nodesById, engineNodes, metricsNodes, windowNodes, addIssue);
  }
  if (issues.length > 0 || !hasUniqueTopology || engineNodes.length !== 1 || nodesOfKind(nodesById, "request").length === 0) {
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

function validateLinearOrder(
  path: string[],
  nodesById: Map<string, Node<FlowNodeData>>,
  engineNodes: Node<FlowNodeData>[],
  metricsNodes: Node<FlowNodeData>[],
  windowNodes: Node<FlowNodeData>[],
  addIssue: AddIssue,
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
    if (executableKinds.has(node.data.kind) && index > engineIndex) {
      addIssue({ code: "invalid_order", nodeId: node.id, message: `${nodeName(node)} must execute before Engine.` });
    }
    if (node.data.kind === "metrics" && index !== engineIndex + 1) {
      addIssue({ code: "invalid_order", nodeId: node.id, message: `${nodeName(node)} must immediately follow Engine.` });
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

function validateStructuredOrder(
  nodesById: Map<string, Node<FlowNodeData>>,
  outgoing: Map<string, string[]>,
  engine: Node<FlowNodeData>,
  metrics: Node<FlowNodeData> | undefined,
  window: Node<FlowNodeData> | undefined,
  terminals: string[],
  addIssue: AddIssue,
) {
  const entry = Array.from(nodesById.keys()).find((id) =>
    !Array.from(outgoing.values()).some((targets) => targets.includes(id)));
  if (entry && nodesById.get(entry)?.data.kind !== "request") {
    addIssue({ code: "invalid_entry", nodeId: entry, message: `${nodeName(nodesById.get(entry))} must be a Request entry.` });
  }
  const engineTargets = outgoing.get(engine.id) ?? [];
  const expectedAfterEngine = metrics?.id;
  if (expectedAfterEngine ? engineTargets.length !== 1 || engineTargets[0] !== expectedAfterEngine : engineTargets.length !== 0) {
    addIssue({
      code: "invalid_order",
      nodeId: engine.id,
      message: metrics ? `${nodeName(metrics)} must immediately follow Engine.` : "Engine must terminate the scenario.",
    });
  }
  if (metrics) {
    const metricTargets = outgoing.get(metrics.id) ?? [];
    if (window ? metricTargets.length !== 1 || metricTargets[0] !== window.id : metricTargets.length !== 0) {
      addIssue({
        code: "invalid_order",
        nodeId: metrics.id,
        message: window ? `${nodeName(window)} must immediately follow Metrics.` : "Metrics must terminate the scenario.",
      });
    }
  }
  if (window && !metrics) {
    addIssue({ code: "invalid_order", nodeId: window.id, message: `${nodeName(window)} requires a Metrics node.` });
  }
  if (window && (outgoing.get(window.id)?.length ?? 0) !== 0) {
    addIssue({ code: "invalid_order", nodeId: window.id, message: `${nodeName(window)} must be terminal.` });
  }
  const expectedTerminal = window ?? metrics ?? engine;
  if (terminals.length === 1 && terminals[0] !== expectedTerminal.id) {
    addIssue({
      code: "invalid_order",
      nodeId: terminals[0],
      message: `${nodeName(nodesById.get(terminals[0]))} cannot terminate the scenario; expected ${nodeName(expectedTerminal)}.`,
    });
  }
  const afterEngine = forwardReachable(engine.id, outgoing);
  for (const id of afterEngine) {
    const node = nodesById.get(id);
    if (id !== engine.id && node && executableKinds.has(node.data.kind)) {
      addIssue({ code: "invalid_order", nodeId: id, message: `${nodeName(node)} must execute before Engine.` });
    }
  }
}

function validateLoopBodies(
  nodesById: Map<string, Node<FlowNodeData>>,
  reducedOutgoing: Map<string, string[]>,
  loopBodyTargets: Map<string, string>,
  addIssue: AddIssue,
) {
  const reverse = reverseConnections(reducedOutgoing);
  for (const [loopID, bodyTarget] of loopBodyTargets) {
    const canReachLoop = reverseReachable(loopID, reverse);
    if (!canReachLoop.has(bodyTarget)) {
      addIssue({
        code: "invalid_loop",
        nodeId: loopID,
        message: `${nodeName(nodesById.get(loopID))} body must return to the Loop node.`,
      });
      continue;
    }
    const region = forwardUntil(bodyTarget, loopID, reducedOutgoing);
    for (const id of region) {
      if (!canReachLoop.has(id)) {
        addIssue({
          code: "invalid_loop",
          nodeId: loopID,
          message: `${nodeName(nodesById.get(loopID))} body can escape without returning to the loop.`,
        });
        break;
      }
      if (nodesById.get(id)?.data.kind === "loop") {
        addIssue({
          code: "invalid_loop",
          nodeId: loopID,
          message: `${nodeName(nodesById.get(loopID))} cannot contain a nested Loop.`,
        });
        break;
      }
    }
    const exit = (reducedOutgoing.get(loopID) ?? [])[0];
    if (exit && region.has(exit)) {
      addIssue({
        code: "invalid_loop",
        nodeId: loopID,
        message: `${nodeName(nodesById.get(loopID))} exit must be outside its body.`,
      });
    }
  }
}

function validateBranchDefinitions(
  nodesById: Map<string, Node<FlowNodeData>>,
  reducedOutgoing: Map<string, string[]>,
  outgoing: Map<string, string[]>,
  addIssue: AddIssue,
) {
  const reverse = reverseConnections(reducedOutgoing);
  const joinOwners = new Map<string, string>();
  const branchRegions = new Map<string, Set<string>[]>();
  for (const branch of nodesOfKind(nodesById, "branch")) {
    const joinID = stringData(branch.data.branchJoinNodeId).trim();
    const join = nodesById.get(joinID);
    if (!joinID || join?.data.kind !== "join") {
      addIssue({
        code: "invalid_branch",
        nodeId: branch.id,
        message: `${nodeName(branch)} join ID must reference an explicit Join node.`,
      });
      continue;
    }
    const existingOwner = joinOwners.get(joinID);
    if (existingOwner) {
      addIssue({
        code: "invalid_join",
        nodeId: joinID,
        message: `${nodeName(join)} cannot be shared by branches ${existingOwner} and ${branch.id}.`,
      });
      continue;
    }
    joinOwners.set(joinID, branch.id);
    const canReachJoin = reverseReachable(joinID, reverse);
    const regions: Set<string>[] = [];
    for (const target of outgoing.get(branch.id) ?? []) {
      if (!canReachJoin.has(target)) {
        addIssue({
          code: "invalid_branch",
          nodeId: branch.id,
          message: `${nodeName(branch)} route ${target} does not reach Join ${joinID}.`,
        });
        continue;
      }
      const region = forwardUntil(target, joinID, reducedOutgoing);
      if (Array.from(region).some((id) => !canReachJoin.has(id))) {
        addIssue({
          code: "invalid_branch",
          nodeId: branch.id,
          message: `${nodeName(branch)} route ${target} can bypass Join ${joinID}.`,
        });
      }
      regions.push(region);
    }
    for (let left = 0; left < regions.length; left += 1) {
      for (let right = left + 1; right < regions.length; right += 1) {
        const overlap = Array.from(regions[left]).find((id) => regions[right].has(id));
        if (overlap) {
          addIssue({
            code: "invalid_branch",
            nodeId: branch.id,
            message: `${nodeName(branch)} routes merge at ${overlap} before Join ${joinID}.`,
          });
        }
      }
    }
    branchRegions.set(branch.id, regions);
    parseBranchRoutes(branch, outgoing.get(branch.id) ?? [], nodesById, addIssue);
  }
  for (const join of nodesOfKind(nodesById, "join")) {
    if (!joinOwners.has(join.id)) {
      addIssue({ code: "invalid_join", nodeId: join.id, message: `${nodeName(join)} is not assigned to a Branch.` });
    }
  }
  for (const [branchID, regions] of branchRegions) {
    const outerJoin = stringData(nodesById.get(branchID)?.data.branchJoinNodeId).trim();
    for (const region of regions) {
      for (const id of region) {
        const nested = nodesById.get(id);
        if (nested?.data.kind === "branch") {
          const nestedJoin = stringData(nested.data.branchJoinNodeId).trim();
          if (!region.has(nestedJoin)) {
            addIssue({
              code: "invalid_branch",
              nodeId: nested.id,
              message: `${nodeName(nested)} must join before outer Join ${outerJoin}.`,
            });
          }
        }
      }
    }
  }
}

function resolveAssertionRequests(
  executionNodeIDs: string[],
  nodesById: Map<string, Node<FlowNodeData>>,
  incoming: Map<string, string[]>,
  addIssue: AddIssue,
) {
  const result = new Map<string, string>();
  for (const id of executionNodeIDs) {
    const node = nodesById.get(id);
    if (node?.data.kind !== "assertion") {
      continue;
    }
    const requests = new Set<string>();
    const visited = new Set<string>();
    const queue = [...(incoming.get(id) ?? [])];
    for (let cursor = 0; cursor < queue.length; cursor += 1) {
      const current = queue[cursor];
      if (visited.has(current)) {
        continue;
      }
      visited.add(current);
      const candidate = nodesById.get(current);
      if (candidate?.data.kind === "request") {
        requests.add(current);
        continue;
      }
      queue.push(...(incoming.get(current) ?? []));
    }
    if (requests.size !== 1) {
      addIssue({
        code: "ambiguous_assertion",
        nodeId: id,
        message: requests.size === 0
          ? `${nodeName(node)} requires an earlier Request on every path.`
          : `${nodeName(node)} has multiple possible preceding Requests: ${Array.from(requests).sort().join(", ")}.`,
      });
      continue;
    }
    result.set(id, Array.from(requests)[0]);
  }
  return result;
}

function buildExecutionPlan(
  executionNodeIDs: string[],
  nodesById: Map<string, Node<FlowNodeData>>,
  outgoing: Map<string, string[]>,
  loopBodyTargets: Map<string, string>,
  assertionRequests: Map<string, string>,
  engineID: string,
  addIssue: AddIssue,
): ExecutionPlan | null {
  const executionIDs = new Set(executionNodeIDs);
  const targetID = (id: string | undefined) => id && id !== engineID && executionIDs.has(id) ? id : undefined;
  const steps: ExecutionPlanStep[] = [];
  for (const id of executionNodeIDs) {
    const node = nodesById.get(id);
    if (!node) {
      return null;
    }
    const targets = outgoing.get(id) ?? [];
    switch (node.data.kind) {
      case "request":
      case "delay":
        steps.push({ id, kind: "step", ...(targetID(targets[0]) ? { nextStepId: targetID(targets[0]) } : {}) });
        break;
      case "assertion": {
        const requestStepId = assertionRequests.get(id);
        if (!requestStepId) {
          return null;
        }
        steps.push({
          id,
          kind: "step",
          requestStepId,
          ...(targetID(targets[0]) ? { nextStepId: targetID(targets[0]) } : {}),
        });
        break;
      }
      case "branch": {
        const routes = parseBranchRoutes(node, targets, nodesById, addIssue);
        if (!routes) {
          return null;
        }
        steps.push({
          id,
          kind: "branch",
          routes,
          joinStepId: stringData(node.data.branchJoinNodeId).trim(),
        });
        break;
      }
      case "join":
        steps.push({ id, kind: "join", ...(targetID(targets[0]) ? { nextStepId: targetID(targets[0]) } : {}) });
        break;
      case "loop": {
        const bodyStepId = loopBodyTargets.get(id);
        const exitStepId = targets.find((target) => target !== bodyStepId);
        if (!bodyStepId) {
          return null;
        }
        steps.push({
          id,
          kind: "loop",
          bodyStepId,
          ...(targetID(exitStepId) ? { exitStepId: targetID(exitStepId) } : {}),
          maxIterations: node.data.loopMaxIterations,
        });
        break;
      }
      default:
        return null;
    }
  }
  return { schemaVersion: 1, entryStepId: executionNodeIDs[0], steps };
}

function parseBranchRoutes(
  branch: Node<FlowNodeData>,
  targets: string[],
  nodesById: Map<string, Node<FlowNodeData>>,
  addIssue: AddIssue,
): ExecutionRoute[] | null {
  const weights = new Map<string, number>();
  const raw = stringData(branch.data.branchRoutesText);
  for (const line of raw.split("\n").map((value) => value.trim()).filter(Boolean)) {
    const splitAt = line.lastIndexOf("=");
    if (splitAt < 1) {
      addIssue({
        code: "invalid_branch",
        nodeId: branch.id,
        message: `${nodeName(branch)} route weight must use targetNodeId=weight: ${line}.`,
      });
      return null;
    }
    const target = line.slice(0, splitAt).trim();
    const weight = Number(line.slice(splitAt + 1).trim());
    if (!targets.includes(target) || weights.has(target) || !Number.isSafeInteger(weight) || weight < 1 || weight > 1_000_000) {
      addIssue({
        code: "invalid_branch",
        nodeId: branch.id,
        message: `${nodeName(branch)} has invalid or duplicate route weight ${line}.`,
      });
      return null;
    }
    weights.set(target, weight);
  }
  return targets.slice().sort().map((target) => ({
    id: target,
    name: nodesById.get(target)?.data.label ?? target,
    targetStepId: target,
    weight: weights.get(target) ?? 1,
  }));
}

function markCycleIssues(
  nodesById: Map<string, Node<FlowNodeData>>,
  incoming: Map<string, string[]>,
  outgoing: Map<string, string[]>,
  addIssue: AddIssue,
) {
  const remainingIncoming = new Map<string, number>();
  for (const id of nodesById.keys()) {
    remainingIncoming.set(id, 0);
  }
  for (const targets of outgoing.values()) {
    for (const target of targets) {
      remainingIncoming.set(target, (remainingIncoming.get(target) ?? 0) + 1);
    }
  }
  const queue = Array.from(nodesById.keys()).filter((id) => remainingIncoming.get(id) === 0).sort();
  for (let cursor = 0; cursor < queue.length; cursor += 1) {
    for (const target of outgoing.get(queue[cursor]) ?? []) {
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
        message: `${nodeName(nodesById.get(id))} is part of an unbounded cycle. Use a bounded Loop node.`,
      });
    }
  }
  void incoming;
}

function nodesOfKind(
  nodesById: Map<string, Node<FlowNodeData>>,
  kind: FlowNodeKind,
) {
  return Array.from(nodesById.values()).filter((node) => node.data.kind === kind);
}

function followUniquePath(entry: string, outgoing: Map<string, string[]>, maximumNodes: number) {
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

function cloneConnections(connections: Map<string, string[]>) {
  return new Map(Array.from(connections, ([id, values]) => [id, values.slice()]));
}

function reverseConnections(outgoing: Map<string, string[]>) {
  const reverse = new Map<string, string[]>();
  for (const id of outgoing.keys()) {
    reverse.set(id, []);
  }
  for (const [source, targets] of outgoing) {
    for (const target of targets) {
      reverse.get(target)?.push(source);
    }
  }
  return reverse;
}

function forwardReachable(entry: string, outgoing: Map<string, string[]>) {
  const reachable = new Set<string>();
  const queue = [entry];
  for (let cursor = 0; cursor < queue.length; cursor += 1) {
    const current = queue[cursor];
    if (reachable.has(current)) {
      continue;
    }
    reachable.add(current);
    queue.push(...(outgoing.get(current) ?? []));
  }
  return reachable;
}

function reverseReachable(target: string, incoming: Map<string, string[]>) {
  const reachable = new Set<string>();
  const queue = [target];
  for (let cursor = 0; cursor < queue.length; cursor += 1) {
    const current = queue[cursor];
    if (reachable.has(current)) {
      continue;
    }
    reachable.add(current);
    queue.push(...(incoming.get(current) ?? []));
  }
  return reachable;
}

function forwardUntil(entry: string, stop: string, outgoing: Map<string, string[]>) {
  const visited = new Set<string>();
  const queue = [entry];
  for (let cursor = 0; cursor < queue.length; cursor += 1) {
    const current = queue[cursor];
    if (current === stop || visited.has(current)) {
      continue;
    }
    visited.add(current);
    queue.push(...(outgoing.get(current) ?? []));
  }
  return visited;
}

function stringData(value: unknown) {
  return typeof value === "string" ? value : "";
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
