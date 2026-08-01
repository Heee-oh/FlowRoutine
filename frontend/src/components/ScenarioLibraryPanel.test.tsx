import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { initialFlowEdges, initialFlowNodes } from "../flowModel";
import { createScenarioSnapshot } from "../scenarioPersistence";
import { ScenarioLibraryPanel } from "./ScenarioLibraryPanel";

describe("ScenarioLibraryPanel", () => {
  it("renders active named scenarios, tags, autosave guidance, and file actions", () => {
    const scenario = createScenarioSnapshot(initialFlowNodes, initialFlowEdges, {
      id: "checkout",
      name: "Checkout smoke",
      tags: ["smoke", "checkout"],
      createdAtUnixMs: 1,
    });
    const markup = renderToStaticMarkup(
      <ScenarioLibraryPanel
        scenarios={[scenario]}
        activeScenarioId="checkout"
        name="Checkout smoke"
        tagsText="smoke, checkout"
        disabled={false}
        onNameChange={vi.fn()}
        onTagsChange={vi.fn()}
        onNew={vi.fn()}
        onSave={vi.fn()}
        onLoad={vi.fn()}
        onDelete={vi.fn()}
        onExport={vi.fn()}
        onImport={vi.fn()}
      />,
    );

    expect(markup).toContain("Checkout smoke");
    expect(markup).toContain("smoke · checkout");
    expect(markup).toContain("autosave locally");
    expect(markup).toContain("Import");
    expect(markup).toContain("Export");
  });
});
