import { memo } from "react";
import { Plus, Trash2 } from "lucide-react";

import {
  environmentSecretNames,
  formatEnvironmentVariables,
  normalizeSecretName,
  parseEnvironmentVariables,
} from "../environmentProfiles";
import type { EnvironmentProfile } from "../flowTypes";

export const EnvironmentPanel = memo(function EnvironmentPanel({
  profiles,
  activeProfile,
  secretBindings,
  disabled,
  onAdd,
  onDelete,
  onSelect,
  onUpdate,
  onUpdateSecret,
}: {
  profiles: EnvironmentProfile[];
  activeProfile: EnvironmentProfile | null;
  secretBindings: Record<string, string>;
  disabled: boolean;
  onAdd: () => void;
  onDelete: (id: string) => void;
  onSelect: (id: string | null) => void;
  onUpdate: (profile: EnvironmentProfile) => void;
  onUpdateSecret: (name: string, value: string) => void;
}) {
  const secretNames = environmentSecretNames(activeProfile);
  return (
    <section className="environment-panel" aria-label="Environment profile">
      <div className="environment-heading">
        <div>
          <div className="eyebrow">Environment</div>
          <h2>{activeProfile?.name || "No profile"}</h2>
        </div>
        <div className="environment-heading-actions">
          <button
            type="button"
            className="secondary icon-button"
            aria-label="Add environment"
            title="Add environment"
            disabled={disabled || profiles.length >= 24}
            onClick={onAdd}
          >
            <Plus size={16} />
          </button>
          <button
            type="button"
            className="secondary icon-button"
            aria-label="Delete environment"
            title="Delete environment"
            disabled={disabled || !activeProfile}
            onClick={() => activeProfile && onDelete(activeProfile.id)}
          >
            <Trash2 size={16} />
          </button>
        </div>
      </div>

      <label>
        Profile
        <select
          value={activeProfile?.id ?? ""}
          disabled={disabled}
          onChange={(event) => onSelect(event.target.value || null)}
        >
          <option value="">No environment</option>
          {profiles.map((profile) => (
            <option key={profile.id} value={profile.id}>{profile.name}</option>
          ))}
        </select>
      </label>

      {activeProfile ? (
        <>
          <label>
            Name
            <input
              value={activeProfile.name}
              disabled={disabled}
              onChange={(event) => onUpdate({ ...activeProfile, name: event.target.value })}
            />
          </label>
          <label>
            Base URL
            <input
              type="url"
              value={activeProfile.baseUrl}
              placeholder="https://api.example.com"
              disabled={disabled}
              onChange={(event) => onUpdate({ ...activeProfile, baseUrl: event.target.value })}
            />
            <small>Use {"{{BASE_URL}}"} in request URLs.</small>
          </label>
          <label>
            Variables
            <textarea
              rows={3}
              value={formatEnvironmentVariables(activeProfile.variables)}
              placeholder={"REGION=ap-northeast-2\nTENANT=demo"}
              spellCheck={false}
              disabled={disabled}
              onChange={(event) => onUpdate({
                ...activeProfile,
                variables: parseEnvironmentVariables(event.target.value),
              })}
            />
            <small>One uppercase NAME=value per line. Sensitive names must be runtime secrets.</small>
          </label>
          <label>
            Runtime secret names
            <textarea
              rows={2}
              value={activeProfile.secretNames.join("\n")}
              placeholder={"API_TOKEN\nCLIENT_SECRET"}
              spellCheck={false}
              disabled={disabled}
              onChange={(event) => onUpdate({
                ...activeProfile,
                secretNames: event.target.value.split("\n"),
              })}
            />
          </label>
          {secretNames.map((name) => (
            <label key={name} className="field-stack">
              <span>{name}</span>
              <input
                type="password"
                value={secretBindings[name] ?? ""}
                placeholder={`{{${name}}}`}
                autoComplete="new-password"
                disabled={disabled}
                onChange={(event) => onUpdateSecret(normalizeSecretName(name), event.target.value)}
              />
            </label>
          ))}
          <div className="inspector-note">
            Profile values are stored locally. Secret names are saved as references; masked values stay in memory only.
          </div>
        </>
      ) : (
        <div className="inspector-note">
          Add a named profile to switch base URLs and variables without editing scenario nodes.
        </div>
      )}
    </section>
  );
});
