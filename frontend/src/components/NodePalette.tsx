import { memo, useRef } from "react";
import { FileJson, Plus, Upload } from "lucide-react";
import { nodePalette } from "../flowModel";
import type { FlowNodeKind } from "../flowTypes";

export const NodePalette = memo(function NodePalette({
  onAddNode,
  onOpenImport,
  onImportHar,
  onImportPostman,
}: {
  onAddNode: (kind: FlowNodeKind) => void;
  onOpenImport: () => void;
  onImportHar: (file: File) => void;
  onImportPostman: (file: File) => void;
}) {
  const harInputRef = useRef<HTMLInputElement | null>(null);
  const postmanInputRef = useRef<HTMLInputElement | null>(null);
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
        <button type="button" className="secondary palette-button import-button" onClick={() => postmanInputRef.current?.click()}>
          <Upload size={15} /> Import Postman
        </button>
        <button type="button" className="secondary palette-button import-button" onClick={() => harInputRef.current?.click()}>
          <Upload size={15} /> Import HAR
        </button>
        <input
          ref={postmanInputRef}
          type="file"
          className="hidden-file-input"
          accept=".json,application/json"
          onChange={(event) => {
            const file = event.target.files?.[0];
            event.target.value = "";
            if (file) {
              onImportPostman(file);
            }
          }}
        />
        <input
          ref={harInputRef}
          type="file"
          className="hidden-file-input"
          accept=".har,.json,application/json"
          onChange={(event) => {
            const file = event.target.files?.[0];
            event.target.value = "";
            if (file) {
              onImportHar(file);
            }
          }}
        />
        {nodePalette.map((item) => (
          <button key={item.kind} type="button" className="secondary palette-button" onClick={() => onAddNode(item.kind)}>
            <Plus size={15} /> {item.label}
          </button>
        ))}
      </div>
    </section>
  );
});
