import type { LoadMode, LoadProfile } from "./types";

export const loadModeOptions: Array<{ value: LoadMode; label: string }> = [
  { value: "constant-vus", label: "Constant VUs" },
  { value: "ramping-vus", label: "Ramping VUs" },
  { value: "constant-arrival-rate", label: "Constant arrival rate" },
  { value: "ramping-arrival-rate", label: "Ramping arrival rate" },
];

const maxDurationMs = 3_600_000;
const maxGracefulStopMs = 300_000;
const maxStages = 64;
const maxVirtualUsers = 100_000;
const maxArrivalRate = 10_000_000;

export function isArrivalMode(mode: LoadMode) {
  return mode === "constant-arrival-rate" || mode === "ramping-arrival-rate";
}

export function isRampingMode(mode: LoadMode) {
  return mode === "ramping-vus" || mode === "ramping-arrival-rate";
}

export function legacyLoadProfile({
  virtualUsers,
  durationMs,
  rampUpMs,
  requestTimeoutMs,
}: {
  virtualUsers: number;
  durationMs: number;
  rampUpMs: number;
  requestTimeoutMs: number;
}): LoadProfile {
  const vus = boundedInteger(virtualUsers, 1, maxVirtualUsers, 1);
  const duration = boundedInteger(durationMs, 1, maxDurationMs, 1_000);
  const ramp = boundedInteger(rampUpMs, 0, duration, 0);
  const gracefulStopMs = boundedInteger(requestTimeoutMs, 1, maxGracefulStopMs, 1_000);
  if (ramp > 0 && vus > 1) {
    const stages = [{ durationMs: ramp, target: vus }];
    if (duration > ramp) {
      stages.push({ durationMs: duration - ramp, target: vus });
    }
    return {
      mode: "ramping-vus",
      startTarget: 1,
      stages,
      preAllocatedVUs: 0,
      maxVUs: 0,
      gracefulStopMs,
    };
  }
  return {
    mode: "constant-vus",
    startTarget: vus,
    stages: [{ durationMs: duration, target: vus }],
    preAllocatedVUs: 0,
    maxVUs: 0,
    gracefulStopMs,
  };
}

export function normalizeLoadProfile(profile: LoadProfile): LoadProfile {
  if (!loadModeOptions.some((option) => option.value === profile.mode)) {
    throw new Error(`Unsupported load profile mode: ${String(profile.mode)}`);
  }
  if (profile.stages.length < 1 || profile.stages.length > maxStages) {
    throw new Error(`Load profile must have between 1 and ${maxStages} stages`);
  }
  requireInteger("Start target", profile.startTarget, 0, isArrivalMode(profile.mode) ? maxArrivalRate : maxVirtualUsers);
  requireInteger("Graceful stop", profile.gracefulStopMs, 1, maxGracefulStopMs);
  let durationMs = 0;
  let positiveTarget = profile.startTarget > 0;
  const stages = profile.stages.map((stage, index) => {
    requireInteger(`Stage ${index + 1} duration`, stage.durationMs, 1, maxDurationMs);
    requireInteger(
      `Stage ${index + 1} target`,
      stage.target,
      0,
      isArrivalMode(profile.mode) ? maxArrivalRate : maxVirtualUsers,
    );
    durationMs += stage.durationMs;
    positiveTarget ||= stage.target > 0;
    if (durationMs > maxDurationMs) {
      throw new Error("Load profile duration must be at most 1 hour");
    }
    return { ...stage };
  });
  if (!positiveTarget) {
    throw new Error("Load profile must have a positive target");
  }
  if (!isRampingMode(profile.mode) && (
    stages.length !== 1 || profile.startTarget < 1 || stages[0].target !== profile.startTarget
  )) {
    throw new Error(`${profile.mode} requires one stage matching a positive start target`);
  }
  if (isArrivalMode(profile.mode)) {
    requireInteger("Pre-allocated VUs", profile.preAllocatedVUs, 1, maxVirtualUsers);
    requireInteger("Max VUs", profile.maxVUs, profile.preAllocatedVUs, maxVirtualUsers);
  }
  return {
    mode: profile.mode,
    startTarget: profile.startTarget,
    stages,
    preAllocatedVUs: isArrivalMode(profile.mode) ? profile.preAllocatedVUs : 0,
    maxVUs: isArrivalMode(profile.mode) ? profile.maxVUs : 0,
    gracefulStopMs: profile.gracefulStopMs,
  };
}

export function loadProfileSummary(profile: LoadProfile) {
  const normalized = normalizeLoadProfile(profile);
  const peakTarget = Math.max(normalized.startTarget, ...normalized.stages.map((stage) => stage.target));
  return {
    profile: normalized,
    durationMs: normalized.stages.reduce((total, stage) => total + stage.durationMs, 0),
    maxVirtualUsers: isArrivalMode(normalized.mode) ? normalized.maxVUs : peakTarget,
    peakTarget,
    targetUnit: isArrivalMode(normalized.mode) ? "iterations/s" : "VUs",
  };
}

export function convertLoadProfileMode(profile: LoadProfile, mode: LoadMode): LoadProfile {
  const durationMs = Math.max(1, profile.stages.reduce((total, stage) => total + stage.durationMs, 0));
  const ramping = isRampingMode(mode);
  const arrival = isArrivalMode(mode);
  const targetLimit = arrival ? maxArrivalRate : maxVirtualUsers;
  const clampTarget = (target: number) => Math.min(targetLimit, Math.max(0, target));
  const peakTarget = Math.max(1, clampTarget(profile.startTarget), ...profile.stages.map((stage) => clampTarget(stage.target)));
  const stages = ramping
    ? profile.stages.map((stage) => ({ ...stage, target: clampTarget(stage.target) }))
    : [{ durationMs, target: peakTarget }];
  const startTarget = ramping ? clampTarget(profile.startTarget) : peakTarget;
  return {
    mode,
    startTarget,
    stages,
    preAllocatedVUs: arrival ? Math.max(1, profile.preAllocatedVUs || Math.min(peakTarget, 10)) : 0,
    maxVUs: arrival ? Math.max(1, profile.maxVUs || Math.min(Math.max(peakTarget, 10), maxVirtualUsers)) : 0,
    gracefulStopMs: profile.gracefulStopMs,
  };
}

function requireInteger(name: string, value: number, minimum: number, maximum: number) {
  if (!Number.isInteger(value) || value < minimum || value > maximum) {
    throw new Error(`${name} must be an integer between ${minimum} and ${maximum}`);
  }
}

function boundedInteger(value: number, minimum: number, maximum: number, fallback: number) {
  if (!Number.isFinite(value)) {
    return fallback;
  }
  return Math.min(maximum, Math.max(minimum, Math.floor(value)));
}
