import { memo, useMemo, useState } from "react";
import type { HelpLanguage, HelpTopic } from "../help";
import { helpDialogTitle, nodeHelpItems, overviewHelpItems } from "../help";
import { nodePalette } from "../flowModel";
import type { SafetyAssessment } from "../flowTypes";
import type { OpenAPIEndpoint, OpenAPIImportResponse, StartRequest } from "../types";
import { formatBytes, formatNumber } from "../format";

export const HelpDialog = memo(function HelpDialog({
  topic,
  language,
  setLanguage,
  onClose,
}: {
  topic: HelpTopic;
  language: HelpLanguage;
  setLanguage: (language: HelpLanguage) => void;
  onClose: () => void;
}) {
  const nodeLabel = nodePalette.find((item) => item.kind === topic)?.label ?? "Node";
  const title = helpDialogTitle(topic, language, nodeLabel);
  const items = topic === "overview" ? overviewHelpItems : nodeHelpItems[topic];

  return (
    <div className="modal-backdrop" role="presentation">
      <div className="modal help-modal" role="dialog" aria-modal="true" aria-labelledby="help-title">
        <div className="help-header">
          <div>
            <div className="eyebrow">Help</div>
            <h2 id="help-title">{title}</h2>
          </div>
          <select
            className="language-select"
            aria-label="Help language"
            value={language}
            onChange={(event) => setLanguage(event.target.value as HelpLanguage)}
          >
            <option value="ko">한국어</option>
            <option value="en">English</option>
          </select>
        </div>
        <div className="help-list">
          {items.map((item) => (
            <div key={item.title} className="help-item">
              <strong>{item.title}</strong>
              <span>{item.description[language]}</span>
            </div>
          ))}
        </div>
        <button type="button" className="secondary" onClick={onClose}>{language === "ko" ? "닫기" : "Close"}</button>
      </div>
    </div>
  );
});

export const StartConfirmDialog = memo(function StartConfirmDialog({
  safety,
  request,
  onCancel,
  onConfirm,
}: {
  safety: SafetyAssessment;
  request: StartRequest | null;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const config = request?.config;
  return (
    <div className="modal-backdrop" role="presentation">
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="start-confirm-title">
        <div>
          <div className="eyebrow">Confirm target</div>
          <h2 id="start-confirm-title">Start load test</h2>
        </div>
        <div className="target-summary">
          <span>{safety.target.host}</span>
          <strong>{safety.target.label}</strong>
        </div>
        <ul className="warning-list">
          {safety.warnings.map((warning) => (
            <li key={warning}>{warning}</li>
          ))}
        </ul>
        <div className="run-summary">
          <span>{formatNumber(config?.virtualUsers ?? 0)} VUs</span>
          <span>{formatNumber(config?.durationMs ?? 0)} ms</span>
          <span>{(config?.rateLimitRps ?? 0) > 0 ? `${formatNumber(config?.rateLimitRps ?? 0)} RPS cap` : "Unlimited RPS"}</span>
          {safety.estimatedMemoryBytes > 0 && <span>~{formatBytes(safety.estimatedMemoryBytes)} peak</span>}
          {safety.estimatedConnections > 0 && <span>Up to {formatNumber(safety.estimatedConnections)} connections</span>}
          {safety.targetHosts > 1 && <span>{formatNumber(safety.targetHosts)} target hosts</span>}
        </div>
        <div className="modal-actions">
          <button type="button" className="secondary" onClick={onCancel}>Cancel</button>
          <button type="button" className="danger" onClick={onConfirm} disabled={safety.target.tone === "invalid"}>Start test</button>
        </div>
      </div>
    </div>
  );
});

export const OpenAPIImportDialog = memo(function OpenAPIImportDialog({
  error,
  imported,
  loading,
  message,
  onCancel,
  onSelectEndpoint,
  onSubmit,
}: {
  error: string;
  imported: OpenAPIImportResponse | null;
  loading: boolean;
  message: string;
  onCancel: () => void;
  onSelectEndpoint: (endpoint: OpenAPIEndpoint) => void;
  onSubmit: (url: string) => void | Promise<void>;
}) {
  const [url, setUrl] = useState("http://localhost:8080/v3/api-docs");
  const [query, setQuery] = useState("");
  const trimmedUrl = url.trim();
  const normalizedQuery = query.trim().toLowerCase();
  const filteredEndpoints = useMemo(() => {
    if (!imported) {
      return [];
    }
    if (!normalizedQuery) {
      return imported.endpoints;
    }
    return imported.endpoints.filter((endpoint) => [
      endpoint.method,
      endpoint.path,
      endpoint.summary,
      endpoint.operationId,
      endpoint.tags.join(" "),
    ].join(" ").toLowerCase().includes(normalizedQuery));
  }, [imported, normalizedQuery]);

  return (
    <div className="modal-backdrop" role="presentation">
      <form
        className="modal openapi-import-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="openapi-import-title"
        onSubmit={(event) => {
          event.preventDefault();
          if (trimmedUrl) {
            onSubmit(trimmedUrl);
          }
        }}
      >
        <div>
          <div className="eyebrow">Import</div>
          <h2 id="openapi-import-title">OpenAPI / Swagger</h2>
        </div>
        <label className="field-stack">
          <span>OpenAPI JSON URL</span>
          <input
            autoFocus
            type="url"
            value={url}
            placeholder="http://localhost:8080/v3/api-docs"
            onChange={(event) => setUrl(event.target.value)}
          />
        </label>
        {error ? <div className="dialog-error">{error}</div> : null}
        {message ? <div className="dialog-success">{message}</div> : null}
        {imported ? (
          <section className="endpoint-results" aria-label="Imported endpoints">
            <div className="endpoint-results-head">
              <div>
                <strong>{imported.endpoints.length} endpoints</strong>
                <span>{imported.servers[0]?.url || imported.sourceUrl}</span>
              </div>
              <input
                type="search"
                value={query}
                placeholder="Search endpoints"
                aria-label="Search endpoints"
                onChange={(event) => setQuery(event.target.value)}
              />
            </div>
            <div className="endpoint-list">
              {filteredEndpoints.length === 0 ? (
                <div className="endpoint-empty">No endpoints match this search.</div>
              ) : (
                filteredEndpoints.map((endpoint) => (
                  <button
                    key={`${endpoint.method} ${endpoint.path} ${endpoint.operationId}`}
                    type="button"
                    className="secondary endpoint-row"
                    disabled={loading}
                    onClick={() => onSelectEndpoint(endpoint)}
                  >
                    <span className={`method-badge method-${endpoint.method.toLowerCase()}`}>{endpoint.method}</span>
                    <span className="endpoint-main">
                      <strong>{endpoint.path}</strong>
                      <small>{endpoint.summary || endpoint.operationId || "No summary"}</small>
                    </span>
                    {endpoint.deprecated ? <span className="deprecated-badge">Deprecated</span> : null}
                  </button>
                ))
              )}
            </div>
          </section>
        ) : null}
        <div className="modal-actions">
          <button type="button" className="secondary" onClick={onCancel} disabled={loading}>Cancel</button>
          <button type="submit" disabled={!trimmedUrl || loading}>{loading ? "Loading" : "Load endpoints"}</button>
        </div>
      </form>
    </div>
  );
});
