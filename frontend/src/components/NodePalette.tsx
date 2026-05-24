import { memo } from "react";
import { FileJson, Plus } from "lucide-react";
import { nodePalette } from "../flowModel";
import type { FlowNodeKind } from "../flowTypes";

export const NodePalette = memo(function NodePalette({
  onAddNode,
  onOpenImport,
}: {
  onAddNode: (kind: FlowNodeKind) => void;
  onOpenImport: () => void;
}) {
  return (
    <section className="palette">
      <div className="palette-head">
        <div>
          <div className="eyebrow">Nodes</div>
          <h2>Add to scenario</h2>
        </div>
      </div>
      <div className="palette-grid">
        <button type="button" className="secondary palette-button import-button" onClick={onOpenImport}>
          <FileJson size={15} /> Import OpenAPI
        </button>
        {nodePalette.map((item) => (
          <button key={item.kind} type="button" className="secondary palette-button" onClick={() => onAddNode(item.kind)}>
            <Plus size={15} /> {item.label}
          </button>
        ))}
      </div>
    </section>
  );
});
