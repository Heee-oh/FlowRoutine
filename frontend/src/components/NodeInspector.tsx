import { memo, type ReactNode } from "react";
import type { Node } from "@xyflow/react";
import { HelpCircle, Plus, Trash2 } from "lucide-react";
import { DEFAULT_METRIC_WINDOW_MS } from "../store";
import type { HelpTopic } from "../help";
import type { FlowNodeData, HeaderInputMode, HeaderRow, RuntimeAuthSecret, SavedScenario } from "../flowTypes";
import {
  classifyTarget,
  commonHeaderNames,
  formatHeaderRows,
  headerRowsFromNode,
  numberValue,
  toNumber,
} from "../flowModel";
import { formatSavedAt } from "../format";

export const NodeInspector = memo(function NodeInspector({
  selectedNode,
  updateNode,
  onOpenHelp,
  savedScenarios,
  onLoadScenario,
  authSecret,
  updateAuthSecret,
}: {
  selectedNode: Node<FlowNodeData> | null;
  updateNode: (patch: Partial<FlowNodeData>) => void;
  onOpenHelp: (topic: HelpTopic) => void;
  savedScenarios: SavedScenario[];
  onLoadScenario: (scenario: SavedScenario) => void;
  authSecret: RuntimeAuthSecret;
  updateAuthSecret: (nodeId: string, patch: Partial<RuntimeAuthSecret>) => void;
}) {
  if (selectedNode) {
    return (
      <div className="inspector">
        <div className="inspector-header">
          <div>
            <div className="eyebrow">Inspector</div>
            <h2>{selectedNode.data.label}</h2>
          </div>
          <button
            type="button"
            className="secondary icon-button inspector-help-button"
            aria-label={`${selectedNode.data.label} help`}
            title={`${selectedNode.data.label} help`}
            onClick={() => onOpenHelp(selectedNode.data.kind)}
          >
            <HelpCircle size={16} />
          </button>
        </div>
        <NodeFields
          node={selectedNode}
          updateNode={updateNode}
          authSecret={authSecret}
          updateAuthSecret={updateAuthSecret}
        />
      </div>
    );
  }

  return (
    <div className="inspector">
      <div>
        <div className="eyebrow">Inspector</div>
        <h2>No node selected</h2>
      </div>
      <div className="inspector-note">
        Select a node to edit its settings. Start uses the Request, Engine, and Metrics nodes on the canvas.
      </div>
      <SavedScenarioList scenarios={savedScenarios} onLoadScenario={onLoadScenario} />
    </div>
  );
});

const SavedScenarioList = memo(function SavedScenarioList({
  scenarios,
  onLoadScenario,
}: {
  scenarios: SavedScenario[];
  onLoadScenario: (scenario: SavedScenario) => void;
}) {
  return (
    <div className="saved-scenarios">
      <div>
        <div className="eyebrow">Saved</div>
        <h2>Recent runs</h2>
      </div>
      {scenarios.length === 0 ? (
        <div className="inspector-note">
          Start a run to save the current graph here.
        </div>
      ) : (
        <div className="saved-scenario-scroll">
          <div className="saved-scenario-list">
            {scenarios.map((scenario) => (
              <button key={scenario.id} type="button" className="secondary saved-scenario-button" onClick={() => onLoadScenario(scenario)}>
                <span>{scenario.name}</span>
                <small>{formatSavedAt(scenario.savedAtUnixMs)}</small>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
});

const AuthFields = memo(function AuthFields({
  node,
  updateNode,
  authSecret,
  updateAuthSecret,
}: {
  node: Node<FlowNodeData>;
  updateNode: (patch: Partial<FlowNodeData>) => void;
  authSecret: RuntimeAuthSecret;
  updateAuthSecret: (nodeId: string, patch: Partial<RuntimeAuthSecret>) => void;
}) {
  const authType = node.data.authType ?? "none";
  return (
    <div className="field">
      <span className="field-label">Auth</span>
      <select value={authType} onChange={(event) => updateNode({ authType: event.target.value as FlowNodeData["authType"] })}>
        <option value="none">None</option>
        <option value="bearer">Bearer JWT</option>
        <option value="cookie">Cookie / Session</option>
        <option value="apiKey">API Key</option>
      </select>
      {authType === "bearer" ? (
        <input
          type="password"
          value={authSecret.token ?? ""}
          placeholder="JWT token"
          autoComplete="off"
          onChange={(event) => updateAuthSecret(node.id, { token: event.target.value })}
        />
      ) : null}
      {authType === "cookie" ? (
        <div className="auth-grid">
          <input
            value={node.data.authCookieName ?? "session"}
            placeholder="Cookie name"
            onChange={(event) => updateNode({ authCookieName: event.target.value })}
          />
          <input
            type="password"
            value={authSecret.cookieValue ?? ""}
            placeholder="Cookie value"
            autoComplete="off"
            onChange={(event) => updateAuthSecret(node.id, { cookieValue: event.target.value })}
          />
        </div>
      ) : null}
      {authType === "apiKey" ? (
        <div className="auth-grid">
          <input
            value={node.data.authApiKeyName ?? "X-Api-Key"}
            placeholder="Header name"
            onChange={(event) => updateNode({ authApiKeyName: event.target.value })}
          />
          <input
            type="password"
            value={authSecret.apiKeyValue ?? ""}
            placeholder="API key"
            autoComplete="off"
            onChange={(event) => updateAuthSecret(node.id, { apiKeyValue: event.target.value })}
          />
        </div>
      ) : null}
      {authType === "none" ? null : (
        <div className="inspector-note">
          Secret values stay in memory only and are not saved in Recent runs.
        </div>
      )}
    </div>
  );
});

const NodeFields = memo(function NodeFields({
  node,
  updateNode,
  authSecret,
  updateAuthSecret,
}: {
  node: Node<FlowNodeData>;
  updateNode: (patch: Partial<FlowNodeData>) => void;
  authSecret: RuntimeAuthSecret;
  updateAuthSecret: (nodeId: string, patch: Partial<RuntimeAuthSecret>) => void;
}) {
  switch (node.data.kind) {
    case "request":
      return (
        <>
          <Field label="Target URL">
            <input type="url" value={node.data.url ?? ""} onChange={(event) => updateNode({ url: event.target.value })} />
            <TargetBadge target={classifyTarget(node.data.url ?? "")} />
          </Field>
          <Field label="Method">
            <select value={node.data.method ?? "GET"} onChange={(event) => updateNode({ method: event.target.value })}>
              {["GET", "POST", "PUT", "PATCH", "DELETE"].map((method) => (
                <option key={method}>{method}</option>
              ))}
            </select>
          </Field>
          <AuthFields
            node={node}
            updateNode={updateNode}
            authSecret={authSecret}
            updateAuthSecret={updateAuthSecret}
          />
          <HeaderFields node={node} updateNode={updateNode} />
          <Field label="Body">
            <textarea rows={5} value={node.data.body ?? ""} spellCheck={false} onChange={(event) => updateNode({ body: event.target.value })} />
          </Field>
          <Field label="Capture JSON">
            <textarea
              rows={3}
              value={node.data.capturesText ?? ""}
              spellCheck={false}
              placeholder="token=data.accessToken"
              onChange={(event) => updateNode({ capturesText: event.target.value })}
            />
            <div className="inspector-note">
              Captured values can be reused as {"{{token}}"} in later URLs, headers, or bodies.
            </div>
          </Field>
        </>
      );
    case "engine":
      return (
        <>
          <div className="field-grid">
            <Field label="VUs">
              <NumberInput value={node.data.virtualUsers ?? 1} min={1} onChange={(value) => updateNode({ virtualUsers: value })} />
            </Field>
            <Field label="Duration ms">
              <NumberInput value={node.data.durationMs ?? 1} min={1} max={3_600_000} onChange={(value) => updateNode({ durationMs: value })} />
            </Field>
          </div>
          <div className="field-grid">
            <Field label="Timeout ms">
              <NumberInput value={node.data.requestTimeoutMs ?? 1} min={1} max={300_000} onChange={(value) => updateNode({ requestTimeoutMs: value })} />
            </Field>
            <Field label="Max conns">
              <NumberInput value={node.data.maxConnsPerHost ?? 10_000} min={1} onChange={(value) => updateNode({ maxConnsPerHost: value })} />
            </Field>
          </div>
          <div className="field-grid">
            <Field label="Rate limit RPS">
              <NumberInput value={node.data.rateLimitRps ?? 0} min={0} onChange={(value) => updateNode({ rateLimitRps: value })} />
            </Field>
            <Field label="Ramp-up ms">
              <NumberInput value={node.data.rampUpMs ?? 0} min={0} max={3_600_000} onChange={(value) => updateNode({ rampUpMs: value })} />
            </Field>
          </div>
        </>
      );
    case "assertion":
      return (
        <Field label="Expected status">
          <input value={node.data.expectedStatus ?? "2xx"} onChange={(event) => updateNode({ expectedStatus: event.target.value })} />
        </Field>
      );
    case "delay":
      return (
        <Field label="Delay ms">
          <NumberInput value={node.data.delayMs ?? 100} min={0} onChange={(value) => updateNode({ delayMs: value })} />
        </Field>
      );
    case "metrics":
      return (
        <>
          <Field label="Batch ms">
            <NumberInput value={node.data.batchIntervalMs ?? 150} min={100} max={200} onChange={(value) => updateNode({ batchIntervalMs: value })} />
          </Field>
          <Field label="Latency sample rate">
            <NumberInput value={node.data.latencySampleRate ?? 1} min={1} onChange={(value) => updateNode({ latencySampleRate: value })} />
          </Field>
        </>
      );
    case "window":
      return (
        <>
          <Field label="Window seconds">
            <NumberInput
              value={Math.max(1, Math.round(numberValue(node.data.windowMs, DEFAULT_METRIC_WINDOW_MS) / 1_000))}
              min={1}
              max={3_600}
              onChange={(value) => updateNode({ windowMs: Math.max(1, value) * 1_000 })}
            />
          </Field>
          <div className="inspector-note">
            Realtime chart points are retained for this duration.
          </div>
        </>
      );
  }
});

const HeaderFields = memo(function HeaderFields({
  node,
  updateNode,
}: {
  node: Node<FlowNodeData>;
  updateNode: (patch: Partial<FlowNodeData>) => void;
}) {
  const mode = node.data.headersMode ?? "direct";
  const rows = headerRowsFromNode(node.data);
  const setRows = (nextRows: HeaderRow[]) => {
    updateNode({
      headerRows: nextRows,
      headersText: formatHeaderRows(nextRows),
    });
  };
  const setMode = (nextMode: HeaderInputMode) => {
    if (nextMode === "form") {
      updateNode({
        headersMode: "form",
        headerRows: rows.length > 0 ? rows : [{ name: "", value: "" }],
      });
      return;
    }
    updateNode({
      headersMode: "direct",
      headersText: formatHeaderRows(rows),
    });
  };
  const updateRow = (index: number, patch: Partial<HeaderRow>) => {
    setRows(rows.map((row, currentIndex) => (currentIndex === index ? { ...row, ...patch } : row)));
  };
  const deleteRow = (index: number) => {
    const nextRows = rows.filter((_, currentIndex) => currentIndex !== index);
    setRows(nextRows.length > 0 ? nextRows : [{ name: "", value: "" }]);
  };

  return (
    <div className="field">
      <span className="field-label">Headers</span>
      <div className="mode-toggle" role="group" aria-label="Header input mode">
        <button type="button" className={mode === "direct" ? "" : "secondary"} onClick={() => setMode("direct")}>Direct</button>
        <button type="button" className={mode === "form" ? "" : "secondary"} onClick={() => setMode("form")}>Form</button>
      </div>
      {mode === "direct" ? (
        <textarea rows={4} value={node.data.headersText ?? ""} spellCheck={false} onChange={(event) => updateNode({ headersText: event.target.value })} />
      ) : (
        <div className="header-form">
          <datalist id="common-header-names">
            {commonHeaderNames.map((name) => (
              <option key={name} value={name} />
            ))}
          </datalist>
          {rows.map((row, index) => (
            <div key={index} className="header-row">
              <input
                list="common-header-names"
                value={row.name}
                placeholder="Header"
                onChange={(event) => updateRow(index, { name: event.target.value })}
              />
              <input
                value={row.value}
                placeholder="Value"
                onChange={(event) => updateRow(index, { value: event.target.value })}
              />
              <button type="button" className="secondary header-delete" aria-label="Delete header" onClick={() => deleteRow(index)}>
                <Trash2 size={14} />
              </button>
            </div>
          ))}
          <button type="button" className="secondary add-header-button" onClick={() => setRows(rows.concat({ name: "", value: "" }))}>
            <Plus size={15} /> Add header
          </button>
        </div>
      )}
    </div>
  );
});

const TargetBadge = memo(function TargetBadge({ target }: { target: ReturnType<typeof classifyTarget> }) {
  return <div className={`target-badge target-badge-${target.tone}`}>Current target: {target.label}</div>;
});

const Field = memo(function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label>
      {label}
      {children}
    </label>
  );
});

const NumberInput = memo(function NumberInput({
  value,
  min,
  max,
  onChange,
}: {
  value: number;
  min: number;
  max?: number;
  onChange: (value: number) => void;
}) {
  return (
    <input
      type="number"
      min={min}
      max={max}
      value={value}
      onChange={(event) => onChange(toNumber(event.target.value, min))}
    />
  );
});
