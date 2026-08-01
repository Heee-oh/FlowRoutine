import { describe, expect, it } from "vitest";

import {
  convertLoadProfileMode,
  legacyLoadProfile,
  loadProfileSummary,
  normalizeLoadProfile,
} from "./loadProfile";

describe("load profiles", () => {
  it("migrates legacy ramp-up settings into explicit stages", () => {
    const profile = legacyLoadProfile({
      virtualUsers: 20,
      durationMs: 10_000,
      rampUpMs: 2_000,
      requestTimeoutMs: 1_500,
    });

    expect(profile).toEqual({
      mode: "ramping-vus",
      startTarget: 1,
      stages: [
        { durationMs: 2_000, target: 20 },
        { durationMs: 8_000, target: 20 },
      ],
      preAllocatedVUs: 0,
      maxVUs: 0,
      gracefulStopMs: 1_500,
    });
  });

  it("summarizes arrival-rate duration, rate, and worker capacity", () => {
    const summary = loadProfileSummary({
      mode: "ramping-arrival-rate",
      startTarget: 100,
      stages: [
        { durationMs: 1_000, target: 500 },
        { durationMs: 2_000, target: 1_000 },
      ],
      preAllocatedVUs: 10,
      maxVUs: 40,
      gracefulStopMs: 2_000,
    });

    expect(summary.durationMs).toBe(3_000);
    expect(summary.peakTarget).toBe(1_000);
    expect(summary.maxVirtualUsers).toBe(40);
    expect(summary.targetUnit).toBe("iterations/s");
  });

  it("keeps constant profiles canonical and rejects unsafe capacity", () => {
    const constant = convertLoadProfileMode(legacyLoadProfile({
      virtualUsers: 5,
      durationMs: 2_000,
      rampUpMs: 1_000,
      requestTimeoutMs: 500,
    }), "constant-arrival-rate");

    expect(normalizeLoadProfile(constant).stages).toEqual([
      { durationMs: 2_000, target: 5 },
    ]);
    expect(() => normalizeLoadProfile({
      ...constant,
      preAllocatedVUs: 10,
      maxVUs: 5,
    })).toThrow("Max VUs must be an integer between 10 and 100000");
  });

  it("clamps targets when switching from arrival rate to VUs", () => {
    const profile = convertLoadProfileMode({
      mode: "ramping-arrival-rate",
      startTarget: 1_000_000,
      stages: [{ durationMs: 1_000, target: 2_000_000 }],
      preAllocatedVUs: 10,
      maxVUs: 20,
      gracefulStopMs: 1_000,
    }, "ramping-vus");

    expect(normalizeLoadProfile(profile)).toMatchObject({
      startTarget: 100_000,
      stages: [{ target: 100_000 }],
    });
  });
});
