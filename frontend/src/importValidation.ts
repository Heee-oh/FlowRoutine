export const MAX_IMPORT_FILE_BYTES = 5 * 1024 * 1024;
export const MAX_IMPORT_JSON_DEPTH = 64;
export const MAX_IMPORT_REQUESTS = 500;

export function assertImportFileSize(
  size: number,
  label: string,
  maxBytes = MAX_IMPORT_FILE_BYTES,
) {
  if (!Number.isSafeInteger(size) || size < 0) {
    throw new Error(`${label} has an invalid file size`);
  }
  if (size > maxBytes) {
    throw new Error(`${label} exceeds the ${formatByteLimit(maxBytes)} file limit`);
  }
}

export function assertImportTextSize(
  raw: string,
  label: string,
  maxBytes = MAX_IMPORT_FILE_BYTES,
) {
  if (raw.length > maxBytes || new TextEncoder().encode(raw).byteLength > maxBytes) {
    throw new Error(`${label} exceeds the ${formatByteLimit(maxBytes)} file limit`);
  }
}

export function assertImportJSONDepth(
  raw: string,
  label: string,
  maxDepth = MAX_IMPORT_JSON_DEPTH,
) {
  let depth = 0;
  let escaped = false;
  let inString = false;

  for (let index = 0; index < raw.length; index += 1) {
    const character = raw[index];
    if (inString) {
      if (escaped) {
        escaped = false;
      } else if (character === "\\") {
        escaped = true;
      } else if (character === '"') {
        inString = false;
      }
      continue;
    }

    if (character === '"') {
      inString = true;
      continue;
    }
    if (character === "{" || character === "[") {
      depth += 1;
      if (depth > maxDepth) {
        throw new Error(`${label} exceeds the ${maxDepth}-level nesting limit`);
      }
    } else if (character === "}" || character === "]") {
      depth -= 1;
    }
  }
}

export function parseBoundedImportJSON(raw: string, label: string): unknown {
  assertImportTextSize(raw, label);
  assertImportJSONDepth(raw, label);
  try {
    return JSON.parse(raw);
  } catch (err) {
    throw new Error(`${label} must be JSON: ${err instanceof Error ? err.message : "invalid JSON"}`);
  }
}

export function assertImportRequestCount(
  count: number,
  label: string,
  maxRequests = MAX_IMPORT_REQUESTS,
) {
  if (count > maxRequests) {
    throw new Error(`${label} exceeds the ${maxRequests}-request limit`);
  }
}

function formatByteLimit(bytes: number) {
  const mebibyte = 1024 * 1024;
  if (bytes >= mebibyte && bytes % mebibyte === 0) {
    return `${bytes / mebibyte} MiB`;
  }
  return `${bytes} byte`;
}
