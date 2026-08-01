import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { createFlowNode } from "../flowModel";
import { NodeInspector } from "./NodeInspector";

describe("NodeInspector load profile", () => {
  it("renders the staged arrival-rate preview before start", () => {
    const node = createFlowNode("engine", 1, {
      loadProfile: {
        mode: "ramping-arrival-rate",
        startTarget: 100,
        stages: [
          { durationMs: 1_000, target: 500 },
          { durationMs: 2_000, target: 1_000 },
        ],
        preAllocatedVUs: 5,
        maxVUs: 20,
        gracefulStopMs: 1_000,
      },
    });

    const markup = renderToStaticMarkup(
      <NodeInspector
        selectedNode={node}
        updateNode={vi.fn()}
        onOpenHelp={vi.fn()}
        savedScenarios={[]}
        onLoadScenario={vi.fn()}
        authSecret={{}}
        updateAuthSecret={vi.fn()}
      />,
    );

    expect(markup).toContain("Ramping arrival rate");
    expect(markup).toContain("Preview");
    expect(markup).toContain("100 iterations/s");
    expect(markup).toContain("1000 after 2s");
    expect(markup).toContain("5-20 VUs");
  });
});

describe("NodeInspector assertions", () => {
  it("renders typed JSON assertion and failure behavior controls", () => {
    const node = createFlowNode("assertion", 2, {
      assertionType: "json",
      assertionOperator: "equals",
      assertionJSONPath: "$.data.id",
      assertionExpected: "42",
      assertionValueType: "number",
      assertionFailureMode: "stop",
    });

    const markup = renderToStaticMarkup(
      <NodeInspector
        selectedNode={node}
        updateNode={vi.fn()}
        onOpenHelp={vi.fn()}
        savedScenarios={[]}
        onLoadScenario={vi.fn()}
        authSecret={{}}
        updateAuthSecret={vi.fn()}
      />,
    );

    expect(markup).toContain("JSON body");
    expect(markup).toContain("$.data.id");
    expect(markup).toContain("Number");
    expect(markup).toContain("Count and stop iteration");
  });
});
