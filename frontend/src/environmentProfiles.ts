import {
  collectSecretPlaceholderNames,
  isSensitiveHeaderName,
  sanitizeSensitiveURL,
  sanitizeStructuredBody,
} from "./secretSanitization";
import type { EnvironmentProfile, EnvironmentVariable } from "./flowTypes";

const environmentProfilesKey = "flowroutine:environment-profiles:v1";
const activeEnvironmentKey = "flowroutine:active-environment:v1";
const environmentNamePattern = /^[A-Z_][A-Z0-9_]*$/;
const templatePattern = /\{\{\s*([a-z_][a-z0-9_.-]*)\s*\}\}/gi;
const maxEnvironmentProfiles = 24;
const maxEnvironmentVariables = 128;

export function createEnvironmentProfile(index: number): EnvironmentProfile {
  const id = typeof globalThis.crypto?.randomUUID === "function"
    ? globalThis.crypto.randomUUID()
    : `environment-${Date.now()}-${index}`;
  return {
    id,
    name: `Environment ${index}`,
    baseUrl: "",
    variables: [],
    secretNames: [],
  };
}

export function loadEnvironmentProfiles(): EnvironmentProfile[] {
  if (typeof window === "undefined") {
    return [];
  }
  try {
    const raw = window.localStorage.getItem(environmentProfilesKey);
    const parsed: unknown = raw ? JSON.parse(raw) : [];
    const profiles = Array.isArray(parsed)
      ? parsed.flatMap(readEnvironmentProfile).slice(0, maxEnvironmentProfiles)
      : [];
    persistProfiles(profiles);
    return profiles;
  } catch {
    return [];
  }
}

export function saveEnvironmentProfiles(profiles: EnvironmentProfile[]) {
  const sanitized = profiles
    .slice(0, maxEnvironmentProfiles)
    .map(sanitizeEnvironmentProfileForStorage);
  persistProfiles(sanitized);
  return sanitized;
}

export function loadActiveEnvironmentId() {
  if (typeof window === "undefined") {
    return null;
  }
  try {
    return window.localStorage.getItem(activeEnvironmentKey) || null;
  } catch {
    return null;
  }
}

export function saveActiveEnvironmentId(id: string | null) {
  if (typeof window === "undefined") {
    return;
  }
  try {
    if (id) {
      window.localStorage.setItem(activeEnvironmentKey, id);
    } else {
      window.localStorage.removeItem(activeEnvironmentKey);
    }
  } catch {
    // Environment selection should remain usable when storage is unavailable.
  }
}

export function environmentVariableBindings(
  profile: EnvironmentProfile | null | undefined,
  captureNames: ReadonlySet<string>,
) {
  if (!profile) {
    return {};
  }
  const normalized = validateEnvironmentProfile(profile);
  const bindings: Record<string, string> = {};
  if (normalized.baseUrl) {
    bindings.BASE_URL = normalized.baseUrl;
  }
  for (const variable of normalized.variables) {
    if (captureNames.has(variable.name)) {
      throw new Error(`Environment variable ${variable.name} conflicts with a capture name`);
    }
    bindings[variable.name] = variable.value;
  }
  if (captureNames.has("BASE_URL") && normalized.baseUrl) {
    throw new Error("Environment variable BASE_URL conflicts with a capture name");
  }
  return bindings;
}

export function environmentSecretNames(profile: EnvironmentProfile | null | undefined) {
  if (!profile) {
    return [];
  }
  const names = new Set(profile.secretNames.flatMap((name) => {
    const normalized = normalizeSecretName(name);
    return normalized ? [normalized] : [];
  }));
  for (const name of collectSecretPlaceholderNames([
    profile.baseUrl,
    ...profile.variables.map((variable) => variable.value),
  ])) {
    names.add(name);
  }
  return Array.from(names).sort();
}

export function normalizeSecretName(value: string) {
  const normalized = value
    .trim()
    .replace(/[^a-z0-9]+/gi, "_")
    .replace(/^_+|_+$/g, "")
    .toUpperCase();
  if (!normalized) {
    return "";
  }
  return normalized.startsWith("SECRET_") ? normalized : `SECRET_${normalized}`;
}

export function resolveEnvironmentPlaceholders(
  value: string,
  bindings: Readonly<Record<string, string>>,
  captureNames: ReadonlySet<string>,
) {
  return value.replace(templatePattern, (placeholder, rawName: string) => {
    const name = rawName.trim();
    if (name.toUpperCase().startsWith("SECRET_") || captureNames.has(name)) {
      return placeholder;
    }
    if (Object.prototype.hasOwnProperty.call(bindings, name)) {
      const resolved = bindings[name];
      if (!resolved) {
        throw new Error(`Environment variable ${name} is required`);
      }
      return resolved;
    }
    if (environmentNamePattern.test(name)) {
      throw new Error(`Environment variable ${name} is required`);
    }
    return placeholder;
  });
}

export function validateEnvironmentProfile(profile: EnvironmentProfile): EnvironmentProfile {
  const name = profile.name.trim();
  if (!name) {
    throw new Error("Environment name is required");
  }
  const baseUrl = profile.baseUrl.trim();
  if (baseUrl && introducedSecretNames(baseUrl, sanitizeEnvironmentURL(baseUrl)).length > 0) {
    throw new Error("Environment base URL contains a secret; use a SECRET_* placeholder instead");
  }
  const seen = new Set<string>();
  const variables: EnvironmentVariable[] = [];
  for (const variable of profile.variables.slice(0, maxEnvironmentVariables)) {
    const variableName = variable.name.trim().toUpperCase();
    const value = variable.value;
    if (!variableName && !value) {
      continue;
    }
    if (!environmentNamePattern.test(variableName)) {
      throw new Error(`Invalid environment variable name: ${variable.name || "(empty)"}`);
    }
    if (variableName === "BASE_URL") {
      throw new Error("BASE_URL is managed by the environment Base URL field");
    }
    if (variableName.startsWith("SECRET_") || isSensitiveHeaderName(variableName)) {
      throw new Error(`Environment variable ${variableName} must be declared as a runtime secret`);
    }
    if (seen.has(variableName)) {
      throw new Error(`Environment variable ${variableName} is duplicated`);
    }
    if (introducedSecretNames(value, sanitizeEnvironmentValue(value)).length > 0) {
      throw new Error(`Environment variable ${variableName} contains a secret; use a SECRET_* placeholder instead`);
    }
    seen.add(variableName);
    variables.push({ name: variableName, value });
  }
  const secretNames = environmentSecretNames({ ...profile, variables, baseUrl });
  return { ...profile, name, baseUrl, variables, secretNames };
}

export function parseEnvironmentVariables(raw: string): EnvironmentVariable[] {
  return raw.split("\n").map((line) => {
    const splitAt = line.indexOf("=");
    if (splitAt < 0) {
      return { name: line, value: "" };
    }
    return {
      name: line.slice(0, splitAt),
      value: line.slice(splitAt + 1),
    };
  });
}

export function formatEnvironmentVariables(variables: EnvironmentVariable[]) {
  return variables.map((variable) => `${variable.name}=${variable.value}`).join("\n");
}

function sanitizeEnvironmentProfileForStorage(profile: EnvironmentProfile): EnvironmentProfile {
  const secretNames = new Set(environmentSecretNames(profile));
  const rawBaseUrl = profile.baseUrl.trim();
  const sanitizedBaseUrl = sanitizeEnvironmentURL(rawBaseUrl);
  const baseUrl = introducedSecretNames(rawBaseUrl, sanitizedBaseUrl).length > 0
    ? sanitizedBaseUrl
    : rawBaseUrl;
  for (const name of collectSecretPlaceholderNames([baseUrl])) {
    secretNames.add(name);
  }
  const variables: EnvironmentVariable[] = [];
  const seen = new Set<string>();
  for (const variable of profile.variables.slice(0, maxEnvironmentVariables)) {
    const name = variable.name.trim().toUpperCase();
    if (!environmentNamePattern.test(name) || name === "BASE_URL" || seen.has(name)) {
      continue;
    }
    seen.add(name);
    if (name.startsWith("SECRET_") || isSensitiveHeaderName(name)) {
      secretNames.add(normalizeSecretName(name));
      continue;
    }
    const sanitizedValue = sanitizeEnvironmentValue(variable.value);
    const value = introducedSecretNames(variable.value, sanitizedValue).length > 0
      ? sanitizedValue
      : variable.value;
    for (const secretName of collectSecretPlaceholderNames([value])) {
      secretNames.add(secretName);
    }
    variables.push({ name, value });
  }
  return {
    id: profile.id,
    name: profile.name.trim() || "Environment",
    baseUrl,
    variables,
    secretNames: Array.from(secretNames).filter(Boolean).sort(),
  };
}

function readEnvironmentProfile(value: unknown): EnvironmentProfile[] {
  if (!value || typeof value !== "object") {
    return [];
  }
  const candidate = value as Partial<EnvironmentProfile>;
  if (typeof candidate.id !== "string" || typeof candidate.name !== "string") {
    return [];
  }
  const profile: EnvironmentProfile = {
    id: candidate.id,
    name: candidate.name,
    baseUrl: typeof candidate.baseUrl === "string" ? candidate.baseUrl : "",
    variables: Array.isArray(candidate.variables)
      ? candidate.variables.flatMap((variable) => {
          if (!variable || typeof variable !== "object") {
            return [];
          }
          const entry = variable as Partial<EnvironmentVariable>;
          return typeof entry.name === "string" && typeof entry.value === "string"
            ? [{ name: entry.name, value: entry.value }]
            : [];
        })
      : [],
    secretNames: Array.isArray(candidate.secretNames)
      ? candidate.secretNames.filter((name): name is string => typeof name === "string")
      : [],
  };
  return [sanitizeEnvironmentProfileForStorage(profile)];
}

function persistProfiles(profiles: EnvironmentProfile[]) {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(environmentProfilesKey, JSON.stringify(profiles));
  } catch {
    // Keep the current in-memory profiles when storage is unavailable.
  }
}

function introducedSecretNames(raw: string, sanitized: string) {
  const existing = new Set(collectSecretPlaceholderNames([raw]));
  return collectSecretPlaceholderNames([sanitized]).filter((name) => !existing.has(name));
}

function sanitizeEnvironmentURL(value: string) {
  return sanitizeSensitiveURL(value, new Set(collectSecretPlaceholderNames([value])));
}

function sanitizeEnvironmentValue(value: string) {
  return sanitizeStructuredBody(value, new Set(collectSecretPlaceholderNames([value])));
}
