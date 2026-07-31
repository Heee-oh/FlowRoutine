import { describe, expect, it } from "vitest";

import {
  assertImportFileSize,
  assertImportJSONDepth,
  assertImportRequestCount,
  assertImportTextSize,
} from "./importValidation";

describe("import validation", () => {
  it("rejects oversized files and UTF-8 text", () => {
    expect(() => assertImportFileSize(5, "HAR file", 5)).not.toThrow();
    expect(() => assertImportTextSize("abc", "Postman collection", 3)).not.toThrow();
    expect(() => assertImportFileSize(6, "HAR file", 5)).toThrow(
      "HAR file exceeds the 5 byte file limit",
    );
    expect(() => assertImportTextSize("éé", "Postman collection", 3)).toThrow(
      "Postman collection exceeds the 3 byte file limit",
    );
  });

  it("rejects deeply nested JSON without counting brackets inside strings", () => {
    expect(() => assertImportJSONDepth('{"text":"[[{\\\"","child":{}}', "HAR file", 2)).not.toThrow();
    expect(() => assertImportJSONDepth('[[[0]]]', "HAR file", 2)).toThrow(
      "HAR file exceeds the 2-level nesting limit",
    );
  });

  it("enforces the request-count limit", () => {
    expect(() => assertImportRequestCount(2, "HAR file", 2)).not.toThrow();
    expect(() => assertImportRequestCount(3, "HAR file", 2)).toThrow(
      "HAR file exceeds the 2-request limit",
    );
  });
});
