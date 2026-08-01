import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  addEdge,
  type Connection,
  type Edge,
  type EdgeChange,
  type Node,
  type NodeChange,
  useEdgesState,
  useNodesState,
} from "@xyflow/react";
import { Play, Square, Workflow } from "lucide-react";
import { AppDialogs, AppToolbar, HistoryBanner } from "./components/AppChrome";
import { EnvironmentPanel } from "./components/EnvironmentPanel";
import { FlowCanvas } from "./components/FlowCanvas";
import { MetricGrid, MetricsChart } from "./components/Metrics";
import { NodeInspector } from "./components/NodeInspector";
import { NodePalette } from "./components/NodePalette";
import { ScenarioPath } from "./components/ScenarioPath";
import { ScenarioLibraryPanel } from "./components/ScenarioLibraryPanel";
import {
  createEnvironmentProfile,
  loadActiveEnvironmentId,
  loadEnvironmentProfiles,
  saveActiveEnvironmentId,
  saveEnvironmentProfiles,
} from "./environmentProfiles";
import {
  assessStartSafety,
  buildStartRequestFromGraph,
  createFlowNode,
  getMetricWindowMs,
  initialFlowEdges,
  initialFlowNodes,
  openAPIEndpointToRequestSettings,
  refreshNodeDisplay,
} from "./flowModel";
import type {
  EnvironmentProfile,
  FlowNodeData,
  FlowNodeKind,
  RuntimeAuthSecret,
  SafetyAssessment,
  SavedScenario,
} from "./flowTypes";
import {
  BoundedHistory,
  createGraphSnapshot,
  type GraphSnapshot,
} from "./graphHistory";
import { validateScenarioGraph } from "./graphCompiler";
import type { HelpLanguage, HelpTopic } from "./help";
import {
  appendImportedRequestsToGraph,
  buildReplacementImportGraph,
  type ImportedRequest,
  type RequestImportMode,
} from "./importGraph";
import { assertImportFileSize } from "./importValidation";
import { downloadK6Script } from "./k6Export";
import { purgeLegacyRunBaselines } from "./report";
import {
  createSavedScenario,
  createScenarioSnapshot,
  nextNodeIndexFromNodes,
  reviveSavedNodes,
} from "./scenarioPersistence";
import {
  deleteScenario,
  downloadScenarioFile,
  formatScenarioTags,
  loadScenarioDraft,
  loadScenarioLibrary,
  parseScenarioFile,
  parseScenarioTags,
  saveScenario,
  saveScenarioDraft,
} from "./scenarioLibrary";
import { DEFAULT_METRIC_WINDOW_MS, useLoadStore, useMetricsStore } from "./store";
import type {
  OpenAPIEndpoint,
  StartRequest,
} from "./types";
import { useOpenAPIImportWorkflow, useRequestImportWorkflow } from "./useImportWorkflows";
import { onMetricsBatch, preflightLoad, startLoad, stopLoad } from "./wails";

export function App() {
  const running = useLoadStore((state) => state.running);
  const stopping = useLoadStore((state) => state.stopping);
  const error = useLoadStore((state) => state.error);
  const setRunning = useLoadStore((state) => state.setRunning);
  const setStopping = useLoadStore((state) => state.setStopping);
  const setError = useLoadStore((state) => state.setError);
  const buildStartRequest = useLoadStore((state) => state.buildStartRequest);
  const pushBatch = useMetricsStore((state) => state.pushBatch);
  const beginMetricsRun = useMetricsStore((state) => state.beginRun);
  const resetMetrics = useMetricsStore((state) => state.reset);
  const setMetricWindowMs = useMetricsStore((state) => state.setMetricWindowMs);
  const [initialDraft] = useState(loadScenarioDraft);
  const [pendingStart, setPendingStart] = useState<StartRequest | null>(null);
  const [pendingSafety, setPendingSafety] = useState<SafetyAssessment | null>(null);
  const [pendingScenario, setPendingScenario] = useState<SavedScenario | null>(null);
  const [helpTopic, setHelpTopic] = useState<HelpTopic | null>(null);
  const [helpLanguage, setHelpLanguage] = useState<HelpLanguage>("ko");
  const [savedScenarios, setSavedScenarios] = useState<SavedScenario[]>(loadScenarioLibrary);
  const [activeScenarioId, setActiveScenarioId] = useState<string | null>(initialDraft?.activeScenarioId ?? null);
  const [scenarioName, setScenarioName] = useState(initialDraft?.name ?? "Untitled scenario");
  const [scenarioTagsText, setScenarioTagsText] = useState(
    initialDraft ? formatScenarioTags(initialDraft.tags) : "",
  );
  const [scenarioCreatedAtUnixMs, setScenarioCreatedAtUnixMs] = useState(
    initialDraft?.createdAtUnixMs ?? Date.now(),
  );
  const [deletedScenario, setDeletedScenario] = useState<{
    scenario: SavedScenario;
    wasActive: boolean;
  } | null>(null);
  const [historyNotice, setHistoryNotice] = useState("");
  const [authSecrets, setAuthSecrets] = useState<Record<string, RuntimeAuthSecret>>({});
  const [environmentProfiles, setEnvironmentProfiles] = useState<EnvironmentProfile[]>(loadEnvironmentProfiles);
  const [activeEnvironmentId, setActiveEnvironmentId] = useState<string | null>(
    () => initialDraft?.environmentProfileId ?? loadActiveEnvironmentId(),
  );
  const [environmentSecrets, setEnvironmentSecrets] = useState<Record<string, Record<string, string>>>({});
  const [flowNodes, setFlowNodes, onFlowNodesChange] = useNodesState<Node<FlowNodeData>>(
    initialDraft?.nodes ?? initialFlowNodes,
  );
  const [flowEdges, setFlowEdges, onFlowEdgesChange] = useEdgesState<Edge>(
    initialDraft?.edges ?? initialFlowEdges,
  );
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const nextNodeIndex = useRef(nextNodeIndexFromNodes(initialDraft?.nodes ?? initialFlowNodes));
  const graphHistory = useRef(new BoundedHistory<GraphSnapshot>(50));
  const historyCapturePending = useRef(false);
  const [, refreshHistory] = useState(0);
  const selectedNode = useMemo(
    () => flowNodes.find((node) => node.id === selectedNodeId) ?? null,
    [flowNodes, selectedNodeId],
  );
  const activeEnvironment = useMemo(
    () => environmentProfiles.find((profile) => profile.id === activeEnvironmentId) ?? null,
    [activeEnvironmentId, environmentProfiles],
  );
  const workspaceRef = useRef<GraphSnapshot>({
    nodes: flowNodes,
    edges: flowEdges,
    authSecrets,
    selectedNodeId,
    nextNodeIndex: nextNodeIndex.current,
    scenario: {
      activeScenarioId,
      name: scenarioName,
      tagsText: scenarioTagsText,
      createdAtUnixMs: scenarioCreatedAtUnixMs,
      activeEnvironmentId,
    },
  });
  workspaceRef.current = {
    nodes: flowNodes,
    edges: flowEdges,
    authSecrets,
    selectedNodeId,
    nextNodeIndex: nextNodeIndex.current,
    scenario: {
      activeScenarioId,
      name: scenarioName,
      tagsText: scenarioTagsText,
      createdAtUnixMs: scenarioCreatedAtUnixMs,
      activeEnvironmentId,
    },
  };
  const draftRef = useRef<Parameters<typeof saveScenarioDraft>[0]>({
    ...(activeScenarioId ? { activeScenarioId } : {}),
    name: scenarioName,
    tags: parseScenarioTags(scenarioTagsText),
    createdAtUnixMs: scenarioCreatedAtUnixMs,
    ...(activeEnvironmentId ? { environmentProfileId: activeEnvironmentId } : {}),
    nodes: flowNodes,
    edges: flowEdges,
  });
  draftRef.current = {
    ...(activeScenarioId ? { activeScenarioId } : {}),
    name: scenarioName,
    tags: parseScenarioTags(scenarioTagsText),
    createdAtUnixMs: scenarioCreatedAtUnixMs,
    ...(activeEnvironmentId ? { environmentProfileId: activeEnvironmentId } : {}),
    nodes: flowNodes,
    edges: flowEdges,
  };
  const graphValidation = useMemo(
    () => validateScenarioGraph(flowNodes, flowEdges),
    [flowEdges, flowNodes],
  );
  const canvasNodes = useMemo(() => {
    const errorsByNode = new Map<string, string[]>();
    for (const issue of graphValidation.issues) {
      if (!issue.nodeId) {
        continue;
      }
      const messages = errorsByNode.get(issue.nodeId) ?? [];
      messages.push(issue.message);
      errorsByNode.set(issue.nodeId, messages);
    }
    const orderByNode = new Map(
      graphValidation.compiled?.path.map((id, index) => [id, index] as const) ?? [],
    );
    return flowNodes.map((node) => ({
      ...node,
      data: {
        ...node.data,
        validationError: errorsByNode.get(node.id)?.join(" "),
        executionOrder: orderByNode.get(node.id),
      },
    }));
  }, [flowNodes, graphValidation]);
  const activeMetricWindowMs = useMemo(
    () => getMetricWindowMs(flowNodes, graphValidation.compiled) || DEFAULT_METRIC_WINDOW_MS,
    [flowNodes, graphValidation.compiled],
  );

  useEffect(() => {
    purgeLegacyRunBaselines();
  }, []);

  useEffect(() => onMetricsBatch((batch) => {
    pushBatch(batch);
    setRunning(batch.running);
    if (!batch.running) {
      setStopping(false);
    }
  }), [pushBatch, setRunning, setStopping]);

  useEffect(() => {
    setMetricWindowMs(activeMetricWindowMs);
  }, [activeMetricWindowMs, setMetricWindowMs]);

  useEffect(() => {
    const timeout = window.setTimeout(() => saveScenarioDraft(draftRef.current), 300);
    return () => window.clearTimeout(timeout);
  }, [
    activeEnvironmentId,
    activeScenarioId,
    flowEdges,
    flowNodes,
    scenarioCreatedAtUnixMs,
    scenarioName,
    scenarioTagsText,
  ]);

  useEffect(() => {
    const flushDraft = () => saveScenarioDraft(draftRef.current);
    window.addEventListener("beforeunload", flushDraft);
    return () => {
      window.removeEventListener("beforeunload", flushDraft);
      flushDraft();
    };
  }, []);

  const recordGraphChange = useCallback((label: string, coalesce = false) => {
    if (coalesce && historyCapturePending.current) {
      return;
    }
    graphHistory.current.record(createGraphSnapshot(workspaceRef.current), label);
    setHistoryNotice(label);
    refreshHistory((version) => version + 1);
    if (coalesce) {
      historyCapturePending.current = true;
      queueMicrotask(() => {
        historyCapturePending.current = false;
      });
    }
  }, []);

  const invalidateRedo = useCallback(() => {
    if (graphHistory.current.clearRedo()) {
      refreshHistory((version) => version + 1);
    }
  }, []);

  const handleDeleteNode = useCallback((nodeId: string) => {
    recordGraphChange("Deleted node", true);
    setFlowNodes((currentNodes) => currentNodes.filter((node) => node.id !== nodeId));
    setFlowEdges((currentEdges) => currentEdges.filter((edge) => edge.source !== nodeId && edge.target !== nodeId));
    setSelectedNodeId((current) => (current === nodeId ? null : current));
    setAuthSecrets((current) => {
      const { [nodeId]: _deleted, ...rest } = current;
      return rest;
    });
  }, [recordGraphChange, setFlowEdges, setFlowNodes]);

  useEffect(() => {
    setFlowNodes((currentNodes) => currentNodes.map((node) => ({
      ...node,
      data: {
        ...node.data,
        onDelete: handleDeleteNode,
      },
    })));
  }, [handleDeleteNode, setFlowNodes]);

  const applyWorkspaceSnapshot = useCallback((snapshot: GraphSnapshot) => {
    setFlowNodes(reviveSavedNodes(snapshot.nodes, handleDeleteNode));
    setFlowEdges(snapshot.edges);
    setAuthSecrets(snapshot.authSecrets);
    setSelectedNodeId(snapshot.selectedNodeId);
    nextNodeIndex.current = snapshot.nextNodeIndex;
    setActiveScenarioId(snapshot.scenario.activeScenarioId);
    setScenarioName(snapshot.scenario.name);
    setScenarioTagsText(snapshot.scenario.tagsText);
    setScenarioCreatedAtUnixMs(snapshot.scenario.createdAtUnixMs);
    setActiveEnvironmentId(snapshot.scenario.activeEnvironmentId);
    saveActiveEnvironmentId(snapshot.scenario.activeEnvironmentId);
    setError("");
  }, [handleDeleteNode, setError, setFlowEdges, setFlowNodes]);

  const handleUndo = useCallback(() => {
    const entry = graphHistory.current.undo(createGraphSnapshot(workspaceRef.current));
    if (!entry) {
      return;
    }
    applyWorkspaceSnapshot(entry.value);
    setHistoryNotice(`Undid: ${entry.label}`);
    refreshHistory((version) => version + 1);
  }, [applyWorkspaceSnapshot]);

  const handleRedo = useCallback(() => {
    const entry = graphHistory.current.redo(createGraphSnapshot(workspaceRef.current));
    if (!entry) {
      return;
    }
    applyWorkspaceSnapshot(entry.value);
    setHistoryNotice(`Redid: ${entry.label}`);
    refreshHistory((version) => version + 1);
  }, [applyWorkspaceSnapshot]);

  const handleAddNode = useCallback((kind: FlowNodeKind) => {
    invalidateRedo();
    const index = nextNodeIndex.current;
    nextNodeIndex.current += 1;
    setFlowNodes((currentNodes) => currentNodes.concat(createFlowNode(kind, index, null, undefined, handleDeleteNode)));
  }, [handleDeleteNode, invalidateRedo, setFlowNodes]);

  const handleSelectEnvironment = useCallback((id: string | null) => {
    invalidateRedo();
    setActiveEnvironmentId(id);
    saveActiveEnvironmentId(id);
  }, [invalidateRedo]);

  const handleScenarioNameChange = useCallback((name: string) => {
    invalidateRedo();
    setScenarioName(name);
  }, [invalidateRedo]);

  const handleScenarioTagsChange = useCallback((tags: string) => {
    invalidateRedo();
    setScenarioTagsText(tags);
  }, [invalidateRedo]);

  const handleAddEnvironment = useCallback(() => {
    const profile = createEnvironmentProfile(environmentProfiles.length + 1);
    const next = environmentProfiles.concat(profile);
    setEnvironmentProfiles(next);
    saveEnvironmentProfiles(next);
    handleSelectEnvironment(profile.id);
  }, [environmentProfiles, handleSelectEnvironment]);

  const handleUpdateEnvironment = useCallback((profile: EnvironmentProfile) => {
    setEnvironmentProfiles((current) => {
      const next = current.map((item) => item.id === profile.id ? profile : item);
      saveEnvironmentProfiles(next);
      return next;
    });
  }, []);

  const handleDeleteEnvironment = useCallback((id: string) => {
    setEnvironmentProfiles((current) => {
      const next = current.filter((profile) => profile.id !== id);
      saveEnvironmentProfiles(next);
      return next;
    });
    setEnvironmentSecrets((current) => {
      const { [id]: _deleted, ...next } = current;
      return next;
    });
    if (activeEnvironmentId === id) {
      handleSelectEnvironment(null);
    }
  }, [activeEnvironmentId, handleSelectEnvironment]);

  const handleUpdateEnvironmentSecret = useCallback((name: string, value: string) => {
    if (!activeEnvironmentId || !name) {
      return;
    }
    setEnvironmentSecrets((current) => {
      const bindings = { ...current[activeEnvironmentId] };
      if (value) {
        bindings[name] = value;
      } else {
        delete bindings[name];
      }
      return { ...current, [activeEnvironmentId]: bindings };
    });
  }, [activeEnvironmentId]);

  const applyScenario = useCallback((scenario: SavedScenario, nextActiveScenarioId: string | null) => {
    setError("");
    const nodes = reviveSavedNodes(scenario.nodes, handleDeleteNode);
    setFlowNodes(nodes);
    setFlowEdges(scenario.edges);
    setSelectedNodeId(null);
    setAuthSecrets({});
    setActiveScenarioId(nextActiveScenarioId);
    setScenarioName(scenario.name);
    setScenarioTagsText(formatScenarioTags(scenario.tags));
    setScenarioCreatedAtUnixMs(scenario.createdAtUnixMs);
    if (scenario.environmentProfileId) {
      if (environmentProfiles.some((profile) => profile.id === scenario.environmentProfileId)) {
        handleSelectEnvironment(scenario.environmentProfileId);
      } else {
        handleSelectEnvironment(null);
        setError("The environment profile saved with this scenario is unavailable; select another profile.");
      }
    } else {
      handleSelectEnvironment(null);
    }
    nextNodeIndex.current = nextNodeIndexFromNodes(nodes);
  }, [environmentProfiles, handleDeleteNode, handleSelectEnvironment, setError, setFlowEdges, setFlowNodes]);

  const persistScenario = useCallback((scenario: SavedScenario) => {
    invalidateRedo();
    setSavedScenarios((current) => saveScenario(scenario, current));
    setActiveScenarioId(scenario.id);
    setScenarioName(scenario.name);
    setScenarioTagsText(formatScenarioTags(scenario.tags));
    setScenarioCreatedAtUnixMs(scenario.createdAtUnixMs);
  }, [invalidateRedo]);

  const currentScenarioSnapshot = useCallback(() => {
    if (!scenarioName.trim()) {
      throw new Error("Scenario name is required");
    }
    return createScenarioSnapshot(flowNodes, flowEdges, {
      ...(activeScenarioId ? { id: activeScenarioId } : {}),
      name: scenarioName,
      tags: parseScenarioTags(scenarioTagsText),
      createdAtUnixMs: scenarioCreatedAtUnixMs,
      ...(activeEnvironmentId ? { environmentProfileId: activeEnvironmentId } : {}),
    });
  }, [activeEnvironmentId, activeScenarioId, flowEdges, flowNodes, scenarioCreatedAtUnixMs, scenarioName, scenarioTagsText]);

  const handleSaveScenario = useCallback(() => {
    try {
      const scenario = currentScenarioSnapshot();
      persistScenario(scenario);
      setDeletedScenario(null);
      setHistoryNotice(`Saved: ${scenario.name}`);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save scenario");
    }
  }, [currentScenarioSnapshot, persistScenario, setError]);

  const handleLoadScenario = useCallback((scenario: SavedScenario) => {
    recordGraphChange(`Loaded scenario: ${scenario.name}`);
    applyScenario(scenario, scenario.id);
    setDeletedScenario(null);
    setHistoryNotice(`Loaded: ${scenario.name}`);
  }, [applyScenario, recordGraphChange]);

  const handleNewScenario = useCallback(() => {
    recordGraphChange("Created new scenario");
    const nodes = reviveSavedNodes(initialFlowNodes, handleDeleteNode);
    setFlowNodes(nodes);
    setFlowEdges(initialFlowEdges.map((edge) => ({ ...edge })));
    setSelectedNodeId(null);
    setAuthSecrets({});
    setActiveScenarioId(null);
    setScenarioName("Untitled scenario");
    setScenarioTagsText("");
    setScenarioCreatedAtUnixMs(Date.now());
    nextNodeIndex.current = nextNodeIndexFromNodes(nodes);
    setDeletedScenario(null);
    setError("");
  }, [handleDeleteNode, recordGraphChange, setError, setFlowEdges, setFlowNodes]);

  const handleDeleteScenario = useCallback((scenario: SavedScenario) => {
    invalidateRedo();
    setSavedScenarios((current) => deleteScenario(scenario.id, current));
    const wasActive = activeScenarioId === scenario.id;
    if (wasActive) {
      setActiveScenarioId(null);
    }
    setDeletedScenario({ scenario, wasActive });
    setHistoryNotice(`Deleted library entry: ${scenario.name}`);
  }, [activeScenarioId, invalidateRedo]);

  const handleUndoScenarioDelete = useCallback(() => {
    if (!deletedScenario) {
      return;
    }
    invalidateRedo();
    setSavedScenarios((current) => saveScenario(deletedScenario.scenario, current));
    if (deletedScenario.wasActive) {
      setActiveScenarioId(deletedScenario.scenario.id);
    }
    setHistoryNotice(`Restored library entry: ${deletedScenario.scenario.name}`);
    setDeletedScenario(null);
  }, [deletedScenario, invalidateRedo]);

  const handleExportScenario = useCallback(() => {
    try {
      downloadScenarioFile(currentScenarioSnapshot());
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to export scenario");
    }
  }, [currentScenarioSnapshot, setError]);

  const handleImportScenario = useCallback(async (file: File) => {
    try {
      assertImportFileSize(file.size, "Scenario file");
      const imported = parseScenarioFile(await file.text());
      recordGraphChange(`Imported scenario file: ${file.name}`);
      applyScenario(imported, null);
      setDeletedScenario(null);
      setHistoryNotice(`Imported as draft: ${imported.name}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to import scenario file");
    }
  }, [applyScenario, recordGraphChange, setError]);

  const updateAuthSecret = useCallback((nodeId: string, patch: Partial<RuntimeAuthSecret>) => {
    invalidateRedo();
    setAuthSecrets((current) => ({
      ...current,
      [nodeId]: {
        ...current[nodeId],
        ...patch,
      },
    }));
  }, [invalidateRedo]);

  const handleConnect = useCallback((connection: Connection) => {
    invalidateRedo();
    setFlowEdges((currentEdges) => addEdge(connection, currentEdges));
  }, [invalidateRedo, setFlowEdges]);

  const handleNodeChanges = useCallback((changes: NodeChange<Node<FlowNodeData>>[]) => {
    const removedNodeIds = changes.flatMap((change) => change.type === "remove" ? [change.id] : []);
    if (removedNodeIds.length > 0) {
      recordGraphChange("Deleted graph selection", true);
      const removed = new Set(removedNodeIds);
      setFlowEdges((currentEdges) => currentEdges.filter(
        (edge) => !removed.has(edge.source) && !removed.has(edge.target),
      ));
      setAuthSecrets((current) => Object.fromEntries(
        Object.entries(current).filter(([nodeId]) => !removed.has(nodeId)),
      ));
    } else if (changes.some((change) => change.type !== "select")) {
      invalidateRedo();
    }
    onFlowNodesChange(changes);
    for (const change of changes) {
      if ("id" in change && change.type === "select" && change.selected) {
        setSelectedNodeId(change.id);
        return;
      }
    }
    if (changes.some((change) => "id" in change && change.type === "remove" && change.id === selectedNodeId)) {
      setSelectedNodeId(null);
    }
  }, [invalidateRedo, onFlowNodesChange, recordGraphChange, selectedNodeId, setFlowEdges]);

  const handleEdgeChanges = useCallback((changes: EdgeChange[]) => {
    if (changes.some((change) => change.type === "remove")) {
      recordGraphChange("Deleted connection", true);
    } else if (changes.some((change) => change.type !== "select")) {
      invalidateRedo();
    }
    onFlowEdgesChange(changes);
  }, [invalidateRedo, onFlowEdgesChange, recordGraphChange]);

  const handlePaneClick = useCallback(() => {
    setSelectedNodeId(null);
  }, []);

  const updateSelectedNodeData = useCallback((patch: Partial<FlowNodeData>) => {
    if (!selectedNodeId) {
      return;
    }
    invalidateRedo();
    setFlowNodes((currentNodes) => currentNodes.map((node) => {
      if (node.id !== selectedNodeId) {
        return node;
      }
      return refreshNodeDisplay({
        ...node,
        data: {
          ...node.data,
          ...patch,
        },
      });
    }));
  }, [invalidateRedo, selectedNodeId, setFlowNodes]);

  const assertGraphValid = useCallback(() => {
    if (graphValidation.compiled) {
      return;
    }
    const issue = graphValidation.issues[0];
    if (issue?.nodeId) {
      setSelectedNodeId(issue.nodeId);
    }
    throw new Error(issue?.message ?? "Scenario graph is invalid");
  }, [graphValidation]);

  const executeStart = useCallback(async (request: StartRequest) => {
    try {
      setError("");
      setStopping(false);
      beginMetricsRun(request);
      setRunning(true);
      await startLoad(request);
    } catch (err) {
      resetMetrics();
      setRunning(false);
      setStopping(false);
      setError(err instanceof Error ? err.message : "Failed to start load test");
    }
  }, [beginMetricsRun, resetMetrics, setError, setRunning, setStopping]);

  const handleStart = useCallback(async () => {
    try {
      setError("");
      assertGraphValid();
      const requested = buildStartRequestFromGraph(
        flowNodes,
        flowEdges,
        buildStartRequest(),
        authSecrets,
        {
          profile: activeEnvironment,
          secretBindings: activeEnvironment ? environmentSecrets[activeEnvironment.id] : undefined,
        },
      );
      const preflight = await preflightLoad(requested);
      const request: StartRequest = {
        ...requested,
        config: {
          ...requested.config,
          ...preflight.effectiveConfig,
        },
        batchIntervalMs: preflight.effectiveBatchIntervalMs,
      };
      const scenario = createSavedScenario(
        flowNodes,
        flowEdges,
        request,
        activeEnvironment?.id,
        {
          ...(activeScenarioId ? { id: activeScenarioId } : {}),
          name: scenarioName,
          tags: parseScenarioTags(scenarioTagsText),
          createdAtUnixMs: scenarioCreatedAtUnixMs,
        },
      );
      const assessment = assessStartSafety(request.config, preflight);
      if (assessment.confirmationRequired) {
        setPendingStart(request);
        setPendingSafety(assessment);
        setPendingScenario(scenario);
        return;
      }
      persistScenario(scenario);
      await executeStart(request);
    } catch (err) {
      setRunning(false);
      setStopping(false);
      setError(err instanceof Error ? err.message : "Failed to start load test");
    }
  }, [activeEnvironment, activeScenarioId, assertGraphValid, authSecrets, buildStartRequest, environmentSecrets, executeStart, flowEdges, flowNodes, persistScenario, scenarioCreatedAtUnixMs, scenarioName, scenarioTagsText, setError, setRunning, setStopping]);

  const handleStop = useCallback(async () => {
    if (stopping) {
      return;
    }
    setStopping(true);
    try {
      await stopLoad();
      setRunning(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to stop load test");
    } finally {
      setStopping(false);
    }
  }, [setError, setRunning, setStopping, stopping]);

  const handleExportK6 = useCallback(() => {
    try {
      setError("");
      assertGraphValid();
      const request = buildStartRequestFromGraph(
        flowNodes,
        flowEdges,
        buildStartRequest(),
        authSecrets,
        {
          profile: activeEnvironment,
          resolveSecrets: false,
        },
      );
      downloadK6Script(request);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to export k6 script");
    }
  }, [activeEnvironment, assertGraphValid, authSecrets, buildStartRequest, flowEdges, flowNodes, setError]);

  const handleConfirmStart = useCallback(() => {
    if (!pendingStart) {
      return;
    }
    const request = pendingStart;
    const scenario = pendingScenario;
    setPendingStart(null);
    setPendingSafety(null);
    setPendingScenario(null);
    if (scenario) {
      persistScenario(scenario);
    }
    void executeStart(request);
  }, [executeStart, pendingScenario, pendingStart, persistScenario]);

  const handleCancelStart = useCallback(() => {
    setPendingStart(null);
    setPendingSafety(null);
    setPendingScenario(null);
  }, []);

  const handleAddOpenAPIEndpoint = useCallback((endpoint: OpenAPIEndpoint, sourceURL: string) => {
    const index = nextNodeIndex.current;
    nextNodeIndex.current += 1;
    const node = createFlowNode(
      "request",
      index,
      openAPIEndpointToRequestSettings(endpoint, sourceURL),
      undefined,
      handleDeleteNode,
    );
    recordGraphChange("Imported OpenAPI request");
    setFlowNodes((currentNodes) => currentNodes.concat(node));
    setSelectedNodeId(node.id);
  }, [handleDeleteNode, recordGraphChange, setFlowNodes]);

  const {
    close: handleCloseOpenAPIImport,
    error: openAPIImportError,
    imported: openAPIImported,
    loading: openAPIImportLoading,
    message: openAPIImportMessage,
    open: openAPIImportOpen,
    selectEndpoint: handleSelectOpenAPIEndpoint,
    show: handleOpenOpenAPIImport,
    submit: handleSubmitOpenAPIImport,
  } = useOpenAPIImportWorkflow(handleAddOpenAPIEndpoint);
  const {
    clear: handleCancelRequestImport,
    error: requestImportError,
    importHAR: handleImportHarFile,
    importPostman: handleImportPostmanFile,
    pending: pendingRequestImport,
    setError: setRequestImportError,
  } = useRequestImportWorkflow(setError);

  const handleConfirmRequestImport = useCallback((requests: ImportedRequest[], mode: RequestImportMode) => {
    if (!pendingRequestImport) {
      return;
    }
    try {
      setError("");
      setRequestImportError("");
      const importedGraph = mode === "replace"
        ? buildReplacementImportGraph(requests, handleDeleteNode)
        : appendImportedRequestsToGraph(
          flowNodes,
          flowEdges,
          requests,
          nextNodeIndex.current,
          handleDeleteNode,
        );
      const historyLabel = `${mode === "replace" ? "Replaced graph with" : "Appended"} ${requests.length} ${pendingRequestImport.source} request${requests.length === 1 ? "" : "s"}`;
      recordGraphChange(historyLabel);
      setFlowNodes(importedGraph.nodes);
      setFlowEdges(importedGraph.edges);
      setSelectedNodeId(importedGraph.selectedNodeId);
      if (mode === "replace") {
        setAuthSecrets({});
      }
      nextNodeIndex.current = importedGraph.nextNodeIndex;
      handleCancelRequestImport();
      setHistoryNotice(historyLabel);
    } catch (err) {
      setRequestImportError(err instanceof Error ? err.message : "Failed to apply request import");
    }
  }, [
    flowEdges,
    flowNodes,
    handleCancelRequestImport,
    handleDeleteNode,
    pendingRequestImport,
    recordGraphChange,
    setError,
    setFlowEdges,
    setFlowNodes,
    setRequestImportError,
  ]);

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark"><Workflow size={22} /></div>
          <div>
            <h1>FlowRoutine</h1>
            <p>HTTP target load runner</p>
          </div>
        </div>

        <EnvironmentPanel
          profiles={environmentProfiles}
          activeProfile={activeEnvironment}
          secretBindings={activeEnvironment ? environmentSecrets[activeEnvironment.id] ?? {} : {}}
          disabled={running || stopping}
          onAdd={handleAddEnvironment}
          onDelete={handleDeleteEnvironment}
          onSelect={handleSelectEnvironment}
          onUpdate={handleUpdateEnvironment}
          onUpdateSecret={handleUpdateEnvironmentSecret}
        />

        <ScenarioLibraryPanel
          scenarios={savedScenarios}
          activeScenarioId={activeScenarioId}
          name={scenarioName}
          tagsText={scenarioTagsText}
          disabled={running || stopping}
          onNameChange={handleScenarioNameChange}
          onTagsChange={handleScenarioTagsChange}
          onNew={handleNewScenario}
          onSave={handleSaveScenario}
          onLoad={handleLoadScenario}
          onDelete={handleDeleteScenario}
          onExport={handleExportScenario}
          onImport={(file) => void handleImportScenario(file)}
        />

        <section className="panel">
          <NodeInspector
            selectedNode={selectedNode}
            updateNode={updateSelectedNodeData}
            onOpenHelp={setHelpTopic}
            authSecret={selectedNode ? authSecrets[selectedNode.id] ?? {} : {}}
            updateAuthSecret={updateAuthSecret}
          />

          <div className="actions">
            <button type="button" onClick={handleStart} disabled={running || stopping}>
              <Play size={16} /> Start
            </button>
            <button type="button" className="secondary" onClick={handleStop} disabled={!running || stopping}>
              <Square size={16} /> {stopping ? "Stopping" : "Stop"}
            </button>
          </div>
          {error ? <div className="error-banner">{error}</div> : null}
        </section>
      </aside>

      <main className="workspace">
        <AppToolbar
          canRedo={graphHistory.current.canRedo}
          canUndo={graphHistory.current.canUndo}
          onExportK6={handleExportK6}
          onOpenHelp={() => setHelpTopic("overview")}
          onRedo={handleRedo}
          onUndo={handleUndo}
          redoLabel={graphHistory.current.redoLabel}
          running={running}
          scenarioName={scenarioName}
          stopping={stopping}
          undoLabel={graphHistory.current.undoLabel}
        />

        <NodePalette
          onAddNode={handleAddNode}
          onOpenImport={handleOpenOpenAPIImport}
          onImportHar={handleImportHarFile}
          onImportPostman={handleImportPostmanFile}
        />

        <HistoryBanner
          canRedo={graphHistory.current.canRedo}
          canUndo={graphHistory.current.canUndo}
          deletedScenarioName={deletedScenario?.scenario.name}
          notice={historyNotice}
          onDismiss={() => {
            setHistoryNotice("");
            setDeletedScenario(null);
          }}
          onRedo={handleRedo}
          onUndo={handleUndo}
          onUndoDelete={handleUndoScenarioDelete}
        />

        <ScenarioPath nodes={flowNodes} validation={graphValidation} />

        <FlowCanvas
          nodes={canvasNodes}
          edges={flowEdges}
          onNodesChange={handleNodeChanges}
          onEdgesChange={handleEdgeChanges}
          onConnect={handleConnect}
          onPaneClick={handlePaneClick}
        />

        <MetricGrid />
        <MetricsChart />
      </main>

      <AppDialogs
        start={pendingSafety ? {
          safety: pendingSafety,
          request: pendingStart,
          onCancel: handleCancelStart,
          onConfirm: handleConfirmStart,
        } : null}
        help={helpTopic ? {
          topic: helpTopic,
          language: helpLanguage,
          setLanguage: setHelpLanguage,
          onClose: () => setHelpTopic(null),
        } : null}
        openAPI={openAPIImportOpen ? {
          error: openAPIImportError,
          imported: openAPIImported,
          loading: openAPIImportLoading,
          message: openAPIImportMessage,
          onCancel: handleCloseOpenAPIImport,
          onSelectEndpoint: handleSelectOpenAPIEndpoint,
          onSubmit: handleSubmitOpenAPIImport,
        } : null}
        requestImport={pendingRequestImport ? {
          key: pendingRequestImport.id,
          props: {
            appendAvailable: Boolean(graphValidation.compiled),
            error: requestImportError,
            preview: pendingRequestImport,
            onCancel: handleCancelRequestImport,
            onConfirm: handleConfirmRequestImport,
          },
        } : null}
      />
    </div>
  );
}
