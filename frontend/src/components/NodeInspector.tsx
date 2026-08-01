import { memo, type ReactNode } from "react";
import type { Node } from "@xyflow/react";
import { HelpCircle, Plus, Trash2 } from "lucide-react";
import {
  DEFAULT_METRIC_BATCH_INTERVAL_MS,
  DEFAULT_METRIC_WINDOW_MS,
  MAX_METRIC_BATCH_INTERVAL_MS,
  MIN_METRIC_BATCH_INTERVAL_MS,
} from "../store";
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
import { formatDuration, formatSavedAt } from "../format";
import { collectSecretPlaceholderNames } from "../secretSanitization";
import {
  convertLoadProfileMode,
  isArrivalMode,
  isRampingMode,
  legacyLoadProfile,
  loadModeOptions,
  loadProfileSummary,
} from "../loadProfile";
import type { LoadMode, LoadProfile } from "../types";

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
          autoComplete="new-password"
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
            autoComplete="new-password"
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
            autoComplete="new-password"
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
          <SecretBindingFields
            node={node}
            authSecret={authSecret}
            updateAuthSecret={updateAuthSecret}
          />
          <Field label="Capture JSON">
            <textarea
              rows={3}
              value={node.data.capturesText ?? ""}
              spellCheck={false}
              placeholder="token@iteration:success=data.accessToken"
              onChange={(event) => updateNode({ capturesText: event.target.value })}
            />
            <div className="inspector-note">
              Use name[@iteration|run][:success|any|2xx|200]=JSON.path. Iteration values reset every loop;
              run values keep the first successful value per virtual user.
            </div>
            <div className="inspector-note">
              Reuse values as {"{{token}}"} in later URLs, headers, or bodies. Missing values skip the request
              and count as template assertions.
            </div>
          </Field>
        </>
      );
    case "engine":
      return (
        <>
          <LoadProfileFields node={node} updateNode={updateNode} />
          <div className="field-grid">
            <Field label="Timeout ms">
              <NumberInput value={node.data.requestTimeoutMs ?? 1} min={1} max={300_000} onChange={(value) => updateNode({ requestTimeoutMs: value })} />
            </Field>
            <Field label="Max conns">
              <NumberInput value={node.data.maxConnsPerHost ?? 10_000} min={1} max={100_000} onChange={(value) => updateNode({ maxConnsPerHost: value })} />
            </Field>
          </div>
          <div className="field-grid">
            <Field label="Read buffer bytes">
              <NumberInput value={node.data.readBufferSize ?? 4_096} min={1_024} max={1_048_576} onChange={(value) => updateNode({ readBufferSize: value })} />
            </Field>
            <Field label="Write buffer bytes">
              <NumberInput value={node.data.writeBufferSize ?? 4_096} min={1_024} max={1_048_576} onChange={(value) => updateNode({ writeBufferSize: value })} />
            </Field>
          </div>
          <Field label="Max response bytes">
            <NumberInput value={node.data.maxResponseBytes ?? 1_048_576} min={1_024} max={67_108_864} onChange={(value) => updateNode({ maxResponseBytes: value })} />
          </Field>
          {!isArrivalMode(engineProfile(node.data).mode) ? (
            <Field label="Request rate cap RPS">
              <NumberInput value={node.data.rateLimitRps ?? 0} min={0} max={10_000_000} onChange={(value) => updateNode({ rateLimitRps: value })} />
            </Field>
          ) : (
            <div className="inspector-note">
              Arrival-rate scheduling controls iteration starts; the request-rate cap is disabled for this mode.
            </div>
          )}
        </>
      );
    case "assertion":
      return (
        <>
          <Field label="Assertion type">
            <select
              value={node.data.assertionType ?? "status"}
              onChange={(event) => updateNode({ assertionType: event.target.value as FlowNodeData["assertionType"] })}
            >
              <option value="status">Status</option>
              <option value="header">Header</option>
              <option value="json">JSON body</option>
              <option value="responseLatency">Response latency</option>
              <option value="stepLatency">Step latency</option>
            </select>
          </Field>
          {(node.data.assertionType ?? "status") === "status" ? (
            <Field label="Expected status">
              <input value={node.data.expectedStatus ?? "2xx"} onChange={(event) => updateNode({ expectedStatus: event.target.value })} />
            </Field>
          ) : null}
          {(node.data.assertionType ?? "status") === "header" ? (
            <>
              <Field label="Header name">
                <input value={node.data.assertionHeaderName ?? "Content-Type"} onChange={(event) => updateNode({ assertionHeaderName: event.target.value })} />
              </Field>
              <AssertionOperatorFields node={node} updateNode={updateNode} label="Header value" />
            </>
          ) : null}
          {(node.data.assertionType ?? "status") === "json" ? (
            <>
              <Field label="JSON path">
                <input value={node.data.assertionJSONPath ?? "$.data.id"} onChange={(event) => updateNode({ assertionJSONPath: event.target.value })} />
              </Field>
              <AssertionOperatorFields node={node} updateNode={updateNode} label="Expected value" json />
            </>
          ) : null}
          {(node.data.assertionType === "responseLatency" || node.data.assertionType === "stepLatency") ? (
            <Field label="Maximum latency ms">
              <NumberInput value={node.data.assertionMaxLatencyMs ?? 500} min={1} max={3_600_000} onChange={(value) => updateNode({ assertionMaxLatencyMs: value })} />
            </Field>
          ) : null}
          <Field label="On failure">
            <select
              value={node.data.assertionFailureMode ?? "continue"}
              onChange={(event) => updateNode({ assertionFailureMode: event.target.value as FlowNodeData["assertionFailureMode"] })}
            >
              <option value="continue">Count and continue</option>
              <option value="stop">Count and stop iteration</option>
              <option value="countOnly">Count only</option>
            </select>
          </Field>
          <div className="inspector-note">
            Response latency measures HTTP time; step latency also includes pacing, templates, and captures.
          </div>
        </>
      );
    case "delay":
      return (
          <Field label="Delay ms">
            <NumberInput value={node.data.delayMs ?? 100} min={0} max={300_000} onChange={(value) => updateNode({ delayMs: value })} />
        </Field>
      );
    case "metrics":
      return (
        <>
          <Field label="Batch ms">
            <NumberInput
              value={node.data.batchIntervalMs ?? DEFAULT_METRIC_BATCH_INTERVAL_MS}
              min={MIN_METRIC_BATCH_INTERVAL_MS}
              max={MAX_METRIC_BATCH_INTERVAL_MS}
              onChange={(value) => updateNode({ batchIntervalMs: value })}
            />
          </Field>
          <div className="inspector-note">
            Use slower updates for long runs; summaries remain cumulative and exact.
          </div>
          <Field label="Latency sample rate">
            <NumberInput value={node.data.latencySampleRate ?? 1} min={1} max={1_000_000} onChange={(value) => updateNode({ latencySampleRate: value })} />
          </Field>
          <div className="field-grid">
            <Field label="Max fail %">
              <NumberInput value={node.data.maxFailureRatePct ?? 1} min={0} onChange={(value) => updateNode({ maxFailureRatePct: value })} />
            </Field>
            <Field label="Min RPS">
              <NumberInput value={node.data.minRps ?? 0} min={0} onChange={(value) => updateNode({ minRps: value })} />
            </Field>
          </div>
          <div className="field-grid">
            <Field label="Max P95 ms">
              <NumberInput value={node.data.maxP95LatencyMs ?? 500} min={0} onChange={(value) => updateNode({ maxP95LatencyMs: value })} />
            </Field>
            <Field label="Max P99 ms">
              <NumberInput value={node.data.maxP99LatencyMs ?? 1000} min={0} onChange={(value) => updateNode({ maxP99LatencyMs: value })} />
            </Field>
          </div>
          <div className="inspector-note">
            Set a gate to 0 to disable that pass/fail check.
          </div>
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

const AssertionOperatorFields = memo(function AssertionOperatorFields({
  node,
  updateNode,
  label,
  json = false,
}: {
  node: Node<FlowNodeData>;
  updateNode: (patch: Partial<FlowNodeData>) => void;
  label: string;
  json?: boolean;
}) {
  const operator = node.data.assertionOperator ?? (json ? "equals" : "exists");
  const valueType = node.data.assertionValueType ?? "string";
  return (
    <>
      <Field label="Operator">
        <select value={operator} onChange={(event) => updateNode({ assertionOperator: event.target.value as FlowNodeData["assertionOperator"] })}>
          <option value="exists">Exists</option>
          <option value="equals">Equals</option>
        </select>
      </Field>
      {operator === "equals" && json ? (
        <Field label="Value type">
          <select value={valueType} onChange={(event) => updateNode({ assertionValueType: event.target.value as FlowNodeData["assertionValueType"] })}>
            <option value="string">String</option>
            <option value="number">Number</option>
            <option value="boolean">Boolean</option>
            <option value="null">Null</option>
          </select>
        </Field>
      ) : null}
      {operator === "equals" && (!json || valueType !== "null") ? (
        <Field label={label}>
          <input value={node.data.assertionExpected ?? ""} onChange={(event) => updateNode({ assertionExpected: event.target.value })} />
        </Field>
      ) : null}
    </>
  );
});

const LoadProfileFields = memo(function LoadProfileFields({
  node,
  updateNode,
}: {
  node: Node<FlowNodeData>;
  updateNode: (patch: Partial<FlowNodeData>) => void;
}) {
  const profile = engineProfile(node.data);
  const arrival = isArrivalMode(profile.mode);
  const ramping = isRampingMode(profile.mode);
  const targetLabel = arrival ? "Iterations / sec" : "Virtual users";
  const targetMax = arrival ? 10_000_000 : 100_000;
  const commit = (next: LoadProfile) => updateNode({ loadProfile: next });
  const updateStage = (index: number, patch: Partial<LoadProfile["stages"][number]>) => {
    const stages = profile.stages.map((stage, current) => current === index ? { ...stage, ...patch } : stage);
    commit({ ...profile, stages });
  };
  const preview = profilePreview(profile);

  return (
    <div className="load-profile-editor">
      <Field label="Execution mode">
        <select
          value={profile.mode}
          onChange={(event) => commit(convertLoadProfileMode(profile, event.target.value as LoadMode))}
        >
          {loadModeOptions.map((option) => (
            <option key={option.value} value={option.value}>{option.label}</option>
          ))}
        </select>
      </Field>
      <div className="field-grid">
        <Field label={`Start ${targetLabel.toLowerCase()}`}>
          <NumberInput
            value={profile.startTarget}
            min={ramping ? 0 : 1}
            max={targetMax}
            onChange={(value) => commit({
              ...profile,
              startTarget: value,
              stages: ramping ? profile.stages : [{ ...profile.stages[0], target: value }],
            })}
          />
        </Field>
        <Field label="Graceful stop ms">
          <NumberInput
            value={profile.gracefulStopMs}
            min={1}
            max={300_000}
            onChange={(value) => commit({ ...profile, gracefulStopMs: value })}
          />
        </Field>
      </div>
      {arrival ? (
        <div className="field-grid">
          <Field label="Pre-allocated VUs">
            <NumberInput
              value={profile.preAllocatedVUs}
              min={1}
              max={100_000}
              onChange={(value) => commit({ ...profile, preAllocatedVUs: value })}
            />
          </Field>
          <Field label="Max VUs">
            <NumberInput
              value={profile.maxVUs}
              min={1}
              max={100_000}
              onChange={(value) => commit({ ...profile, maxVUs: value })}
            />
          </Field>
        </div>
      ) : null}
      <div className="load-stage-heading">
        <span>{ramping ? "Stages" : "Duration"}</span>
        {ramping && profile.stages.length < 64 ? (
          <button
            type="button"
            className="secondary load-stage-add"
            onClick={() => commit({
              ...profile,
              stages: profile.stages.concat({
                durationMs: 1_000,
                target: profile.stages[profile.stages.length - 1]?.target ?? profile.startTarget,
              }),
            })}
          >
            <Plus size={14} /> Add stage
          </button>
        ) : null}
      </div>
      <div className="load-stage-list">
        {profile.stages.map((stage, index) => (
          <div className={`load-stage-row${ramping ? "" : " load-stage-row-single"}`} key={index}>
            <Field label={ramping ? `Stage ${index + 1} ms` : "Duration ms"}>
              <NumberInput
                value={stage.durationMs}
                min={1}
                max={3_600_000}
                onChange={(value) => updateStage(index, { durationMs: value })}
              />
            </Field>
            {ramping ? (
              <Field label={targetLabel}>
                <NumberInput
                  value={stage.target}
                  min={0}
                  max={targetMax}
                  onChange={(value) => updateStage(index, { target: value })}
                />
              </Field>
            ) : null}
            {ramping && profile.stages.length > 1 ? (
              <button
                type="button"
                className="secondary icon-button load-stage-delete"
                aria-label={`Delete stage ${index + 1}`}
                title={`Delete stage ${index + 1}`}
                onClick={() => commit({
                  ...profile,
                  stages: profile.stages.filter((_, current) => current !== index),
                })}
              >
                <Trash2 size={14} />
              </button>
            ) : null}
          </div>
        ))}
      </div>
      <div className={`load-profile-preview${preview.error ? " load-profile-preview-error" : ""}`}>
        <strong>Preview</strong>
        <span>{preview.text}</span>
      </div>
    </div>
  );
});

function engineProfile(data: FlowNodeData): LoadProfile {
  return data.loadProfile ?? legacyLoadProfile({
    virtualUsers: numberValue(data.virtualUsers, 128),
    durationMs: numberValue(data.durationMs, 10_000),
    rampUpMs: numberValue(data.rampUpMs, 1_000),
    requestTimeoutMs: numberValue(data.requestTimeoutMs, 1_000),
  });
}

function profilePreview(profile: LoadProfile) {
  try {
    const summary = loadProfileSummary(profile);
    const points = [
      `${summary.profile.startTarget} ${summary.targetUnit}`,
      ...summary.profile.stages.map((stage) => `${stage.target} after ${formatDuration(stage.durationMs)}`),
    ];
    const capacity = isArrivalMode(summary.profile.mode)
      ? ` · ${summary.profile.preAllocatedVUs}-${summary.profile.maxVUs} VUs`
      : "";
    return {
      error: false,
      text: `${points.join(" → ")} · total ${formatDuration(summary.durationMs)}${capacity}`,
    };
  } catch (error) {
    return {
      error: true,
      text: error instanceof Error ? error.message : "Invalid load profile",
    };
  }
}

const SecretBindingFields = memo(function SecretBindingFields({
  node,
  authSecret,
  updateAuthSecret,
}: {
  node: Node<FlowNodeData>;
  authSecret: RuntimeAuthSecret;
  updateAuthSecret: (nodeId: string, patch: Partial<RuntimeAuthSecret>) => void;
}) {
  const headerValues = headerRowsFromNode(node.data).flatMap((header) => [header.name, header.value]);
  const names = collectSecretPlaceholderNames([
    node.data.url,
    node.data.headersText,
    node.data.body,
    ...headerValues,
  ]);
  if (names.length === 0) {
    return null;
  }
  const bindings = authSecret.bindings ?? {};
  const updateBinding = (name: string, value: string) => {
    const next = { ...bindings };
    if (value) {
      next[name] = value;
    } else {
      delete next[name];
    }
    updateAuthSecret(node.id, { bindings: next });
  };
  return (
    <div className="field">
      <span className="field-label">Runtime secrets</span>
      {names.map((name) => (
        <label key={name} className="field-stack">
          <span>{name.replace(/^SECRET_/, "")}</span>
          <input
            type="password"
            value={bindings[name] ?? ""}
            placeholder={name}
            autoComplete="new-password"
            onChange={(event) => updateBinding(name, event.target.value)}
          />
        </label>
      ))}
      <div className="inspector-note">
        Placeholder values stay in memory only and are required before running.
      </div>
    </div>
  );
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
