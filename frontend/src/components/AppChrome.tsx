import { memo, type ComponentProps } from "react";
import { FileCode, HelpCircle, Redo2, Undo2 } from "lucide-react";

import { HelpDialog, OpenAPIImportDialog, StartConfirmDialog } from "./Dialogs";
import { ImportPreviewDialog } from "./ImportPreviewDialog";

type AppToolbarProps = {
  canRedo: boolean;
  canUndo: boolean;
  onExportK6: () => void;
  onOpenHelp: () => void;
  onRedo: () => void;
  onUndo: () => void;
  redoLabel: string;
  running: boolean;
  scenarioName: string;
  stopping: boolean;
  undoLabel: string;
};

export const AppToolbar = memo(function AppToolbar({
  canRedo,
  canUndo,
  onExportK6,
  onOpenHelp,
  onRedo,
  onUndo,
  redoLabel,
  running,
  scenarioName,
  stopping,
  undoLabel,
}: AppToolbarProps) {
  const disabled = running || stopping;
  return (
    <header className="toolbar">
      <div>
        <div className="eyebrow">Scenario</div>
        <h2>{scenarioName || "Untitled scenario"}</h2>
      </div>
      <div className="toolbar-actions">
        <button
          type="button"
          className="secondary icon-button"
          aria-label="Undo graph change"
          title={canUndo ? `Undo: ${undoLabel}` : "Nothing to undo"}
          disabled={disabled || !canUndo}
          onClick={onUndo}
        >
          <Undo2 size={17} />
        </button>
        <button
          type="button"
          className="secondary icon-button"
          aria-label="Redo graph change"
          title={canRedo ? `Redo: ${redoLabel}` : "Nothing to redo"}
          disabled={disabled || !canRedo}
          onClick={onRedo}
        >
          <Redo2 size={17} />
        </button>
        <button
          type="button"
          className="secondary icon-button"
          aria-label="Export k6 script"
          title="Export k6 script"
          disabled={disabled}
          onClick={onExportK6}
        >
          <FileCode size={17} />
        </button>
        <button type="button" className="secondary icon-button" aria-label="Open help" title="Help" onClick={onOpenHelp}>
          <HelpCircle size={17} />
        </button>
        <div className={`status-pill ${running ? "running" : ""}`}>
          {stopping ? "Stopping" : running ? "Running" : "Idle"}
        </div>
      </div>
    </header>
  );
});

type HistoryBannerProps = {
  canRedo: boolean;
  canUndo: boolean;
  deletedScenarioName?: string;
  notice: string;
  onDismiss: () => void;
  onRedo: () => void;
  onUndo: () => void;
  onUndoDelete: () => void;
};

export const HistoryBanner = memo(function HistoryBanner({
  canRedo,
  canUndo,
  deletedScenarioName,
  notice,
  onDismiss,
  onRedo,
  onUndo,
  onUndoDelete,
}: HistoryBannerProps) {
  if (!notice && !deletedScenarioName) {
    return null;
  }
  return (
    <div className="import-undo" role="status">
      <span>{notice || `Deleted library entry: ${deletedScenarioName}`}</span>
      <div>
        {deletedScenarioName ? (
          <button type="button" className="secondary" onClick={onUndoDelete}>Undo delete</button>
        ) : null}
        {canUndo ? <button type="button" className="secondary" onClick={onUndo}>Undo graph</button> : null}
        {canRedo ? <button type="button" className="secondary" onClick={onRedo}>Redo graph</button> : null}
        <button type="button" className="secondary" onClick={onDismiss}>Dismiss</button>
      </div>
    </div>
  );
});

type AppDialogsProps = {
  help: ComponentProps<typeof HelpDialog> | null;
  openAPI: ComponentProps<typeof OpenAPIImportDialog> | null;
  requestImport: {
    key: number;
    props: ComponentProps<typeof ImportPreviewDialog>;
  } | null;
  start: ComponentProps<typeof StartConfirmDialog> | null;
};

export function AppDialogs({ help, openAPI, requestImport, start }: AppDialogsProps) {
  return (
    <>
      {start ? <StartConfirmDialog {...start} /> : null}
      {help ? <HelpDialog {...help} /> : null}
      {openAPI ? <OpenAPIImportDialog {...openAPI} /> : null}
      {requestImport ? <ImportPreviewDialog key={requestImport.key} {...requestImport.props} /> : null}
    </>
  );
}
