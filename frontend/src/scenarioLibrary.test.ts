import { afterEach, describe, expect, it, vi } from "vitest";

import { createFlowNode, initialFlowEdges, initialFlowNodes } from "./flowModel";
import { createScenarioSnapshot } from "./scenarioPersistence";
import {
  deleteScenario,
  loadScenarioDraft,
  loadScenarioLibrary,
  parseScenarioFile,
  saveScenario,
  saveScenarioDraft,
  serializeScenarioFile,
} from "./scenarioLibrary";

describe("scenario library", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("migrates legacy recent runs into a sanitized versioned library without graph data loss", () => {
    const values = new Map<string, string>();
    const request = createFlowNode("request", 1, {
      url: "https://example.com?access_token=legacy-secret",
    });
    values.set("flowroutine:saved-scenarios", JSON.stringify([{
      id: "legacy",
      name: "Legacy scenario",
      savedAtUnixMs: 123,
      environmentProfileId: "staging",
      nodes: [request],
      edges: [],
    }]));
    stubStorage(values);

    const scenarios = loadScenarioLibrary();
    const persisted = values.get("flowroutine:scenario-library:v2") ?? "";

    expect(scenarios).toHaveLength(1);
    expect(scenarios[0]).toMatchObject({
      schemaVersion: 2,
      id: "legacy",
      name: "Legacy scenario",
      tags: [],
      createdAtUnixMs: 123,
      updatedAtUnixMs: 123,
      environmentProfileId: "staging",
    });
    expect(scenarios[0].nodes).toHaveLength(1);
    expect(persisted).toContain('"schemaVersion":2');
    expect(persisted).toContain("SECRET_ACCESS_TOKEN");
    expect(persisted).not.toContain("legacy-secret");
    expect(values.has("flowroutine:saved-scenarios")).toBe(false);
  });

  it("returns a sanitized legacy graph and removes the secret-bearing source if migration storage fails", () => {
    const request = createFlowNode("request", 1, {
      headersText: "Authorization: Bearer legacy-secret",
    });
    const removeItem = vi.fn();
    vi.stubGlobal("window", {
      localStorage: {
        getItem: (key: string) => key === "flowroutine:saved-scenarios"
          ? JSON.stringify([{
            id: "legacy",
            name: "Legacy scenario",
            savedAtUnixMs: 123,
            nodes: [request],
            edges: [],
          }])
          : null,
        setItem: () => {
          throw new Error("storage full");
        },
        removeItem,
      },
    });

    const scenarios = loadScenarioLibrary();

    expect(scenarios).toHaveLength(1);
    expect(JSON.stringify(scenarios)).toContain("SECRET_AUTHORIZATION");
    expect(JSON.stringify(scenarios)).not.toContain("legacy-secret");
    expect(removeItem).toHaveBeenCalledWith("flowroutine:saved-scenarios");
  });

  it("autosaves and restores a sanitized draft independently of library saves", () => {
    const values = new Map<string, string>();
    stubStorage(values);
    const request = createFlowNode("request", 1, {
      headersText: "Authorization: Bearer draft-secret",
    });

    saveScenarioDraft({
      activeScenarioId: "scenario-1",
      name: "Draft",
      tags: ["smoke"],
      createdAtUnixMs: 10,
      environmentProfileId: "local",
      nodes: [request],
      edges: [],
    });
    const restored = loadScenarioDraft();
    const persisted = values.get("flowroutine:scenario-draft:v2") ?? "";

    expect(restored).toMatchObject({
      schemaVersion: 2,
      activeScenarioId: "scenario-1",
      name: "Draft",
      tags: ["smoke"],
      createdAtUnixMs: 10,
      environmentProfileId: "local",
    });
    expect(persisted).toContain("SECRET_AUTHORIZATION");
    expect(persisted).not.toContain("draft-secret");
  });

  it("round-trips scenario files and rejects unsupported schemas", () => {
    const scenario = createScenarioSnapshot(initialFlowNodes, initialFlowEdges, {
      id: "scenario-1",
      name: "Checkout smoke",
      tags: ["smoke", "checkout"],
      createdAtUnixMs: 1,
    });
    const parsed = parseScenarioFile(serializeScenarioFile(scenario));
    const versionOne = parseScenarioFile(JSON.stringify({
      ...scenario,
      schemaVersion: 1,
      savedAtUnixMs: 5,
      createdAtUnixMs: undefined,
      updatedAtUnixMs: undefined,
    }));

    expect(parsed).toEqual(scenario);
    expect(versionOne).toMatchObject({
      schemaVersion: 2,
      id: "scenario-1",
      tags: ["smoke", "checkout"],
      createdAtUnixMs: 5,
      updatedAtUnixMs: 5,
    });
    expect(() => parseScenarioFile(JSON.stringify({
      schemaVersion: 99,
      scenario,
    }))).toThrow("newer than supported");
    expect(() => parseScenarioFile("not-json")).toThrow("not valid JSON");
  });

  it("upserts, sorts, and deletes named scenarios", () => {
    const values = new Map<string, string>();
    stubStorage(values);
    const older = createScenarioSnapshot(initialFlowNodes, initialFlowEdges, {
      id: "older",
      name: "Older",
      createdAtUnixMs: 1,
    });
    const newer = {
      ...createScenarioSnapshot(initialFlowNodes, initialFlowEdges, {
        id: "newer",
        name: "Newer",
        createdAtUnixMs: 2,
      }),
      updatedAtUnixMs: older.updatedAtUnixMs + 1,
    };

    const saved = saveScenario(newer, [older]);
    const remaining = deleteScenario("newer", saved);

    expect(saved.map((scenario) => scenario.id)).toEqual(["newer", "older"]);
    expect(remaining.map((scenario) => scenario.id)).toEqual(["older"]);
  });
});

function stubStorage(values: Map<string, string>) {
  vi.stubGlobal("window", {
    localStorage: {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    },
  });
}
