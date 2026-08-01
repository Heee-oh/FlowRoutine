import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { EnvironmentPanel } from "./EnvironmentPanel";

describe("EnvironmentPanel", () => {
  it("renders named variables and masked runtime secret bindings", () => {
    const profile = {
      id: "staging",
      name: "Staging",
      baseUrl: "https://staging.example.com",
      variables: [{ name: "REGION", value: "ap-northeast-2" }],
      secretNames: ["SECRET_API_TOKEN"],
    };
    const markup = renderToStaticMarkup(
      <EnvironmentPanel
        profiles={[profile]}
        activeProfile={profile}
        secretBindings={{}}
        disabled={false}
        onAdd={vi.fn()}
        onDelete={vi.fn()}
        onSelect={vi.fn()}
        onUpdate={vi.fn()}
        onUpdateSecret={vi.fn()}
      />,
    );

    expect(markup).toContain("Staging");
    expect(markup).toContain("REGION=ap-northeast-2");
    expect(markup).toContain("SECRET_API_TOKEN");
    expect(markup).toContain('type="password"');
    expect(markup).toContain("stay in memory only");
  });
});
