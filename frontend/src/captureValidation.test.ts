import { describe, expect, it } from "vitest";
import { normalizeCaptureDefinition } from "./captureValidation";

describe("normalizeCaptureDefinition", () => {
  it("normalizes defaults and explicit policies", () => {
    expect(normalizeCaptureDefinition({ name: " token ", path: " $.items[01].id " })).toEqual({
      name: "token",
      path: "$.items[01].id",
      scope: "iteration",
      onStatus: "success",
    });
    expect(normalizeCaptureDefinition({
      name: "session",
      path: "$[0].id",
      scope: " RUN ",
      onStatus: " 2XX ",
    })).toEqual({
      name: "session",
      path: "$[0].id",
      scope: "run",
      onStatus: "2xx",
    });
  });

  it.each([
    "$..token",
    "$.[0]",
    ".token",
    "items]",
    "items[nope]",
    "items.[0]",
  ])("rejects malformed path %s", (path) => {
    expect(() => normalizeCaptureDefinition({ name: "token", path })).toThrow("Invalid capture path");
  });
});
