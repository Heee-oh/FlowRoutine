import type { FlowNodeData } from "./flowTypes";
import {
  sanitizeSensitiveURL,
  sanitizeStructuredBody,
} from "./secretSanitization";
import type { OpenAPIEndpoint } from "./types";

export function openAPIEndpointToRequestSettings(endpoint: OpenAPIEndpoint, sourceURL: string): Partial<FlowNodeData> {
  const method = endpoint.method.toUpperCase();
  return {
    url: sanitizeSensitiveURL(openAPIEndpointURL(endpoint, sourceURL)),
    method,
    headersText: openAPIHeadersText(method),
    ...openAPIAuthSettings(endpoint),
    body: sanitizeStructuredBody(endpoint.bodySample),
  };
}

function openAPIEndpointURL(endpoint: OpenAPIEndpoint, sourceURL: string) {
  const serverURL = endpoint.serverUrl || sourceURL;
  const path = openAPIPathWithSamples(endpoint);
  try {
    const base = new URL(serverURL, sourceURL);
    const url = new URL(path.replace(/^\/+/, ""), normalizedBaseURL(base));
    for (const parameter of endpoint.parameters ?? []) {
      if (parameter.in === "query" && parameter.name) {
        url.searchParams.set(parameter.name, parameter.sample || "string");
      }
    }
    return url.toString();
  } catch {
    const query = openAPIQueryString(endpoint);
    return `${serverURL.replace(/\/+$/, "")}/${path.replace(/^\/+/, "")}${query}`;
  }
}

function openAPIPathWithSamples(endpoint: OpenAPIEndpoint) {
  const pathParameters = new Map(
    (endpoint.parameters ?? [])
      .filter((parameter) => parameter.in === "path")
      .map((parameter) => [parameter.name, parameter.sample || "1"]),
  );
  return endpoint.path.replace(/\{([^}]+)\}/g, (_match, name: string) => encodeURIComponent(pathParameters.get(name) ?? "1"));
}

function openAPIQueryString(endpoint: OpenAPIEndpoint) {
  const params = new URLSearchParams();
  for (const parameter of endpoint.parameters ?? []) {
    if (parameter.in === "query" && parameter.name) {
      params.set(parameter.name, parameter.sample || "string");
    }
  }
  const query = params.toString();
  return query ? `?${query}` : "";
}

function normalizedBaseURL(url: URL) {
  if (!url.pathname.endsWith("/")) {
    url.pathname = `${url.pathname}/`;
  }
  return url;
}

function openAPIHeadersText(method: string) {
  const headers = ["Accept: application/json"];
  if (method !== "GET" && method !== "HEAD") {
    headers.push("Content-Type: application/json");
  }
  return headers.join("\n");
}

function openAPIAuthSettings(endpoint: OpenAPIEndpoint): Partial<FlowNodeData> {
  switch (endpoint.auth.type) {
    case "bearer":
      return { authType: "bearer" };
    case "apiKey":
      return { authType: "apiKey", authApiKeyName: endpoint.auth.name || "X-Api-Key" };
    case "cookie":
      return { authType: "cookie", authCookieName: endpoint.auth.name || "session" };
    default:
      return { authType: "none" };
  }
}
