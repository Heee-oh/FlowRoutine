import type { Header } from "./types";

type HeaderValue = {
  name: string;
  value: string;
};

const secretPlaceholderPattern = /\{\{(SECRET_[a-z0-9_]+)\}\}/gi;
const sensitiveHeaderNames = new Set([
  "auth",
  "authentication",
  "authorization",
  "proxyauthorization",
  "cookie",
  "setcookie",
  "apikey",
  "xauth",
  "xapikey",
  "xauthkey",
  "xauthorization",
]);
const sensitiveParameterNames = new Set([
  "accesskey",
  "accesskeyid",
  "accesstoken",
  "apikey",
  "auth",
  "authorization",
  "awsaccesskeyid",
  "clientsecret",
  "code",
  "cookie",
  "googleaccessid",
  "idtoken",
  "keypairid",
  "passphrase",
  "password",
  "passwd",
  "privatekey",
  "refreshtoken",
  "sas",
  "secretkey",
  "sessiontoken",
  "sharedaccesssignature",
  "sig",
  "session",
  "sessionid",
  "setcookie",
]);
const sensitiveHeaderValuePattern = /^(?:aws4-hmac-sha256|basic|bearer|digest|hawk|token)\s+/i;

export function isSensitiveHeaderName(name: string) {
  const normalized = normalizeSensitiveName(name);
  return sensitiveHeaderNames.has(normalized) ||
    normalized.includes("token") ||
    normalized.includes("secret") ||
    normalized.includes("signature") ||
    normalized.includes("credential") ||
    normalized.includes("apikey") ||
    normalized.includes("accesskey") ||
    normalized.includes("privatekey") ||
    normalized.includes("password") ||
    normalized.endsWith("authorization");
}

export function isSensitiveHeaderValue(value: string) {
  return sensitiveHeaderValuePattern.test(value.trim());
}

export function isSensitiveQueryParameter(name: string) {
  const normalized = normalizeSensitiveName(name);
  return sensitiveParameterNames.has(normalized) ||
    normalized.includes("token") ||
    normalized.includes("secret") ||
    normalized.includes("signature") ||
    normalized.includes("credential") ||
    normalized.includes("apikey") ||
    normalized.includes("accesskey");
}

export function secretBindingName(name: string) {
  const normalized = name
    .replace(/[^a-z0-9]+/gi, "_")
    .replace(/^_+|_+$/g, "")
    .toUpperCase();
  return `SECRET_${normalized || "VALUE"}`;
}

export function secretPlaceholder(name: string) {
  return `{{${secretBindingName(name)}}}`;
}

export function sanitizeHeaderRows(headers: HeaderValue[]): HeaderValue[] {
  return headers.map((header) => {
    if (isSensitiveHeaderName(header.name) || isSensitiveHeaderValue(header.value)) {
      return { name: header.name, value: secretPlaceholder(header.name) };
    }
    return { name: header.name, value: sanitizeURLValue(header.value) };
  });
}

export function redactHeaders(headers: Header[]): Header[] {
  return headers.map((header) => ({
    name: header.name,
    value: isSensitiveHeaderName(header.name) || isSensitiveHeaderValue(header.value)
      ? "[redacted]"
      : redactURLValue(header.value),
  }));
}

export function sanitizeHeaderText(raw: string) {
  return raw
    .split("\n")
    .map((line) => {
      const splitAt = line.indexOf(":");
      if (splitAt < 1) {
        return line;
      }
      const name = line.slice(0, splitAt).trim();
      const value = line.slice(splitAt + 1).trim();
      if (isSensitiveHeaderName(name) || isSensitiveHeaderValue(value)) {
        return `${name}: ${secretPlaceholder(name)}`;
      }
      const sanitizedValue = sanitizeURLValue(value);
      return sanitizedValue === value ? line : `${name}: ${sanitizedValue}`;
    })
    .join("\n");
}

export function sanitizeSensitiveURL(rawURL: string) {
  return transformSensitiveURL(rawURL, (name) => secretPlaceholder(name));
}

export function redactSensitiveURL(rawURL: string) {
  return transformSensitiveURL(rawURL, () => "REDACTED");
}

export function sanitizeStructuredBody(raw: string) {
  if (!raw) {
    return raw;
  }
  const trimmed = raw.trim();
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
    try {
      const parsed: unknown = JSON.parse(raw);
      const result = sanitizeJSONValue(parsed);
      if (result.changed) {
        return JSON.stringify(result.value, null, raw.includes("\n") ? 2 : undefined);
      }
    } catch {
      // Fall through to form-like formats.
    }
  }

  const lines = raw.split("\n");
  if (lines.length > 1 && lines.every((line) => !line.trim() || line.includes("="))) {
    let changed = false;
    const sanitized = lines.map((line) => {
      const result = sanitizeKeyValuePart(line);
      changed ||= result.changed;
      return result.value;
    });
    if (changed) {
      return sanitized.join("\n");
    }
  }

  if (raw.includes("=")) {
    let changed = false;
    const sanitized = raw.split("&").map((part) => {
      const result = sanitizeKeyValuePart(part);
      changed ||= result.changed;
      return result.value;
    });
    if (changed) {
      return sanitized.join("&");
    }
  }
  const assignments = sanitizeNamedAssignments(raw);
  if (assignments !== raw) {
    return assignments;
  }
  return sanitizeURLValue(raw);
}

export function collectSecretPlaceholderNames(values: Array<string | null | undefined>) {
  const names = new Set<string>();
  for (const value of values) {
    if (!value) {
      continue;
    }
    for (const match of value.matchAll(secretPlaceholderPattern)) {
      names.add(match[1].toUpperCase());
    }
  }
  return Array.from(names).sort();
}

export function resolveSecretPlaceholders(value: string, bindings: Record<string, string> | undefined) {
  return value.replace(secretPlaceholderPattern, (_placeholder, rawName: string) => {
    const name = rawName.toUpperCase();
    const resolved = bindings?.[name];
    if (!resolved) {
      throw new Error(`Runtime secret ${name} is required`);
    }
    return resolved;
  });
}

function transformSensitiveURL(rawURL: string, replacement: (name: string) => string) {
  try {
    const url = new URL(rawURL);
    if (url.username) {
      url.username = replacement("URL_USERNAME");
    }
    if (url.password) {
      url.password = replacement("URL_PASSWORD");
    }
    const queryNames = Array.from(new Set(url.searchParams.keys()));
    for (const name of queryNames) {
      if (isSensitiveQueryParameter(name)) {
        url.searchParams.set(name, replacement(name));
      }
    }
    url.hash = transformURLFragment(url.hash, replacement);
    return restoreEncodedTemplates(url.toString());
  } catch {
    return transformRelativeURL(rawURL, replacement);
  }
}

function transformRelativeURL(rawURL: string, replacement: (name: string) => string) {
  const hashAt = rawURL.indexOf("#");
  const withoutHash = hashAt >= 0 ? rawURL.slice(0, hashAt) : rawURL;
  const hash = hashAt >= 0 ? rawURL.slice(hashAt) : "";
  const sanitizedHash = transformURLFragment(hash, replacement);
  const queryAt = withoutHash.indexOf("?");
  if (queryAt < 0) {
    return sanitizedHash === hash ? rawURL : `${withoutHash}${sanitizedHash}`;
  }
  const path = withoutHash.slice(0, queryAt);
  const query = transformQueryString(withoutHash.slice(queryAt + 1), replacement);
  return query.changed || sanitizedHash !== hash
    ? `${path}?${query.value}${sanitizedHash}`
    : rawURL;
}

function transformURLFragment(hash: string, replacement: (name: string) => string) {
  if (!hash) {
    return hash;
  }
  const fragment = hash.startsWith("#") ? hash.slice(1) : hash;
  const queryAt = fragment.indexOf("?");
  if (queryAt >= 0) {
    const query = transformQueryString(fragment.slice(queryAt + 1), replacement);
    return query.changed ? `#${fragment.slice(0, queryAt + 1)}${query.value}` : hash;
  }
  if (!fragment.includes("=")) {
    return hash;
  }
  const query = transformQueryString(fragment, replacement);
  return query.changed ? `#${query.value}` : hash;
}

function transformQueryString(rawQuery: string, replacement: (name: string) => string) {
  let changed = false;
  const value = rawQuery.split("&").map((part) => {
    const splitAt = part.indexOf("=");
    const encodedName = splitAt >= 0 ? part.slice(0, splitAt) : part;
    const name = safeDecodeURIComponent(encodedName);
    if (!isSensitiveQueryParameter(name)) {
      return part;
    }
    changed = true;
    return `${encodedName}=${replacement(name)}`;
  }).join("&");
  return { value, changed };
}

function sanitizeJSONValue(value: unknown): { value: unknown; changed: boolean } {
  if (Array.isArray(value)) {
    let changed = false;
    const next = value.map((item) => {
      const result = sanitizeJSONValue(item);
      changed ||= result.changed;
      return result.value;
    });
    return { value: changed ? next : value, changed };
  }
  if (value && typeof value === "object") {
    let changed = false;
    const next = Object.fromEntries(Object.entries(value).map(([key, item]) => {
      if (isSensitiveQueryParameter(key)) {
        changed = true;
        return [key, secretPlaceholder(key)];
      }
      const result = sanitizeJSONValue(item);
      changed ||= result.changed;
      return [key, result.value];
    }));
    return { value: changed ? next : value, changed };
  }
  if (typeof value === "string") {
    const sanitized = sanitizeURLValue(value);
    return { value: sanitized, changed: sanitized !== value };
  }
  return { value, changed: false };
}

function sanitizeURLValue(value: string) {
  return /^https?:\/\//i.test(value) ? sanitizeSensitiveURL(value) : value;
}

function redactURLValue(value: string) {
  return /^https?:\/\//i.test(value) ? redactSensitiveURL(value) : value;
}

function sanitizeKeyValuePart(part: string) {
  const splitAt = part.indexOf("=");
  if (splitAt < 1) {
    return { value: part, changed: false };
  }
  const encodedName = part.slice(0, splitAt).trim();
  const name = safeDecodeURIComponent(encodedName.replace(/\+/g, " "));
  if (!isSensitiveQueryParameter(name)) {
    return { value: part, changed: false };
  }
  return {
    value: `${part.slice(0, splitAt + 1)}${secretPlaceholder(name)}`,
    changed: true,
  };
}

function sanitizeNamedAssignments(raw: string) {
  return raw.replace(
    /(["']?)([a-z_][a-z0-9_.-]*)\1(\s*[:=]\s*)("[^"\r\n]*"|'[^'\r\n]*'|[^,\s;&}\]\r\n]+)/gi,
    (match, keyQuote: string, key: string, separator: string, value: string) => {
      if (!isSensitiveQueryParameter(key)) {
        return match;
      }
      const valueQuote = value[0] === "\"" || value[0] === "'" ? value[0] : "";
      return `${keyQuote}${key}${keyQuote}${separator}${valueQuote}${secretPlaceholder(key)}${valueQuote}`;
    },
  );
}

function restoreEncodedTemplates(value: string) {
  return value.replace(
    /%7B%7B([A-Z_][A-Z0-9_.-]*)%7D%7D/gi,
    (_match, name: string) => `{{${name}}}`,
  );
}

function normalizeSensitiveName(name: string) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "");
}

function safeDecodeURIComponent(value: string) {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}
