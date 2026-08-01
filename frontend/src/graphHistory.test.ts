import { describe, expect, it } from "vitest";

import { BoundedHistory, createGraphSnapshot, type GraphSnapshot } from "./graphHistory";
import { createFlowNode } from "./flowModel";

describe("BoundedHistory", () => {
  it("supports bounded undo/redo and clears redo after a new destructive action", () => {
    const history = new BoundedHistory<number>(2);
    history.record(1, "one");
    history.record(2, "two");
    history.record(3, "three");

    expect(history.undo(4)).toEqual({ label: "three", value: 3 });
    expect(history.undo(3)).toEqual({ label: "two", value: 2 });
    expect(history.undo(2)).toBeNull();
    expect(history.redo(2)).toEqual({ label: "two", value: 3 });

    history.record(9, "new");
    expect(history.canRedo).toBe(false);
    expect(history.undoLabel).toBe("new");
  });

  it("invalidates redo when a newer non-history edit is applied", () => {
    const history = new BoundedHistory<number>();
    history.record(1, "delete");
    expect(history.undo(2)).not.toBeNull();

    expect(history.clearRedo()).toBe(true);
    expect(history.canRedo).toBe(false);
    expect(history.clearRedo()).toBe(false);
  });

  it("clones graph state without persisting React callbacks", () => {
    const node = createFlowNode("request", 1, null, undefined, () => undefined);
    const snapshot: GraphSnapshot = {
      nodes: [node],
      edges: [],
      authSecrets: { [node.id]: { token: "memory-only" } },
      selectedNodeId: node.id,
      nextNodeIndex: 2,
      scenario: {
        activeScenarioId: "scenario-1",
        name: "Scenario",
        tagsText: "smoke",
        createdAtUnixMs: 1,
        activeEnvironmentId: "staging",
      },
    };

    const cloned = createGraphSnapshot(snapshot);
    node.data.label = "changed";
    snapshot.authSecrets[node.id].token = "changed";

    expect(cloned.nodes[0].data.label).not.toBe("changed");
    expect(cloned.nodes[0].data.onDelete).toBeUndefined();
    expect(cloned.authSecrets[node.id].token).toBe("memory-only");
  });
});
