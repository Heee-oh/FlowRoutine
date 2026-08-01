import { afterEach, describe, expect, it, vi } from "vitest";

import {
  environmentSecretNames,
  environmentVariableBindings,
  formatEnvironmentVariables,
  loadEnvironmentProfiles,
  normalizeSecretName,
  parseEnvironmentVariables,
  resolveEnvironmentPlaceholders,
  saveEnvironmentProfiles,
  validateEnvironmentProfile,
} from "./environmentProfiles";
import type { EnvironmentProfile } from "./flowTypes";

describe("environment profiles", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("resolves named values while leaving capture and secret templates for their runtime paths", () => {
    const profile = createProfile();
    const bindings = environmentVariableBindings(profile, new Set(["captured"]));
    const resolved = resolveEnvironmentPlaceholders(
      "{{BASE_URL}}/{{REGION}}/{{captured}}/{{SECRET_API_TOKEN}}",
      bindings,
      new Set(["captured"]),
    );

    expect(resolved).toBe(
      "https://staging.example.com/ap-northeast-2/{{captured}}/{{SECRET_API_TOKEN}}",
    );
    expect(() => resolveEnvironmentPlaceholders("{{TENANT}}", bindings, new Set())).toThrow(
      "Environment variable TENANT is required",
    );
  });

  it("rejects duplicate, capture-conflicting, and sensitive non-secret variables", () => {
    expect(() => validateEnvironmentProfile({
      ...createProfile(),
      variables: [{ name: "TOKEN", value: "literal" }],
    })).toThrow("must be declared as a runtime secret");
    expect(() => validateEnvironmentProfile({
      ...createProfile(),
      variables: [{ name: "CALLBACK", value: "https://example.com?access_token=literal" }],
    })).toThrow("contains a secret");
    expect(() => validateEnvironmentProfile({
      ...createProfile(),
      variables: [{ name: "REGION", value: "one" }, { name: "region", value: "two" }],
    })).toThrow("is duplicated");
    expect(() => environmentVariableBindings(createProfile(), new Set(["REGION"]))).toThrow(
      "conflicts with a capture name",
    );
  });

  it("persists secret references without secret values", () => {
    let persisted = "";
    vi.stubGlobal("window", {
      localStorage: {
        getItem: () => persisted || null,
        setItem: (_key: string, value: string) => {
          persisted = value;
        },
        removeItem: vi.fn(),
      },
    });
    saveEnvironmentProfiles([{
      ...createProfile(),
      baseUrl: "https://example.com?access_token=base-secret",
      variables: [
        { name: "REGION", value: "ap-northeast-2" },
        { name: "API_TOKEN", value: "profile-secret" },
      ],
    }]);

    expect(persisted).not.toMatch(/base-secret|profile-secret/);
    expect(persisted).toContain("SECRET_ACCESS_TOKEN");
    expect(persisted).toContain("SECRET_API_TOKEN");
    expect(loadEnvironmentProfiles()[0].variables).toEqual([
      { name: "REGION", value: "ap-northeast-2" },
    ]);
  });

  it("normalizes secret aliases and variable text", () => {
    expect(normalizeSecretName("api-token")).toBe("SECRET_API_TOKEN");
    expect(environmentSecretNames({
      ...createProfile(),
      secretNames: ["api-token"],
    })).toEqual(["SECRET_API_TOKEN"]);
    const variables = parseEnvironmentVariables("REGION=ap-northeast-2\nTENANT=demo=value");
    expect(formatEnvironmentVariables(variables)).toBe("REGION=ap-northeast-2\nTENANT=demo=value");
    expect(validateEnvironmentProfile({
      ...createProfile(),
      baseUrl: "https://example.com?access_token={{SECRET_API_TOKEN}}",
    }).baseUrl).toContain("{{SECRET_API_TOKEN}}");
  });
});

function createProfile(): EnvironmentProfile {
  return {
    id: "staging",
    name: "Staging",
    baseUrl: "https://staging.example.com",
    variables: [{ name: "REGION", value: "ap-northeast-2" }],
    secretNames: ["SECRET_API_TOKEN"],
  };
}
