import { notFound } from "next/navigation";
import { AgentDiagnosticsPanel } from "@/components/agent-diagnostics-panel";
import {
  collectAgentDiagnostics,
  isAgentDiagnosticsEnabled,
} from "@/lib/agent-diagnostics";

export const metadata = {
  title: "Agent Diagnostics",
};

export default function DebugPage() {
  if (!isAgentDiagnosticsEnabled()) {
    notFound();
  }

  return <AgentDiagnosticsPanel diagnostics={collectAgentDiagnostics()} />;
}

