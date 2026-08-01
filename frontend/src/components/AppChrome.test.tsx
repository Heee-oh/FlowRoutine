import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { AppToolbar, HistoryBanner } from "./AppChrome";

describe("application chrome", () => {
  it("preserves scenario controls and run status", () => {
    const markup = renderToStaticMarkup(
      <AppToolbar
        canRedo={false}
        canUndo
        onExportK6={vi.fn()}
        onOpenHelp={vi.fn()}
        onRedo={vi.fn()}
        onUndo={vi.fn()}
        redoLabel=""
        running
        scenarioName="Checkout"
        stopping={false}
        undoLabel="Added request"
      />,
    );

    expect(markup).toContain("Checkout");
    expect(markup).toContain("Undo: Added request");
    expect(markup).toContain("Nothing to redo");
    expect(markup).toContain("Running");
  });

  it("renders graph and scenario recovery actions", () => {
    const markup = renderToStaticMarkup(
      <HistoryBanner
        canRedo
        canUndo
        deletedScenarioName="Smoke test"
        notice=""
        onDismiss={vi.fn()}
        onRedo={vi.fn()}
        onUndo={vi.fn()}
        onUndoDelete={vi.fn()}
      />,
    );

    expect(markup).toContain("Deleted library entry: Smoke test");
    expect(markup).toContain("Undo delete");
    expect(markup).toContain("Undo graph");
    expect(markup).toContain("Redo graph");
  });
});
