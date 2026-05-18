import { memo, useCallback, useRef, type WheelEvent as ReactWheelEvent } from "react";
import {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  Position,
  ReactFlow,
  type Connection,
  type Edge,
  type Node,
  type NodeChange,
  type NodeProps,
  type ReactFlowInstance,
  type useEdgesState,
  type useNodesState,
} from "@xyflow/react";
import { Trash2 } from "lucide-react";
import type { FlowNodeData } from "../flowTypes";

const minFlowZoom = 0.65;
const maxFlowZoom = 1.8;
const wheelZoomStep = 1.4;

const nodeTypes = {
  flowNode: memo(function FlowNode({ id, data }: NodeProps<Node<FlowNodeData>>) {
    return (
      <div className={`flow-node flow-node-${data.tone}`}>
        <button
          type="button"
          className="node-delete nodrag"
          aria-label="Delete node"
          onClick={(event) => {
            event.stopPropagation();
            data.onDelete?.(id);
          }}
        >
          <Trash2 size={13} />
        </button>
        <Handle type="target" position={Position.Left} />
        <span>{data.label}</span>
        <strong>{data.value}</strong>
        <small>{data.caption}</small>
        <Handle type="source" position={Position.Right} />
      </div>
    );
  }),
};

export const FlowCanvas = memo(function FlowCanvas({
  nodes,
  edges,
  onNodesChange,
  onEdgesChange,
  onConnect,
  onPaneClick,
}: {
  nodes: Node<FlowNodeData>[];
  edges: Edge[];
  onNodesChange: ReturnType<typeof useNodesState<Node<FlowNodeData>>>[2];
  onEdgesChange: ReturnType<typeof useEdgesState>[2];
  onConnect: (connection: Connection) => void;
  onPaneClick: () => void;
}) {
  const flowInstance = useRef<ReactFlowInstance<Node<FlowNodeData>, Edge> | null>(null);
  const handlePaneScroll = useCallback((event?: ReactWheelEvent) => {
    if (!event || event.deltaY === 0) {
      return;
    }
    event.preventDefault();
    const instance = flowInstance.current;
    if (!instance) {
      return;
    }
    const currentZoom = instance.getZoom();
    const nextZoom = event.deltaY < 0
      ? Math.min(maxFlowZoom, currentZoom * wheelZoomStep)
      : Math.max(minFlowZoom, currentZoom / wheelZoomStep);
    void instance.zoomTo(nextZoom, { duration: 80 });
  }, []);

  return (
    <section className="flow-shell">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onPaneClick={onPaneClick}
        onInit={(instance) => {
          flowInstance.current = instance;
        }}
        onPaneScroll={handlePaneScroll}
        defaultViewport={{ x: 34, y: 22, zoom: 0.92 }}
        minZoom={minFlowZoom}
        maxZoom={maxFlowZoom}
        nodesDraggable
        nodesConnectable
        edgesFocusable
        elementsSelectable
        deleteKeyCode={["Backspace", "Delete"]}
        elevateNodesOnSelect={false}
        elevateEdgesOnSelect
        panOnScroll={false}
        zoomOnScroll={false}
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={32} size={1} color="#263241" />
        <Controls showInteractive={false} />
      </ReactFlow>
    </section>
  );
});
