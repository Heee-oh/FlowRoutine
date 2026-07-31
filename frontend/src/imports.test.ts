import { describe, expect, it } from "vitest";

import { parseHarArchive } from "./harImport";
import { MAX_IMPORT_JSON_DEPTH, MAX_IMPORT_REQUESTS } from "./importValidation";
import { parsePostmanCollection } from "./postmanImport";

describe("parseHarArchive", () => {
  it("normalizes API requests and filters browser assets", () => {
    const requests = parseHarArchive(JSON.stringify({
      log: {
        entries: [
          {
            request: {
              method: "post",
              url: "https://api.example.com/v1/items?debug=true",
              headers: [
                { name: "Host", value: "api.example.com" },
                { name: "Accept", value: "application/json" },
                { name: "X-Trace", value: "trace-1" },
              ],
              postData: {
                mimeType: "application/x-www-form-urlencoded",
                params: [{ name: "name", value: "tea" }],
              },
            },
            response: { content: { mimeType: "application/json" } },
          },
          {
            request: {
              method: "GET",
              url: "https://api.example.com/app.js",
            },
            response: { content: { mimeType: "application/javascript" } },
          },
        ],
      },
    }));

    expect(requests).toMatchInlineSnapshot(`
      [
        {
          "name": "POST /v1/items ?",
          "settings": {
            "body": "name=tea",
            "headerRows": [
              {
                "name": "Accept",
                "value": "application/json",
              },
              {
                "name": "X-Trace",
                "value": "trace-1",
              },
            ],
            "headersMode": "form",
            "headersText": "Accept: application/json
      X-Trace: trace-1",
            "method": "POST",
            "url": "https://api.example.com/v1/items?debug=true",
          },
        },
      ]
    `);
  });

  it("rejects malformed and empty archives", () => {
    expect(() => parseHarArchive("{")).toThrow("HAR file must be JSON");
    expect(() => parseHarArchive("{}")).toThrow("HAR file is missing log.entries");
    expect(() => parseHarArchive(JSON.stringify({ log: { entries: [] } }))).toThrow(
      "HAR file has no importable HTTP API requests",
    );
  });

  it("rejects archives above the request limit", () => {
    const entries = Array.from({ length: MAX_IMPORT_REQUESTS + 1 }, (_, index) => ({
      request: { method: "GET", url: `https://api.example.com/items/${index}` },
      response: { content: { mimeType: "application/json" } },
    }));

    expect(() => parseHarArchive(JSON.stringify({ log: { entries } }))).toThrow(
      `${MAX_IMPORT_REQUESTS}-request limit`,
    );
  });

  it("rejects deeply nested input before parsing the archive", () => {
    const raw = `${"[".repeat(MAX_IMPORT_JSON_DEPTH + 1)}0${"]".repeat(MAX_IMPORT_JSON_DEPTH + 1)}`;
    expect(() => parseHarArchive(raw)).toThrow(`${MAX_IMPORT_JSON_DEPTH}-level nesting limit`);
  });
});

describe("parsePostmanCollection", () => {
  it("flattens folders and normalizes request fields", () => {
    const requests = parsePostmanCollection(JSON.stringify({
      item: [
        {
          name: "Folder",
          item: [{
            name: "Search",
            request: {
              method: "TRACE",
              url: {
                protocol: "https",
                host: ["api", "example", "com"],
                path: ["v1", "items"],
                query: [
                  { key: "q", value: "tea" },
                  { key: "ignored", value: "1", disabled: true },
                ],
              },
              header: [
                { key: "Accept", value: "application/json" },
                { key: "X-Ignored", value: "secret", disabled: true },
              ],
              body: {
                mode: "urlencoded",
                urlencoded: [
                  { key: "page", value: "1" },
                  { key: "ignored", value: "2", disabled: true },
                ],
              },
            },
          }],
        },
        {
          name: "Health",
          request: "https://api.example.com/health",
        },
      ],
    }));

    expect(requests).toMatchInlineSnapshot(`
      [
        {
          "name": "Folder / Search",
          "settings": {
            "body": "page=1",
            "headerRows": [
              {
                "name": "Accept",
                "value": "application/json",
              },
            ],
            "headersMode": "form",
            "headersText": "Accept: application/json",
            "method": "GET",
            "url": "https://api.example.com/v1/items?q=tea",
          },
        },
        {
          "name": "Health",
          "settings": {
            "body": "",
            "headerRows": [],
            "headersMode": "form",
            "headersText": "",
            "method": "GET",
            "url": "https://api.example.com/health",
          },
        },
      ]
    `);
  });

  it("rejects malformed and request-free collections", () => {
    expect(() => parsePostmanCollection("{")).toThrow("Postman collection must be JSON");
    expect(() => parsePostmanCollection("{}")).toThrow("Postman collection is missing an item array");
    expect(() => parsePostmanCollection(JSON.stringify({ item: [{ name: "folder" }] }))).toThrow(
      "Postman collection has no runnable HTTP requests",
    );
  });

  it("rejects collections above the request limit", () => {
    const item = Array.from({ length: MAX_IMPORT_REQUESTS + 1 }, (_, index) => ({
      name: `Request ${index}`,
      request: `https://api.example.com/items/${index}`,
    }));

    expect(() => parsePostmanCollection(JSON.stringify({ item }))).toThrow(
      `${MAX_IMPORT_REQUESTS}-request limit`,
    );
  });
});
