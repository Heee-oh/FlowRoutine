import { memo, useMemo, useState } from "react";

import { formatBytes } from "../format";
import type {
  ImportedRequest,
  RequestImportMode,
  RequestImportPreview,
} from "../importGraph";

export const ImportPreviewDialog = memo(function ImportPreviewDialog({
  appendAvailable,
  error,
  onCancel,
  onConfirm,
  preview,
}: {
  appendAvailable: boolean;
  error: string;
  onCancel: () => void;
  onConfirm: (requests: ImportedRequest[], mode: RequestImportMode) => void;
  preview: RequestImportPreview;
}) {
  const [mode, setMode] = useState<RequestImportMode | "">(appendAvailable ? "append" : "");
  const [query, setQuery] = useState("");
  const [selectedIndexes, setSelectedIndexes] = useState<Set<number>>(
    () => new Set(preview.requests.map((_, index) => index)),
  );
  const normalizedQuery = query.trim().toLowerCase();
  const visibleIndexes = useMemo(() => preview.requests
    .map((request, index) => ({ index, request }))
    .filter(({ request }) => !normalizedQuery || requestSearchText(request).includes(normalizedQuery))
    .map(({ index }) => index), [normalizedQuery, preview.requests]);
  const allVisibleSelected = visibleIndexes.length > 0
    && visibleIndexes.every((index) => selectedIndexes.has(index));
  const selectedRequests = preview.requests.filter((_, index) => selectedIndexes.has(index));

  const updateVisibleSelection = (selected: boolean) => {
    setSelectedIndexes((current) => {
      const next = new Set(current);
      for (const index of visibleIndexes) {
        if (selected) {
          next.add(index);
        } else {
          next.delete(index);
        }
      }
      return next;
    });
  };

  return (
    <div className="modal-backdrop" role="presentation">
      <form
        className="modal request-import-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="request-import-title"
        onSubmit={(event) => {
          event.preventDefault();
          if (mode && selectedRequests.length > 0) {
            onConfirm(selectedRequests, mode);
          }
        }}
      >
        <div className="request-import-heading">
          <div>
            <div className="eyebrow">Import preview</div>
            <h2 id="request-import-title">{preview.source} requests</h2>
          </div>
          <div className="request-import-file" title={preview.fileName}>
            <strong>{preview.fileName}</strong>
            <span>{formatBytes(preview.fileSize)} · {preview.requests.length} requests</span>
          </div>
        </div>

        <section className="request-import-results" aria-label="Requests available to import">
          <div className="request-import-controls">
            <input
              autoFocus
              type="search"
              value={query}
              placeholder="Filter by name, method, or URL"
              aria-label="Filter imported requests"
              onChange={(event) => setQuery(event.target.value)}
            />
            <button
              type="button"
              className="secondary"
              disabled={visibleIndexes.length === 0}
              onClick={() => updateVisibleSelection(!allVisibleSelected)}
            >
              {allVisibleSelected ? "Clear filtered" : "Select filtered"}
            </button>
          </div>
          <div className="request-import-summary">
            <span>{selectedRequests.length} selected</span>
            <span>{visibleIndexes.length} shown</span>
          </div>
          <div className="request-import-list">
            {visibleIndexes.length === 0 ? (
              <div className="endpoint-empty">No requests match this filter.</div>
            ) : visibleIndexes.map((index) => {
              const request = preview.requests[index];
              const method = requestMethod(request);
              const url = requestURL(request);
              return (
                <label key={`${index}-${request.name}`} className="request-import-row">
                  <input
                    type="checkbox"
                    checked={selectedIndexes.has(index)}
                    onChange={(event) => {
                      setSelectedIndexes((current) => {
                        const next = new Set(current);
                        if (event.target.checked) {
                          next.add(index);
                        } else {
                          next.delete(index);
                        }
                        return next;
                      });
                    }}
                  />
                  <span className={`method-badge method-${method.toLowerCase()}`}>{method}</span>
                  <span className="request-import-main">
                    <strong>{request.name}</strong>
                    <small>{url}</small>
                  </span>
                </label>
              );
            })}
          </div>
        </section>

        <fieldset className="request-import-modes">
          <legend>Import behavior</legend>
          <label className={`request-import-mode${mode === "append" ? " selected" : ""}${appendAvailable ? "" : " disabled"}`}>
            <input
              type="radio"
              name="request-import-mode"
              value="append"
              checked={mode === "append"}
              disabled={!appendAvailable}
              onChange={() => setMode("append")}
            />
            <span>
              <strong>Append before Engine</strong>
              <small>{appendAvailable
                ? "Keep the current graph and insert selected requests into its execution path."
                : "Fix the current graph before appending requests."}</small>
            </span>
          </label>
          <label className={`request-import-mode replace${mode === "replace" ? " selected" : ""}`}>
            <input
              type="radio"
              name="request-import-mode"
              value="replace"
              checked={mode === "replace"}
              onChange={() => setMode("replace")}
            />
            <span>
              <strong>Replace current graph</strong>
              <small>Create a new linear scenario. The current graph is retained for one-step Undo.</small>
            </span>
          </label>
        </fieldset>

        {error ? <div className="dialog-error">{error}</div> : null}
        <div className="modal-actions">
          <button type="button" className="secondary" onClick={onCancel}>Cancel</button>
          <button
            type="submit"
            className={mode === "replace" ? "danger" : undefined}
            disabled={!mode || selectedRequests.length === 0}
          >
            {mode === "append"
              ? `Append ${selectedRequests.length} request${selectedRequests.length === 1 ? "" : "s"}`
              : mode === "replace"
                ? `Replace with ${selectedRequests.length} request${selectedRequests.length === 1 ? "" : "s"}`
                : "Choose import behavior"}
          </button>
        </div>
      </form>
    </div>
  );
});

function requestSearchText(request: ImportedRequest) {
  return [request.name, requestMethod(request), requestURL(request)].join(" ").toLowerCase();
}

function requestMethod(request: ImportedRequest) {
  return typeof request.settings.method === "string"
    ? request.settings.method.toUpperCase()
    : "GET";
}

function requestURL(request: ImportedRequest) {
  return typeof request.settings.url === "string" ? request.settings.url : "";
}
