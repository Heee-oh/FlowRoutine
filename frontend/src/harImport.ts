import {
  sanitizeHeaderRows,
  sanitizeSensitiveURL,
  sanitizeStructuredBody,
} from "./secretSanitization";
import type { ImportedRequest } from "./importGraph";
import {
  assertImportRequestCount,
  parseBoundedImportJSON,
} from "./importValidation";

export type HarRequestImport = ImportedRequest;

type HarArchive = {
  log?: {
    entries?: HarEntry[];
  };
};

type HarEntry = {
  request?: HarRequest;
  response?: {
    content?: {
      mimeType?: string;
    };
  };
};

type HarRequest = {
  method?: string;
  url?: string;
  headers?: HarHeader[];
  postData?: HarPostData;
};

type HarHeader = {
  name?: string;
  value?: string;
};

type HarPostData = {
  mimeType?: string;
  text?: string;
  params?: HarPostParam[];
};

type HarPostParam = {
  name?: string;
  value?: string;
};

const supportedMethods = new Set(["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"]);
const skippedExtensions = /\.(avif|bmp|css|gif|ico|jpg|jpeg|js|map|png|svg|ttf|webp|woff|woff2)(\?|$)/i;
const skippedMimeTypes = /^(image|font)\//i;

export function parseHarArchive(raw: string): HarRequestImport[] {
  const parsed = parseBoundedImportJSON(raw, "HAR file");
  const entries = isObject(parsed) ? (parsed as HarArchive).log?.entries : undefined;
  if (!Array.isArray(entries)) {
    throw new Error("HAR file is missing log.entries");
  }

  const requests: HarRequestImport[] = [];
  for (let index = 0; index < entries.length; index += 1) {
    const entry = entries[index];
    if (!isObject(entry) || !shouldImportEntry(entry as HarEntry)) {
      continue;
    }
    assertImportRequestCount(requests.length + 1, "HAR file");
    requests.push(entryToRequest(entry as HarEntry, index));
  }
  if (requests.length === 0) {
    throw new Error("HAR file has no importable HTTP API requests");
  }
  return requests;
}

function shouldImportEntry(entry: HarEntry) {
  const request = entry.request;
  if (!request?.url || !supportedMethods.has((request.method || "GET").toUpperCase())) {
    return false;
  }
  if (!/^https?:\/\//i.test(request.url)) {
    return false;
  }
  if (skippedExtensions.test(request.url)) {
    return false;
  }
  const mimeType = entry.response?.content?.mimeType ?? "";
  return !skippedMimeTypes.test(mimeType);
}

function entryToRequest(entry: HarEntry, index: number): HarRequestImport {
  const request = entry.request;
  const method = (request?.method || "GET").toUpperCase();
  const url = request?.url || "http://127.0.0.1:8080";
  return {
    name: requestName(method, url, index),
    settings: {
      method,
      url: sanitizeSensitiveURL(url),
      headersMode: "form",
      headerRows: normalizeHeaderRows(request?.headers),
      headersText: normalizeHeaders(request?.headers),
      body: normalizeBody(request?.postData),
    },
  };
}

function normalizeHeaderRows(headers: HarHeader[] | undefined) {
  return sanitizeHeaderRows((headers ?? [])
    .filter((header) => header.name && !isHopByHopHeader(header.name))
    .map((header) => ({
      name: header.name ?? "",
      value: header.value ?? "",
    })));
}

function normalizeHeaders(headers: HarHeader[] | undefined) {
  return normalizeHeaderRows(headers).map((header) => `${header.name}: ${header.value}`).join("\n");
}

function normalizeBody(postData: HarPostData | undefined) {
  if (!postData) {
    return "";
  }
  if (postData.text) {
    return sanitizeStructuredBody(postData.text);
  }
  if (postData.params && postData.params.length > 0) {
    return sanitizeStructuredBody(new URLSearchParams(
      postData.params
        .filter((param) => param.name)
        .map((param) => [param.name ?? "", param.value ?? ""]),
    ).toString());
  }
  return "";
}

function requestName(method: string, rawURL: string, index: number) {
  try {
    const url = new URL(rawURL);
    return `${method} ${url.pathname || "/"}${url.search ? " ?" : ""}`;
  } catch {
    return `${method} request ${index + 1}`;
  }
}

function isHopByHopHeader(name: string) {
  return /^(accept-encoding|connection|content-length|host|user-agent)$/i.test(name);
}

function isObject(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object";
}
