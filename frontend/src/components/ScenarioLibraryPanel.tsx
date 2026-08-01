import { memo, useRef } from "react";
import { Download, Plus, Save, Trash2, Upload } from "lucide-react";

import { formatSavedAt } from "../format";
import type { SavedScenario } from "../flowTypes";

export const ScenarioLibraryPanel = memo(function ScenarioLibraryPanel({
  scenarios,
  activeScenarioId,
  name,
  tagsText,
  disabled,
  onNameChange,
  onTagsChange,
  onNew,
  onSave,
  onLoad,
  onDelete,
  onExport,
  onImport,
}: {
  scenarios: SavedScenario[];
  activeScenarioId: string | null;
  name: string;
  tagsText: string;
  disabled: boolean;
  onNameChange: (name: string) => void;
  onTagsChange: (tags: string) => void;
  onNew: () => void;
  onSave: () => void;
  onLoad: (scenario: SavedScenario) => void;
  onDelete: (scenario: SavedScenario) => void;
  onExport: () => void;
  onImport: (file: File) => void;
}) {
  const importInputRef = useRef<HTMLInputElement | null>(null);
  return (
    <section className="scenario-library-panel" aria-label="Scenario library">
      <div className="scenario-library-heading">
        <div>
          <div className="eyebrow">Library</div>
          <h2>Scenarios</h2>
        </div>
        <div className="scenario-library-heading-actions">
          <button
            type="button"
            className="secondary icon-button"
            aria-label="New scenario"
            title="New scenario"
            disabled={disabled}
            onClick={onNew}
          >
            <Plus size={16} />
          </button>
          <button
            type="button"
            className="secondary icon-button"
            aria-label="Save scenario"
            title="Save scenario"
            disabled={disabled}
            onClick={onSave}
          >
            <Save size={16} />
          </button>
        </div>
      </div>

      <label>
        Name
        <input
          value={name}
          maxLength={120}
          disabled={disabled}
          onChange={(event) => onNameChange(event.target.value)}
        />
      </label>
      <label>
        Tags
        <input
          value={tagsText}
          placeholder="smoke, checkout"
          disabled={disabled}
          onChange={(event) => onTagsChange(event.target.value)}
        />
      </label>

      <div className="scenario-library-file-actions">
        <button type="button" className="secondary" disabled={disabled} onClick={() => importInputRef.current?.click()}>
          <Upload size={14} /> Import
        </button>
        <button type="button" className="secondary" disabled={disabled} onClick={onExport}>
          <Download size={14} /> Export
        </button>
        <input
          ref={importInputRef}
          type="file"
          className="hidden-file-input"
          accept=".json,.flowroutine.json,application/json"
          onChange={(event) => {
            const file = event.target.files?.[0];
            event.target.value = "";
            if (file) {
              onImport(file);
            }
          }}
        />
      </div>

      <small>Draft changes autosave locally. Save updates the named library entry.</small>
      {scenarios.length === 0 ? (
        <div className="inspector-note">Save the draft to add its first named library entry.</div>
      ) : (
        <div className="scenario-library-list">
          {scenarios.map((scenario) => (
            <div
              key={scenario.id}
              className={`scenario-library-row${scenario.id === activeScenarioId ? " active" : ""}`}
            >
              <button
                type="button"
                className="secondary scenario-library-load"
                disabled={disabled}
                onClick={() => onLoad(scenario)}
              >
                <span>{scenario.name}</span>
                <small>
                  {scenario.tags.length > 0 ? `${scenario.tags.join(" · ")} · ` : ""}
                  {formatSavedAt(scenario.updatedAtUnixMs)}
                </small>
              </button>
              <button
                type="button"
                className="secondary scenario-library-delete"
                aria-label={`Delete ${scenario.name}`}
                title={`Delete ${scenario.name}`}
                disabled={disabled}
                onClick={() => onDelete(scenario)}
              >
                <Trash2 size={14} />
              </button>
            </div>
          ))}
        </div>
      )}
    </section>
  );
});
