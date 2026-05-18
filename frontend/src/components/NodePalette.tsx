import { memo } from "react";
import { Plus } from "lucide-react";
import { nodePalette } from "../flowModel";
import type { FlowNodeKind } from "../flowTypes";

export const NodePalette = memo(function NodePalette({ onAddNode }: { onAddNode: (kind: FlowNodeKind) => void }) {
  return (
    <section className="palette">
      <div className="palette-head">
        <div>
          <div className="eyebrow">Nodes</div>
          <h2>Add to scenario</h2>
        </div>
      </div>
      <div className="palette-grid">
        {nodePalette.map((item) => (
          <button key={item.kind} type="button" className="secondary palette-button" onClick={() => onAddNode(item.kind)}>
            <Plus size={15} /> {item.label}
          </button>
        ))}
      </div>
    </section>
  );
});
