import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  AgentDiagnosticsPanel,
  escapeJsonForScript,
} from "@/components/agent-diagnostics-panel";
import type { AgentDiagnostics } from "@/lib/agent-diagnostics";

const diagnostics: AgentDiagnostics = {
  schemaVersion: 1,
  generatedAt: "2026-07-03T00:00:00.000Z",
  runtime: {
    nodeEnv: "development",
    nextRuntime: "nodejs",
    ci: false,
    production: false,
  },
  routes: [
    {
      path: "/debug",
      kind: "diagnostic",
      purpose:
        "Readable </script><script>alert(1)</script> JSON with ampersands & line separators \u2028\u2029.",
    },
  ],
  devtools: {
    mcp: {
      status: "available-in-dev",
      guard: "not-found-in-production",
      note: "No writable actions.",
    },
  },
  safety: {
    secretsExposed: false,
    envValuesRedacted: true,
    writableActions: false,
  },
};

describe("AgentDiagnosticsPanel", () => {
  it("keeps visible diagnostics readable while escaping the embedded JSON script", () => {
    render(<AgentDiagnosticsPanel diagnostics={diagnostics} />);

    const visibleJson = screen
      .getByText((content) => content.includes('"schemaVersion": 1'))
      .textContent;
    expect(visibleJson).toContain("</script><script>alert(1)</script>");
    expect(visibleJson).toContain("& line separators");

    const script = document.querySelector<HTMLScriptElement>(
      "#caam-agent-diagnostics",
    );
    expect(script).not.toBeNull();
    expect(script?.type).toBe("application/json");
    expect(script?.textContent).not.toContain("</script>");
    expect(script?.textContent).not.toContain("<script>");
    expect(script?.textContent).not.toContain("&");
    expect(JSON.parse(script?.textContent ?? "")).toEqual(diagnostics);
  });
});

describe("escapeJsonForScript", () => {
  it("escapes characters that can break out of script or HTML parsing contexts", () => {
    expect(escapeJsonForScript('{"value":"<>&\u2028\u2029"}')).toBe(
      '{"value":"\\u003c\\u003e\\u0026\\u2028\\u2029"}',
    );
  });
});
