import type { Capture } from "./types";

export type NormalizedCapture = {
  name: string;
  path: string;
  scope: "iteration" | "run";
  onStatus: string;
};

type CaptureInput = Omit<Partial<Capture>, "scope"> & {
  scope?: string;
};

export function normalizeCaptureDefinition(capture: CaptureInput): NormalizedCapture {
  const name = capture.name?.trim() ?? "";
  const path = capture.path?.trim() ?? "";
  if (!name || !path) {
    throw new Error("Invalid capture: name and path are required");
  }
  if (!/^[a-z_][a-z0-9_.-]*$/i.test(name)) {
    throw new Error(`Invalid capture name: ${name}`);
  }
  if (name.toUpperCase().startsWith("SECRET_")) {
    throw new Error("Capture names cannot use the reserved SECRET_ prefix");
  }
  validateCapturePath(path);
  return {
    name,
    path,
    scope: normalizeCaptureScope(capture.scope),
    onStatus: normalizeCaptureStatus(capture.onStatus),
  };
}

export function normalizeCaptureScope(value: string | undefined): "iteration" | "run" {
  const normalized = value?.trim().toLowerCase() || "iteration";
  if (normalized !== "iteration" && normalized !== "run") {
    throw new Error(`Invalid capture scope: ${normalized}`);
  }
  return normalized;
}

export function normalizeCaptureStatus(value: string | undefined) {
  const normalized = value?.trim().toLowerCase() || "success";
  if (normalized !== "success" &&
    normalized !== "any" &&
    !/^[1-5]xx$/.test(normalized) &&
    !/^[1-5][0-9]{2}$/.test(normalized)) {
    throw new Error(`Invalid capture status policy: ${normalized}`);
  }
  return normalized;
}

export function validateCapturePath(path: string) {
  if (path === "$") {
    return;
  }
  let remaining = path;
  if (remaining.startsWith("$.")) {
    remaining = remaining.slice(2);
    if (!remaining || [".", "[", "]"].includes(remaining[0])) {
      throw new Error(`Invalid capture path: ${path}`);
    }
  } else if (remaining.startsWith("$")) {
    remaining = remaining.slice(1);
    if (!remaining.startsWith("[")) {
      throw new Error(`Invalid capture path: ${path}`);
    }
  }
  if (!remaining) {
    throw new Error(`Invalid capture path: ${path}`);
  }

  while (remaining) {
    if (remaining.startsWith("[")) {
      const index = remaining.match(/^\[(\d+)\]/);
      if (!index) {
        throw new Error(`Invalid capture path: ${path}`);
      }
      remaining = remaining.slice(index[0].length);
    } else {
      const dotAt = remaining.indexOf(".");
      const bracketAt = remaining.indexOf("[");
      const boundaries = [dotAt, bracketAt].filter((index) => index >= 0);
      const end = boundaries.length > 0 ? Math.min(...boundaries) : remaining.length;
      const segment = remaining.slice(0, end);
      if (!segment || segment.includes("]")) {
        throw new Error(`Invalid capture path: ${path}`);
      }
      remaining = remaining.slice(end);
    }

    if (!remaining) {
      break;
    }
    if (remaining.startsWith(".")) {
      remaining = remaining.slice(1);
      if (!remaining || [".", "[", "]"].includes(remaining[0])) {
        throw new Error(`Invalid capture path: ${path}`);
      }
    } else if (!remaining.startsWith("[")) {
      throw new Error(`Invalid capture path: ${path}`);
    }
  }
}
