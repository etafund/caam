export interface AgentDiagnostics {
  schemaVersion: 1;
  generatedAt: string;
  runtime: {
    nodeEnv: string;
    nextRuntime: string;
    ci: boolean;
    production: boolean;
  };
  routes: Array<{
    path: string;
    kind: "page" | "diagnostic";
    purpose: string;
  }>;
  devtools: {
    mcp: {
      status: "available-in-dev" | "disabled";
      guard: "not-found-in-production";
      note: string;
    };
  };
  safety: {
    secretsExposed: false;
    envValuesRedacted: true;
    writableActions: false;
  };
}

type EnvLike = Partial<Record<string, string | undefined>>;

const disabledValues = new Set(["0", "false", "off", "disabled"]);

export function isAgentDiagnosticsEnabled(env: EnvLike = process.env) {
  if (env.NODE_ENV === "production") {
    return false;
  }

  const flag = env.NEXT_PUBLIC_CAAM_AGENT_DIAGNOSTICS;
  if (flag && disabledValues.has(flag.trim().toLowerCase())) {
    return false;
  }

  return true;
}

export function collectAgentDiagnostics(env: EnvLike = process.env): AgentDiagnostics {
  const enabled = isAgentDiagnosticsEnabled(env);
  const nodeEnv = env.NODE_ENV ?? "development";

  return {
    schemaVersion: 1,
    generatedAt: new Date().toISOString(),
    runtime: {
      nodeEnv,
      nextRuntime: env.NEXT_RUNTIME ?? "nodejs",
      ci: env.CI === "true",
      production: nodeEnv === "production",
    },
    routes: [
      {
        path: "/",
        kind: "page",
        purpose: "Main CAAM dashboard shell and current static UI state.",
      },
      {
        path: "/debug",
        kind: "diagnostic",
        purpose: "Development-only agent diagnostics for DevTools and MCP-assisted inspection.",
      },
    ],
    devtools: {
      mcp: {
        status: enabled ? "available-in-dev" : "disabled",
        guard: "not-found-in-production",
        note: "Attach Next.js DevTools MCP only while running the dashboard with next dev. This page never includes secrets or writable actions.",
      },
    },
    safety: {
      secretsExposed: false,
      envValuesRedacted: true,
      writableActions: false,
    },
  };
}

