import { describe, expect, it } from "vitest";
import {
  collectAgentDiagnostics,
  isAgentDiagnosticsEnabled,
} from "./agent-diagnostics";

describe("agent diagnostics", () => {
  it("is enabled for development and test by default", () => {
    expect(isAgentDiagnosticsEnabled({ NODE_ENV: "development" })).toBe(true);
    expect(isAgentDiagnosticsEnabled({ NODE_ENV: "test" })).toBe(true);
  });

  it("is disabled in production and when explicitly turned off", () => {
    expect(isAgentDiagnosticsEnabled({ NODE_ENV: "production" })).toBe(false);
    expect(
      isAgentDiagnosticsEnabled({
        NODE_ENV: "development",
        NEXT_PUBLIC_CAAM_AGENT_DIAGNOSTICS: "false",
      }),
    ).toBe(false);
  });

  it("redacts environment values and exposes only read-only diagnostics", () => {
    const diagnostics = collectAgentDiagnostics({
      NODE_ENV: "development",
      NEXT_RUNTIME: "nodejs",
      CI: "true",
      CAAM_TOKEN: "secret-token",
      OPENAI_API_KEY: "secret-key",
    });
    const body = JSON.stringify(diagnostics);

    expect(diagnostics.safety).toEqual({
      secretsExposed: false,
      envValuesRedacted: true,
      writableActions: false,
    });
    expect(diagnostics.runtime).toEqual({
      nodeEnv: "development",
      nextRuntime: "nodejs",
      ci: true,
      production: false,
    });
    expect(body).not.toContain("secret-token");
    expect(body).not.toContain("secret-key");
    expect(diagnostics.routes).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ path: "/" }),
        expect.objectContaining({ path: "/debug" }),
      ]),
    );
  });
});

