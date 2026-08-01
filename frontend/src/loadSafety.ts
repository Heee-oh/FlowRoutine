import type { SafetyAssessment, TargetProfile } from "./flowTypes";
import type { LoadConfig, PreflightResponse } from "./types";

export function assessStartSafety(config: LoadConfig, preflight?: PreflightResponse): SafetyAssessment {
  const target = classifyTarget(config.url);
  const warnings: string[] = [];
  if (target.tone === "public") {
    warnings.push("Public target traffic leaves this machine.");
  }
  if (target.tone === "invalid") {
    warnings.push("Target URL is invalid.");
  }
  if (config.rateLimitRps === 0) {
    warnings.push("RPS is unlimited.");
  }
  if (config.durationMs > 300_000) {
    warnings.push("Run duration is longer than 5 minutes.");
  }
  if (config.virtualUsers >= 1_000) {
    warnings.push("Virtual users are set to 1,000 or more.");
  }
  if (preflight) {
    warnings.push(...preflight.warnings.map((warning) => warning.message));
  }
  const uniqueWarnings = Array.from(new Set(warnings));

  return {
    target,
    warnings: uniqueWarnings,
    confirmationRequired: uniqueWarnings.length > 0,
    estimatedMemoryBytes: preflight?.estimate.memoryBytes ?? 0,
    estimatedConnections: preflight?.estimate.connections ?? 0,
    targetHosts: preflight?.estimate.targetHosts ?? 0,
  };
}

export function classifyTarget(rawUrl: string): TargetProfile {
  try {
    const url = new URL(rawUrl);
    const host = url.hostname.toLowerCase();
    if (isLocalHost(host)) {
      return { label: "Localhost", tone: "local", host: url.host };
    }
    if (isPrivateHost(host)) {
      return { label: "Private", tone: "private", host: url.host };
    }
    return { label: "Public", tone: "public", host: url.host };
  } catch {
    return { label: "Invalid", tone: "invalid", host: rawUrl || "No target" };
  }
}

function isLocalHost(host: string) {
  return host === "localhost" || host === "127.0.0.1" || host === "::1" || host.endsWith(".localhost");
}

function isPrivateHost(host: string) {
  const parts = host.split(".").map((part) => Number(part));
  if (parts.length === 4 && parts.every((part) => Number.isInteger(part) && part >= 0 && part <= 255)) {
    return parts[0] === 10 ||
      (parts[0] === 172 && parts[1] >= 16 && parts[1] <= 31) ||
      (parts[0] === 192 && parts[1] === 168) ||
      (parts[0] === 169 && parts[1] === 254);
  }
  return host.endsWith(".local") || host.startsWith("fc") || host.startsWith("fd");
}
