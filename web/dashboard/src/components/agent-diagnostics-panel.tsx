import type { AgentDiagnostics } from "@/lib/agent-diagnostics";

interface AgentDiagnosticsPanelProps {
  diagnostics: AgentDiagnostics;
}

export function AgentDiagnosticsPanel({
  diagnostics,
}: AgentDiagnosticsPanelProps) {
  const json = JSON.stringify(diagnostics, null, 2);

  return (
    <main className="min-h-screen bg-background p-6 text-foreground">
      <div className="mx-auto max-w-5xl space-y-6">
        <header className="space-y-2">
          <p className="text-sm font-medium uppercase tracking-wide text-accent">
            Agent diagnostics
          </p>
          <h1 className="text-2xl font-semibold">Debug surface</h1>
          <p className="max-w-3xl text-sm text-muted">
            Development-only, read-only app state for AI agents and DevTools MCP
            sessions. Secrets and raw environment values are intentionally
            omitted.
          </p>
        </header>

        <section
          aria-label="Runtime summary"
          className="grid gap-4 md:grid-cols-3"
        >
          <SummaryTile label="Environment" value={diagnostics.runtime.nodeEnv} />
          <SummaryTile
            label="Next runtime"
            value={diagnostics.runtime.nextRuntime}
          />
          <SummaryTile
            label="DevTools MCP"
            value={diagnostics.devtools.mcp.status}
          />
        </section>

        <section
          aria-label="Safety guarantees"
          className="rounded-xl border border-border bg-surface p-5"
        >
          <h2 className="font-semibold">Safety guarantees</h2>
          <dl className="mt-4 grid gap-3 text-sm sm:grid-cols-3">
            <Flag
              label="Secrets exposed"
              value={diagnostics.safety.secretsExposed ? "yes" : "no"}
            />
            <Flag
              label="Raw env values"
              value={diagnostics.safety.envValuesRedacted ? "redacted" : "visible"}
            />
            <Flag
              label="Writable actions"
              value={diagnostics.safety.writableActions ? "enabled" : "disabled"}
            />
          </dl>
        </section>

        <section
          aria-label="Known routes"
          className="rounded-xl border border-border bg-surface p-5"
        >
          <h2 className="font-semibold">Known routes</h2>
          <div className="mt-4 divide-y divide-border">
            {diagnostics.routes.map((route) => (
              <div
                className="grid gap-2 py-3 text-sm sm:grid-cols-[8rem_8rem_1fr]"
                key={route.path}
              >
                <code className="font-mono text-accent">{route.path}</code>
                <span className="text-muted">{route.kind}</span>
                <span>{route.purpose}</span>
              </div>
            ))}
          </div>
        </section>

        <section
          aria-label="Machine-readable snapshot"
          className="rounded-xl border border-border bg-surface p-5"
        >
          <h2 className="font-semibold">Machine-readable snapshot</h2>
          <p className="mt-1 text-sm text-muted">
            Agents can read this JSON from the page or query the embedded script
            tag with id <code>caam-agent-diagnostics</code>.
          </p>
          <pre className="mt-4 max-h-[32rem] overflow-auto rounded-lg bg-background p-4 text-xs">
            {json}
          </pre>
          <script
            id="caam-agent-diagnostics"
            type="application/json"
            dangerouslySetInnerHTML={{ __html: json }}
          />
        </section>
      </div>
    </main>
  );
}

function SummaryTile({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-border bg-surface p-5">
      <dt className="text-sm text-muted">{label}</dt>
      <dd className="mt-2 font-mono text-sm">{value}</dd>
    </div>
  );
}

function Flag({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-muted">{label}</dt>
      <dd className="mt-1 font-medium">{value}</dd>
    </div>
  );
}

