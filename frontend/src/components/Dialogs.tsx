import { memo } from "react";
import type { HelpLanguage, HelpTopic } from "../help";
import { helpDialogTitle, nodeHelpItems, overviewHelpItems } from "../help";
import { nodePalette } from "../flowModel";
import type { SafetyAssessment } from "../flowTypes";
import type { StartRequest } from "../types";
import { formatNumber } from "../format";

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
        </div>
        <div className="modal-actions">
          <button type="button" className="secondary" onClick={onCancel}>Cancel</button>
          <button type="button" className="danger" onClick={onConfirm} disabled={safety.target.tone === "invalid"}>Start test</button>
        </div>
      </div>
    </div>
  );
});
