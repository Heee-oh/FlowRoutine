import { describe, expect, it } from "vitest";

import { parseHarArchive } from "./harImport";
import { parsePostmanCollection } from "./postmanImport";
import { openAPIEndpointToRequestSettings } from "./flowModel";

describe("import secret sanitization", () => {
  it("removes literal secrets from Postman imports", () => {
    const imported = parsePostmanCollection(JSON.stringify({
      item: [{
        name: "signed request",
        request: {
          method: "POST",
          url: "https://example.com/items?X-Amz-Signature=postman-url-secret",
          header: [
            { key: "aUtHoRiZaTiOn", value: "Bearer postman-auth-secret" },
            { key: "X-API-Key", value: "postman-api-secret" },
          ],
          body: {
            mode: "raw",
            raw: JSON.stringify({
              password: "postman-body-secret",
              safe: "kept",
            }),
          },
        },
      }],
    }));
    const serialized = JSON.stringify(imported);

    expect(serialized).not.toMatch(/postman-(?:url|auth|api|body)-secret/);
    expect(serialized).toContain("SECRET_X_AMZ_SIGNATURE");
    expect(serialized).toContain("SECRET_AUTHORIZATION");
    expect(serialized).toContain("SECRET_X_API_KEY");
    expect(serialized).toContain("SECRET_PASSWORD");
    expect(serialized).toContain("kept");
  });

  it("removes literal secrets from HAR imports", () => {
    const imported = parseHarArchive(JSON.stringify({
      log: {
        entries: [{
          request: {
            method: "POST",
            url: "https://example.com/items?access_token=har-url-secret",
            headers: [
              { name: "X-Auth-Token", value: "har-header-secret" },
              { name: "Accept", value: "application/json" },
            ],
            postData: {
              mimeType: "application/x-www-form-urlencoded",
              text: "client_secret=har-body-secret&name=kept",
            },
          },
          response: {
            content: { mimeType: "application/json" },
          },
        }],
      },
    }));
    const serialized = JSON.stringify(imported);

    expect(serialized).not.toMatch(/har-(?:url|header|body)-secret/);
    expect(serialized).toContain("SECRET_ACCESS_TOKEN");
    expect(serialized).toContain("SECRET_X_AUTH_TOKEN");
    expect(serialized).toContain("SECRET_CLIENT_SECRET");
    expect(serialized).toContain("application/json");
  });

  it("removes sensitive OpenAPI examples from generated requests", () => {
    const settings = openAPIEndpointToRequestSettings({
      method: "post",
      path: "/items",
      summary: "",
      operationId: "createItem",
      tags: [],
      serverUrl: "https://example.com",
      deprecated: false,
      auth: { type: "none", name: "" },
      parameters: [{
        name: "access_token",
        in: "query",
        required: true,
        description: "",
        sample: "openapi-url-secret",
      }],
      bodySample: "{\"client_secret\":\"openapi-body-secret\",\"safe\":\"kept\"}",
    }, "https://example.com/openapi.json");
    const serialized = JSON.stringify(settings);

    expect(serialized).not.toMatch(/openapi-(?:url|body)-secret/);
    expect(serialized).toContain("SECRET_ACCESS_TOKEN");
    expect(serialized).toContain("SECRET_CLIENT_SECRET");
    expect(serialized).toContain("kept");
  });
});
