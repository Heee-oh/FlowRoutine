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
import { FlowCanvas } from "./components/FlowCanvas";
import { HelpDialog, OpenAPIImportDialog, StartConfirmDialog } from "./components/Dialogs";
import { MetricGrid, MetricsChart } from "./components/Metrics";
import { NodeInspector } from "./components/NodeInspector";
import { NodePalette } from "./components/NodePalette";
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
import type { FlowNodeData, FlowNodeKind, RuntimeAuthSecret, SafetyAssessment, SavedScenario } from "./flowTypes";
import { parseHarArchive } from "./harImport";
import type { HelpLanguage, HelpTopic } from "./help";
import { downloadK6Script } from "./k6Export";
import { parsePostmanCollection } from "./postmanImport";
import { DEFAULT_METRIC_WINDOW_MS, useLoadStore, useMetricsStore } from "./store";
import type { OpenAPIEndpoint, OpenAPIImportResponse, StartRequest } from "./types";
import { importOpenAPI, onMetricsBatch, startLoad, stopLoad } from "./wails";

type ImportedRequest = {
  name: string;
  settings: Partial<FlowNodeData>;
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
  const [savedScenarios, setSavedScenarios] = useState<SavedScenario[]>(loadSavedScenarios);
  const [authSecrets, setAuthSecrets] = useState<Record<string, RuntimeAuthSecret>>({});
  const [flowNodes, setFlowNodes, onFlowNodesChange] = useNodesState<Node<FlowNodeData>>(initialFlowNodes);
  const [flowEdges, setFlowEdges, onFlowEdgesChange] = useEdgesState<Edge>(initialFlowEdges);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const nextNodeIndex = useRef(initialFlowNodes.length);
  const selectedNode = useMemo(
    () => flowNodes.find((node) => node.id === selectedNodeId) ?? null,
    [flowNodes, selectedNodeId],
  );
  const activeMetricWindowMs = useMemo(
    () => getMetricWindowMs(flowNodes, flowEdges) || DEFAULT_METRIC_WINDOW_MS,
    [flowEdges, flowNodes],
  );

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

  const handleLoadScenario = useCallback((scenario: SavedScenario) => {
    const nodes = reviveSavedNodes(scenario.nodes, handleDeleteNode);
    setFlowNodes(nodes);
    setFlowEdges(scenario.edges);
    setSelectedNodeId(null);
    setAuthSecrets({});
    nextNodeIndex.current = nextNodeIndexFromNodes(nodes);
  }, [handleDeleteNode, setFlowEdges, setFlowNodes]);

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
      const request = buildStartRequestFromGraph(flowNodes, flowEdges, buildStartRequest(), authSecrets);
      const scenario = createSavedScenario(flowNodes, flowEdges, request);
      const assessment = assessStartSafety(request.config);
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
  }, [authSecrets, buildStartRequest, executeStart, flowEdges, flowNodes, setError, setRunning, setStopping]);

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
      const request = buildStartRequestFromGraph(flowNodes, flowEdges, buildStartRequest(), authSecrets);
      downloadK6Script(request);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to export k6 script");
    }
  }, [authSecrets, buildStartRequest, flowEdges, flowNodes, setError]);

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

  const handleSubmitOpenAPIImport = useCallback(async (url: string) => {
    setOpenAPIImportLoading(true);
    setOpenAPIImportError("");
    setOpenAPIImportMessage("");
    setOpenAPIImported(null);
    try {
      const imported = await importOpenAPI(url);
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

  const replaceGraphWithImportedRequests = useCallback((importedRequests: ImportedRequest[]) => {
    const requestNodes = importedRequests.map((item, index) => {
      const node = createFlowNode(
        "request",
        index,
        item.settings,
        { x: 20 + index * 240, y: 80 },
        handleDeleteNode,
      );
      return {
        ...node,
        data: {
          ...node.data,
          label: item.name.slice(0, 42) || "Request",
        },
      };
    });
    const engineIndex = requestNodes.length;
    const metricsIndex = engineIndex + 1;
    const windowIndex = engineIndex + 2;
    const engineNode = createFlowNode("engine", engineIndex, null, { x: 20 + engineIndex * 240, y: 80 }, handleDeleteNode);
    const metricsNode = createFlowNode("metrics", metricsIndex, null, { x: 20 + metricsIndex * 240, y: 80 }, handleDeleteNode);
    const windowNode = createFlowNode("window", windowIndex, null, { x: 20 + metricsIndex * 240, y: 215 }, handleDeleteNode);
    const edges: Edge[] = requestNodes.map((node, index) => ({
      id: index === requestNodes.length - 1 ? `${node.id}-${engineNode.id}` : `${node.id}-${requestNodes[index + 1].id}`,
      source: node.id,
      target: index === requestNodes.length - 1 ? engineNode.id : requestNodes[index + 1].id,
    }));
    edges.push(
      { id: `${engineNode.id}-${metricsNode.id}`, source: engineNode.id, target: metricsNode.id },
      { id: `${metricsNode.id}-${windowNode.id}`, source: metricsNode.id, target: windowNode.id },
    );
    const nodes = requestNodes.concat(engineNode, metricsNode, windowNode);
    setFlowNodes(nodes);
    setFlowEdges(edges);
    setSelectedNodeId(requestNodes[0]?.id ?? null);
    setAuthSecrets({});
    nextNodeIndex.current = nodes.length;
  }, [handleDeleteNode, setFlowEdges, setFlowNodes]);

  const handleImportPostmanFile = useCallback(async (file: File) => {
    try {
      setError("");
      replaceGraphWithImportedRequests(parsePostmanCollection(await file.text()));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to import Postman collection");
    }
  }, [replaceGraphWithImportedRequests, setError]);

  const handleImportHarFile = useCallback(async (file: File) => {
    try {
      setError("");
      replaceGraphWithImportedRequests(parseHarArchive(await file.text()));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to import HAR file");
    }
  }, [replaceGraphWithImportedRequests, setError]);

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

        <FlowCanvas
          nodes={flowNodes}
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
    </div>
  );
}
