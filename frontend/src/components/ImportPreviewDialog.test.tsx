import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { ImportPreviewDialog } from "./ImportPreviewDialog";

const preview = {
  id: 1,
  fileName: "requests.json",
  fileSize: 128,
  source: "Postman" as const,
  requests: [
    { name: "List tea", settings: { method: "GET", url: "https://api.example.com/tea" } },
    { name: "Create tea", settings: { method: "POST", url: "https://api.example.com/tea" } },
  ],
};

describe("ImportPreviewDialog", () => {
  it("defaults to safe append when the current graph is valid", () => {
    const html = renderToStaticMarkup(
      <ImportPreviewDialog
        appendAvailable
        error=""
        preview={preview}
        onCancel={() => undefined}
        onConfirm={() => undefined}
      />,
    );

    expect(html).toContain("Append 2 requests");
    expect(html).toContain("2 selected");
    expect(html).toContain("Replace current graph");
  });

  it("requires an explicit destructive choice when append is unavailable", () => {
    const html = renderToStaticMarkup(
      <ImportPreviewDialog
        appendAvailable={false}
        error=""
        preview={{ ...preview, fileName: "<unsafe>.har", source: "HAR" }}
        onCancel={() => undefined}
        onConfirm={() => undefined}
      />,
    );

    expect(html).toContain("Choose import behavior");
    expect(html).toContain("Fix the current graph before appending requests.");
    expect(html).toContain("&lt;unsafe&gt;.har");
    expect(html).not.toContain("<unsafe>.har");
  });
});
