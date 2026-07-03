import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const dashboardRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);

describe("dependency health report", () => {
  it("lists the dashboard security policy and current critical package checks", () => {
    const output = execFileSync(
      process.execPath,
      [path.join(dashboardRoot, "scripts/dependency-health.mjs"), "--json"],
      { cwd: dashboardRoot, encoding: "utf8" },
    );

    const report = JSON.parse(output) as {
      status: string;
      policy: { packages: Record<string, { minimum: string }> };
      commands: { fullSecurityCheck: string };
      checks: Array<{ name: string; status: string }>;
    };

    expect(report.status).toBe("ok");
    expect(report.policy.packages.next.minimum).toBe("16.1.4");
    expect(report.policy.packages.react.minimum).toBe("19.2.3");
    expect(report.policy.packages["react-dom"].minimum).toBe("19.2.3");
    expect(report.policy.packages.vitest.minimum).toBe("3.2.6");
    expect(report.commands.fullSecurityCheck).toBe("pnpm run security:check");
    expect(report.checks).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ name: "next", status: "ok" }),
        expect.objectContaining({ name: "react", status: "ok" }),
        expect.objectContaining({ name: "react-dom", status: "ok" }),
        expect.objectContaining({ name: "vitest", status: "ok" }),
        expect.objectContaining({ name: "pnpm-lock.yaml", status: "ok" }),
      ]),
    );
  });
});
