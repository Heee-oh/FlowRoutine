import type { FlowNodeData } from "./flowTypes";
import {
  sanitizeHeaderRows,
  sanitizeSensitiveURL,
  sanitizeStructuredBody,
} from "./secretSanitization";

export type PostmanRequestImport = {
  name: string;
  settings: Partial<FlowNodeData>;
};

type PostmanCollection = {
  info?: {
    name?: string;
    schema?: string;
  };
  item?: PostmanItem[];
};

type PostmanItem = {
  name?: string;
  item?: PostmanItem[];
  request?: PostmanRequest | string;
};

type PostmanRequest = {
  method?: string;
  url?: PostmanURL | string;
  header?: PostmanHeader[];
  body?: {
    mode?: string;
    raw?: string;
    urlencoded?: Array<{ key?: string; value?: string; disabled?: boolean }>;
    formdata?: Array<{ key?: string; value?: string; disabled?: boolean }>;
  };
};

type PostmanURL = {
  raw?: string;
  protocol?: string;
  host?: string[] | string;
  path?: string[] | string;
  query?: Array<{ key?: string; value?: string; disabled?: boolean }>;
};

type PostmanHeader = {
  key?: string;
  name?: string;
  value?: string;
  disabled?: boolean;
};

const supportedMethods = new Set(["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"]);

export function parsePostmanCollection(raw: string): PostmanRequestImport[] {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    throw new Error(`Postman collection must be JSON: ${err instanceof Error ? err.message : "invalid JSON"}`);
  }
  if (!isObject(parsed) || !Array.isArray((parsed as PostmanCollection).item)) {
    throw new Error("Postman collection is missing an item array");
  }

  const collection = parsed as PostmanCollection;
  const requests = flattenItems(collection.item ?? [], []);
  if (requests.length === 0) {
    throw new Error("Postman collection has no runnable HTTP requests");
  }
  return requests;
}

function flattenItems(items: PostmanItem[], path: string[]): PostmanRequestImport[] {
  const requests: PostmanRequestImport[] = [];
  for (const item of items) {
    const nextPath = item.name ? path.concat(item.name) : path;
    if (Array.isArray(item.item)) {
      requests.push(...flattenItems(item.item, nextPath));
      continue;
    }
    const request = normalizeRequest(item.request);
    if (!request) {
      continue;
    }
    const method = normalizeMethod(request.method);
    requests.push({
      name: sanitizeSensitiveURL(nextPath.join(" / ") || `${method} request`),
      settings: {
        method,
        url: normalizeURL(request.url),
        headersText: normalizeHeaders(request.header),
        headersMode: "form",
        headerRows: normalizeHeaderRows(request.header),
        body: normalizeBody(request.body),
      },
    });
  }
  return requests;
}

function normalizeRequest(request: PostmanItem["request"]): PostmanRequest | null {
  if (typeof request === "string") {
    return { method: "GET", url: request };
  }
  return isObject(request) ? request as PostmanRequest : null;
}

function normalizeMethod(method: string | undefined) {
  const normalized = (method || "GET").toUpperCase();
  return supportedMethods.has(normalized) ? normalized : "GET";
}

function normalizeURL(url: PostmanRequest["url"]) {
  if (typeof url === "string") {
    return sanitizeSensitiveURL(url);
  }
  if (!isObject(url)) {
    return "http://127.0.0.1:8080";
  }
  if (url.raw) {
    return sanitizeSensitiveURL(url.raw);
  }
  const protocol = url.protocol || "http";
  const host = Array.isArray(url.host) ? url.host.join(".") : url.host || "127.0.0.1:8080";
  const path = Array.isArray(url.path) ? url.path.join("/") : url.path || "";
  const query = new URLSearchParams();
  for (const item of url.query ?? []) {
    if (!item.disabled && item.key) {
      query.set(item.key, item.value ?? "");
    }
  }
  const queryString = query.toString();
  return sanitizeSensitiveURL(
    `${protocol}://${host}${path ? `/${path.replace(/^\/+/, "")}` : ""}${queryString ? `?${queryString}` : ""}`,
  );
}

function normalizeHeaderRows(headers: PostmanHeader[] | undefined) {
  return sanitizeHeaderRows((headers ?? [])
    .filter((header) => !header.disabled)
    .map((header) => ({ name: header.key || header.name || "", value: header.value ?? "" }))
    .filter((header) => header.name.trim()));
}

function normalizeHeaders(headers: PostmanHeader[] | undefined) {
  return normalizeHeaderRows(headers).map((header) => `${header.name}: ${header.value}`).join("\n");
}

function normalizeBody(body: PostmanRequest["body"]) {
  if (!body) {
    return "";
  }
  if (body.mode === "raw") {
    return sanitizeStructuredBody(body.raw ?? "");
  }
  if (body.mode === "urlencoded") {
    return sanitizeStructuredBody(new URLSearchParams(activeKeyValueRows(body.urlencoded)).toString());
  }
  if (body.mode === "formdata") {
    return sanitizeStructuredBody(activeKeyValueRows(body.formdata)
      .map(([key, value]) => `${key}=${value}`)
      .join("\n"));
  }
  return sanitizeStructuredBody(body.raw ?? "");
}

function activeKeyValueRows(rows: Array<{ key?: string; value?: string; disabled?: boolean }> | undefined): [string, string][] {
  return (rows ?? [])
    .filter((row) => !row.disabled && row.key)
    .map((row) => [row.key ?? "", row.value ?? ""]);
}

function isObject(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object";
}
