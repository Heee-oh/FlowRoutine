import { describe, expect, it } from "vitest";

import {
  collectSecretPlaceholderNames,
  isSensitiveHeaderName,
  isSensitiveQueryParameter,
  redactSensitiveURL,
  resolveSecretPlaceholders,
  sanitizeHeaderRows,
  sanitizeHeaderText,
  sanitizeSensitiveURL,
  sanitizeStructuredBody,
} from "./secretSanitization";

describe("secret sanitization", () => {
  it("detects common mixed-case header and parameter aliases", () => {
    for (const name of ["aUtHoRiZaTiOn", "X-API-KEY", "X-Auth-Token", "Cookie", "X-Hub-Signature"]) {
      expect(isSensitiveHeaderName(name)).toBe(true);
    }
    for (const name of [
      "access_token",
      "client_secret",
      "X-Amz-Credential",
      "X-Amz-Signature",
      "x-amz-security-token",
      "Key-Pair-Id",
      "GoogleAccessId",
      "sig",
    ]) {
      expect(isSensitiveQueryParameter(name)).toBe(true);
    }
  });

  it("replaces header secrets and authorization-shaped values with bindings", () => {
    expect(sanitizeHeaderRows([
      { name: "aUtHoRiZaTiOn", value: "Bearer header-secret" },
      { name: "X-Custom", value: "Basic value-secret" },
      { name: "Referer", value: "https://example.com?sig=referer-secret" },
      { name: "Accept", value: "application/json" },
    ])).toEqual([
      { name: "aUtHoRiZaTiOn", value: "{{SECRET_AUTHORIZATION}}" },
      { name: "X-Custom", value: "{{SECRET_X_CUSTOM}}" },
      { name: "Referer", value: "https://example.com/?sig={{SECRET_SIG}}" },
      { name: "Accept", value: "application/json" },
    ]);
    expect(sanitizeHeaderText("X-API-Key: raw-key\nAccept: application/json")).toBe(
      "X-API-Key: {{SECRET_X_API_KEY}}\nAccept: application/json",
    );
  });

  it("sanitizes credentials, signed queries, and OAuth fragments in URLs", () => {
    const raw = "https://alice:password@example.com/items?safe=kept&X-Amz-Signature=url-secret#access_token=fragment-secret";
    const sanitized = sanitizeSensitiveURL(raw);
    const redacted = redactSensitiveURL(raw);

    expect(sanitized).toContain("safe=kept");
    expect(sanitized).toContain("{{SECRET_URL_USERNAME}}");
    expect(sanitized).toContain("{{SECRET_URL_PASSWORD}}");
    expect(sanitized).toContain("{{SECRET_X_AMZ_SIGNATURE}}");
    expect(sanitized).toContain("{{SECRET_ACCESS_TOKEN}}");
    expect(sanitized).not.toMatch(/alice|password|url-secret|fragment-secret/);
    expect(redacted).not.toMatch(/alice|password|url-secret|fragment-secret/);
    expect(sanitizeSensitiveURL("/callback#access_token=fragment-secret")).toBe(
      "/callback#access_token={{SECRET_ACCESS_TOKEN}}",
    );
  });

  it("sanitizes nested JSON, form bodies, and signed URL values", () => {
    const json = sanitizeStructuredBody(JSON.stringify({
      username: "kept",
      password: "body-secret",
      callback: "https://example.com/callback?sig=callback-secret",
      nested: { refresh_token: "refresh-secret" },
    }));
    const form = sanitizeStructuredBody("client_secret=form-secret&name=kept");
    const malformedJSON = sanitizeStructuredBody("{\"password\":\"broken-secret\",}");

    expect(json).toContain("\"username\":\"kept\"");
    expect(json).toContain("{{SECRET_PASSWORD}}");
    expect(json).toContain("{{SECRET_SIG}}");
    expect(json).toContain("{{SECRET_REFRESH_TOKEN}}");
    expect(json).not.toMatch(/body-secret|callback-secret|refresh-secret/);
    expect(form).toBe("client_secret={{SECRET_CLIENT_SECRET}}&name=kept");
    expect(malformedJSON).toBe("{\"password\":\"{{SECRET_PASSWORD}}\",}");
  });

  it("collects and resolves reserved runtime bindings", () => {
    expect(collectSecretPlaceholderNames([
      "{{secret_token}}",
      "Bearer {{SECRET_AUTHORIZATION}}",
      "{{captured_response_value}}",
    ])).toEqual(["SECRET_AUTHORIZATION", "SECRET_TOKEN"]);
    expect(resolveSecretPlaceholders(
      "{{SECRET_TOKEN}}/{{secret_authorization}}",
      {
        SECRET_TOKEN: "token-value",
        SECRET_AUTHORIZATION: "Bearer auth-value",
      },
    )).toBe("token-value/Bearer auth-value");
    expect(() => resolveSecretPlaceholders("{{SECRET_TOKEN}}", {})).toThrow(
      "Runtime secret SECRET_TOKEN is required",
    );
  });
});
