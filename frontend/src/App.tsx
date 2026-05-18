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
import { HelpCircle, Play, Square, Workflow } from "lucide-react";
import { FlowCanvas } from "./components/FlowCanvas";
import { HelpDialog, StartConfirmDialog } from "./components/Dialogs";
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
  refreshNodeDisplay,
  reviveSavedNodes,
  saveScenario,
} from "./flowModel";
import type { FlowNodeData, FlowNodeKind, RuntimeAuthSecret, SafetyAssessment, SavedScenario } from "./flowTypes";
import type { HelpLanguage, HelpTopic } from "./help";
import { DEFAULT_METRIC_WINDOW_MS, useLoadStore, useMetricsStore } from "./store";
import type { StartRequest } from "./types";
import { onMetricsBatch, startLoad, stopLoad } from "./wails";

export function App() {
  const running = useLoadStore((state) => state.running);
  const stopping = useLoadStore((state) => state.stopping);
  const error = useLoadStore((state) => state.error);
  const setRunning = useLoadStore((state) => state.setRunning);
  const setStopping = useLoadStore((state) => state.setStopping);
  const setError = useLoadStore((state) => state.setError);
  const buildStartRequest = useLoadStore((state) => state.buildStartRequest);
  const pushBatch = useMetricsStore((state) => state.pushBatch);
  const resetMetrics = useMetricsStore((state) => state.reset);
  const setMetricWindowMs = useMetricsStore((state) => state.setMetricWindowMs);
  const [pendingStart, setPendingStart] = useState<StartRequest | null>(null);
  const [pendingSafety, setPendingSafety] = useState<SafetyAssessment | null>(null);
  const [pendingScenario, setPendingScenario] = useState<SavedScenario | null>(null);
  const [helpTopic, setHelpTopic] = useState<HelpTopic | null>(null);
  const [helpLanguage, setHelpLanguage] = useState<HelpLanguage>("ko");
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
      resetMetrics();
      setRunning(true);
      await startLoad(request);
    } catch (err) {
      setRunning(false);
      setStopping(false);
      setError(err instanceof Error ? err.message : "Failed to start load test");
    }
  }, [resetMetrics, setError, setRunning, setStopping]);

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
            <button type="button" className="secondary icon-button" aria-label="Open help" title="Help" onClick={() => setHelpTopic("overview")}>
              <HelpCircle size={17} />
            </button>
            <div className={`status-pill ${running ? "running" : ""}`}>{stopping ? "Stopping" : running ? "Running" : "Idle"}</div>
          </div>
        </header>

        <NodePalette onAddNode={handleAddNode} />

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
    </div>
  );
}
