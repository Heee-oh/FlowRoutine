import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  addEdge,
  type Connection,
  type Edge,
  type Node,
  type NodeChange,
  useEdgesState,
  useNodesState,
} from "@xyflow/react";
import { FileCode, HelpCircle, Play, Square, Workflow } from "lucide-react";
import { EnvironmentPanel } from "./components/EnvironmentPanel";
import { FlowCanvas } from "./components/FlowCanvas";
import { HelpDialog, OpenAPIImportDialog, StartConfirmDialog } from "./components/Dialogs";
import { ImportPreviewDialog } from "./components/ImportPreviewDialog";
import { MetricGrid, MetricsChart } from "./components/Metrics";
import { NodeInspector } from "./components/NodeInspector";
import { NodePalette } from "./components/NodePalette";
import { ScenarioPath } from "./components/ScenarioPath";
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
  createSavedScenario,
  getMetricWindowMs,
  initialFlowEdges,
  initialFlowNodes,
  loadSavedScenarios,
  nextNodeIndexFromNodes,
  openAPIEndpointToRequestSettings,
  refreshNodeDisplay,
  reviveSavedNodes,
  saveScenario,
} from "./flowModel";
import type {
  EnvironmentProfile,
  FlowNodeData,
  FlowNodeKind,
  RuntimeAuthSecret,
  SafetyAssessment,
  SavedScenario,
} from "./flowTypes";
import { validateScenarioGraph } from "./graphCompiler";
import { parseHarArchive } from "./harImport";
import type { HelpLanguage, HelpTopic } from "./help";
import {
  appendImportedRequestsToGraph,
  buildReplacementImportGraph,
  type ImportedRequest,
  type RequestImportMode,
  type RequestImportPreview,
  type RequestImportSource,
} from "./importGraph";
import { assertImportFileSize } from "./importValidation";
import { downloadK6Script } from "./k6Export";
import { parsePostmanCollection } from "./postmanImport";
import { purgeLegacyRunBaselines } from "./report";
import { DEFAULT_METRIC_WINDOW_MS, useLoadStore, useMetricsStore } from "./store";
import type {
  OpenAPIEndpoint,
  OpenAPIImportRequest,
  OpenAPIImportResponse,
  StartRequest,
} from "./types";
import { importOpenAPI, onMetricsBatch, preflightLoad, startLoad, stopLoad } from "./wails";

type ImportUndo = {
  authSecrets: Record<string, RuntimeAuthSecret>;
  edges: Edge[];
  message: string;
  nextNodeIndex: number;
  nodes: Node<FlowNodeData>[];
  selectedNodeId: string | null;
};

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
  const [pendingStart, setPendingStart] = useState<StartRequest | null>(null);
  const [pendingSafety, setPendingSafety] = useState<SafetyAssessment | null>(null);
  const [pendingScenario, setPendingScenario] = useState<SavedScenario | null>(null);
  const [helpTopic, setHelpTopic] = useState<HelpTopic | null>(null);
  const [helpLanguage, setHelpLanguage] = useState<HelpLanguage>("ko");
  const [openAPIImportOpen, setOpenAPIImportOpen] = useState(false);
  const [openAPIImportLoading, setOpenAPIImportLoading] = useState(false);
  const [openAPIImportError, setOpenAPIImportError] = useState("");
  const [openAPIImportMessage, setOpenAPIImportMessage] = useState("");
  const [openAPIImported, setOpenAPIImported] = useState<OpenAPIImportResponse | null>(null);
  const [pendingRequestImport, setPendingRequestImport] = useState<RequestImportPreview | null>(null);
  const [requestImportError, setRequestImportError] = useState("");
  const [importUndo, setImportUndo] = useState<ImportUndo | null>(null);
  const [savedScenarios, setSavedScenarios] = useState<SavedScenario[]>(loadSavedScenarios);
  const [authSecrets, setAuthSecrets] = useState<Record<string, RuntimeAuthSecret>>({});
  const [environmentProfiles, setEnvironmentProfiles] = useState<EnvironmentProfile[]>(loadEnvironmentProfiles);
  const [activeEnvironmentId, setActiveEnvironmentId] = useState<string | null>(loadActiveEnvironmentId);
  const [environmentSecrets, setEnvironmentSecrets] = useState<Record<string, Record<string, string>>>({});
  const [flowNodes, setFlowNodes, onFlowNodesChange] = useNodesState<Node<FlowNodeData>>(initialFlowNodes);
  const [flowEdges, setFlowEdges, onFlowEdgesChange] = useEdgesState<Edge>(initialFlowEdges);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const nextNodeIndex = useRef(initialFlowNodes.length);
  const nextRequestImportId = useRef(1);
  const selectedNode = useMemo(
    () => flowNodes.find((node) => node.id === selectedNodeId) ?? null,
    [flowNodes, selectedNodeId],
  );
  const activeEnvironment = useMemo(
    () => environmentProfiles.find((profile) => profile.id === activeEnvironmentId) ?? null,
    [activeEnvironmentId, environmentProfiles],
  );
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

  const handleDeleteNode = useCallback((nodeId: string) => {
    setFlowNodes((currentNodes) => currentNodes.filter((node) => node.id !== nodeId));
    setFlowEdges((currentEdges) => currentEdges.filter((edge) => edge.source !== nodeId && edge.target !== nodeId));
    setSelectedNodeId((current) => (current === nodeId ? null : current));
    setAuthSecrets((current) => {
      const { [nodeId]: _deleted, ...rest } = current;
      return rest;
    });
  }, [setFlowEdges, setFlowNodes]);

  useEffect(() => {
    setFlowNodes((currentNodes) => currentNodes.map((node) => ({
      ...node,
      data: {
        ...node.data,
        onDelete: handleDeleteNode,
      },
    })));
  }, [handleDeleteNode, setFlowNodes]);

  const handleAddNode = useCallback((kind: FlowNodeKind) => {
    const index = nextNodeIndex.current;
    nextNodeIndex.current += 1;
    setFlowNodes((currentNodes) => currentNodes.concat(createFlowNode(kind, index, null, undefined, handleDeleteNode)));
  }, [handleDeleteNode, setFlowNodes]);

  const handleSelectEnvironment = useCallback((id: string | null) => {
    setActiveEnvironmentId(id);
    saveActiveEnvironmentId(id);
  }, []);

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

  const handleLoadScenario = useCallback((scenario: SavedScenario) => {
    const nodes = reviveSavedNodes(scenario.nodes, handleDeleteNode);
    setFlowNodes(nodes);
    setFlowEdges(scenario.edges);
    setSelectedNodeId(null);
    setAuthSecrets({});
    if (scenario.environmentProfileId) {
      if (environmentProfiles.some((profile) => profile.id === scenario.environmentProfileId)) {
        handleSelectEnvironment(scenario.environmentProfileId);
      } else {
        handleSelectEnvironment(null);
        setError("The environment profile saved with this scenario is unavailable; select another profile.");
      }
    }
    nextNodeIndex.current = nextNodeIndexFromNodes(nodes);
  }, [environmentProfiles, handleDeleteNode, handleSelectEnvironment, setError, setFlowEdges, setFlowNodes]);

  const updateAuthSecret = useCallback((nodeId: string, patch: Partial<RuntimeAuthSecret>) => {
    setAuthSecrets((current) => ({
      ...current,
      [nodeId]: {
        ...current[nodeId],
        ...patch,
      },
    }));
  }, []);

  const handleConnect = useCallback((connection: Connection) => {
    setFlowEdges((currentEdges) => addEdge(connection, currentEdges));
  }, [setFlowEdges]);

  const handleNodeChanges = useCallback((changes: NodeChange<Node<FlowNodeData>>[]) => {
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
  }, [onFlowNodesChange, selectedNodeId]);

  const handlePaneClick = useCallback(() => {
    setSelectedNodeId(null);
  }, []);

  const updateSelectedNodeData = useCallback((patch: Partial<FlowNodeData>) => {
    if (!selectedNodeId) {
      return;
    }
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
  }, [selectedNodeId, setFlowNodes]);

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
      const scenario = createSavedScenario(flowNodes, flowEdges, request, activeEnvironment?.id);
      const assessment = assessStartSafety(request.config, preflight);
      if (assessment.confirmationRequired) {
        setPendingStart(request);
        setPendingSafety(assessment);
        setPendingScenario(scenario);
        return;
      }
      setSavedScenarios(saveScenario(scenario));
      await executeStart(request);
    } catch (err) {
      setRunning(false);
      setStopping(false);
      setError(err instanceof Error ? err.message : "Failed to start load test");
    }
  }, [activeEnvironment, assertGraphValid, authSecrets, buildStartRequest, environmentSecrets, executeStart, flowEdges, flowNodes, setError, setRunning, setStopping]);

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
      setSavedScenarios(saveScenario(scenario));
    }
    void executeStart(request);
  }, [executeStart, pendingScenario, pendingStart]);

  const handleCancelStart = useCallback(() => {
    setPendingStart(null);
    setPendingSafety(null);
    setPendingScenario(null);
  }, []);

  const handleOpenOpenAPIImport = useCallback(() => {
    setOpenAPIImportError("");
    setOpenAPIImportMessage("");
    setOpenAPIImported(null);
    setOpenAPIImportOpen(true);
  }, []);

  const handleCloseOpenAPIImport = useCallback(() => {
    if (!openAPIImportLoading) {
      setOpenAPIImportOpen(false);
      setOpenAPIImportError("");
      setOpenAPIImportMessage("");
      setOpenAPIImported(null);
    }
  }, [openAPIImportLoading]);

  const handleSubmitOpenAPIImport = useCallback(async (request: OpenAPIImportRequest) => {
    setOpenAPIImportLoading(true);
    setOpenAPIImportError("");
    setOpenAPIImportMessage("");
    setOpenAPIImported(null);
    try {
      const imported = await importOpenAPI(request);
      setOpenAPIImported(imported);
      setOpenAPIImportMessage(`Loaded ${imported.title || "OpenAPI document"} (${imported.openapi}) with ${imported.endpoints.length} endpoints`);
    } catch (err) {
      setOpenAPIImportError(err instanceof Error ? err.message : "Failed to load OpenAPI document");
    } finally {
      setOpenAPIImportLoading(false);
    }
  }, []);

  const handleSelectOpenAPIEndpoint = useCallback((endpoint: OpenAPIEndpoint) => {
    if (!openAPIImported) {
      return;
    }
    const index = nextNodeIndex.current;
    nextNodeIndex.current += 1;
    const node = createFlowNode(
      "request",
      index,
      openAPIEndpointToRequestSettings(endpoint, openAPIImported.sourceUrl),
      undefined,
      handleDeleteNode,
    );
    setFlowNodes((currentNodes) => currentNodes.concat(node));
    setSelectedNodeId(node.id);
    setOpenAPIImportOpen(false);
    setOpenAPIImportError("");
    setOpenAPIImportMessage("");
    setOpenAPIImported(null);
  }, [handleDeleteNode, openAPIImported, setFlowNodes]);

  const prepareRequestImport = useCallback(async (source: RequestImportSource, file: File) => {
    const label = source === "Postman" ? "Postman collection" : "HAR file";
    try {
      setError("");
      setRequestImportError("");
      assertImportFileSize(file.size, label);
      const raw = await file.text();
      const requests = source === "Postman"
        ? parsePostmanCollection(raw)
        : parseHarArchive(raw);
      setPendingRequestImport({
        id: nextRequestImportId.current,
        fileName: file.name,
        fileSize: file.size,
        requests,
        source,
      });
      nextRequestImportId.current += 1;
    } catch (err) {
      setPendingRequestImport(null);
      setError(err instanceof Error ? err.message : `Failed to read ${label}`);
    }
  }, [setError]);

  const handleImportPostmanFile = useCallback((file: File) => {
    void prepareRequestImport("Postman", file);
  }, [prepareRequestImport]);

  const handleImportHarFile = useCallback((file: File) => {
    void prepareRequestImport("HAR", file);
  }, [prepareRequestImport]);

  const handleCancelRequestImport = useCallback(() => {
    setPendingRequestImport(null);
    setRequestImportError("");
  }, []);

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
      setImportUndo({
        authSecrets,
        edges: flowEdges,
        message: `${mode === "replace" ? "Replaced the graph with" : "Appended"} ${requests.length} ${pendingRequestImport.source} request${requests.length === 1 ? "" : "s"}.`,
        nextNodeIndex: nextNodeIndex.current,
        nodes: flowNodes,
        selectedNodeId,
      });
      setFlowNodes(importedGraph.nodes);
      setFlowEdges(importedGraph.edges);
      setSelectedNodeId(importedGraph.selectedNodeId);
      if (mode === "replace") {
        setAuthSecrets({});
      }
      nextNodeIndex.current = importedGraph.nextNodeIndex;
      setPendingRequestImport(null);
    } catch (err) {
      setRequestImportError(err instanceof Error ? err.message : "Failed to apply request import");
    }
  }, [authSecrets, flowEdges, flowNodes, handleDeleteNode, pendingRequestImport, selectedNodeId, setError, setFlowEdges, setFlowNodes]);

  const handleUndoRequestImport = useCallback(() => {
    if (!importUndo) {
      return;
    }
    setFlowNodes(importUndo.nodes);
    setFlowEdges(importUndo.edges);
    setSelectedNodeId(importUndo.selectedNodeId);
    setAuthSecrets(importUndo.authSecrets);
    nextNodeIndex.current = importUndo.nextNodeIndex;
    setImportUndo(null);
    setError("");
  }, [importUndo, setError, setFlowEdges, setFlowNodes]);

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

        <section className="panel">
          <NodeInspector
            selectedNode={selectedNode}
            updateNode={updateSelectedNodeData}
            onOpenHelp={setHelpTopic}
            savedScenarios={savedScenarios}
            onLoadScenario={handleLoadScenario}
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
        <header className="toolbar">
          <div>
            <div className="eyebrow">Scenario</div>
            <h2>HTTP target stress flow</h2>
          </div>
          <div className="toolbar-actions">
            <button
              type="button"
              className="secondary icon-button"
              aria-label="Export k6 script"
              title="Export k6 script"
              disabled={running || stopping}
              onClick={handleExportK6}
            >
              <FileCode size={17} />
            </button>
            <button type="button" className="secondary icon-button" aria-label="Open help" title="Help" onClick={() => setHelpTopic("overview")}>
              <HelpCircle size={17} />
            </button>
            <div className={`status-pill ${running ? "running" : ""}`}>{stopping ? "Stopping" : running ? "Running" : "Idle"}</div>
          </div>
        </header>

        <NodePalette
          onAddNode={handleAddNode}
          onOpenImport={handleOpenOpenAPIImport}
          onImportHar={handleImportHarFile}
          onImportPostman={handleImportPostmanFile}
        />

        {importUndo ? (
          <div className="import-undo" role="status">
            <span>{importUndo.message}</span>
            <div>
              <button type="button" className="secondary" onClick={handleUndoRequestImport}>Undo import</button>
              <button type="button" className="secondary" onClick={() => setImportUndo(null)}>Dismiss</button>
            </div>
          </div>
        ) : null}

        <ScenarioPath nodes={flowNodes} validation={graphValidation} />

        <FlowCanvas
          nodes={canvasNodes}
          edges={flowEdges}
          onNodesChange={handleNodeChanges}
          onEdgesChange={onFlowEdgesChange}
          onConnect={handleConnect}
          onPaneClick={handlePaneClick}
        />

        <MetricGrid />
        <MetricsChart />
      </main>

      {pendingSafety ? (
        <StartConfirmDialog
          safety={pendingSafety}
          request={pendingStart}
          onCancel={handleCancelStart}
          onConfirm={handleConfirmStart}
        />
      ) : null}
      {helpTopic ? (
        <HelpDialog
          topic={helpTopic}
          language={helpLanguage}
          setLanguage={setHelpLanguage}
          onClose={() => setHelpTopic(null)}
        />
      ) : null}
      {openAPIImportOpen ? (
        <OpenAPIImportDialog
          error={openAPIImportError}
          imported={openAPIImported}
          loading={openAPIImportLoading}
          message={openAPIImportMessage}
          onCancel={handleCloseOpenAPIImport}
          onSelectEndpoint={handleSelectOpenAPIEndpoint}
          onSubmit={handleSubmitOpenAPIImport}
        />
      ) : null}
      {pendingRequestImport ? (
        <ImportPreviewDialog
          key={pendingRequestImport.id}
          appendAvailable={Boolean(graphValidation.compiled)}
          error={requestImportError}
          preview={pendingRequestImport}
          onCancel={handleCancelRequestImport}
          onConfirm={handleConfirmRequestImport}
        />
      ) : null}
    </div>
  );
}
