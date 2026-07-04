#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const rootDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const packagePath = path.join(rootDir, "package.json");
const lockfilePath = path.join(rootDir, "pnpm-lock.yaml");
const workspacePath = path.join(rootDir, "pnpm-workspace.yaml");

export const dependencyPolicy = {
  cadence:
    "Review dashboard dependency patches weekly; land Next/React/Tailwind security patches as soon as validation passes.",
  criticalResponse:
    "For actively exploited or critical React/Next RSC advisories, open a hotfix branch, run security:check plus lint/typecheck/tests, release immediately, then backfill the normal update review.",
  packages: {
    next: {
      section: "dependencies",
      minimum: "16.2.10",
      allowedMajors: [16],
      reason: "Next.js carries the App Router/RSC server surface and must stay on the current supported 16.x line.",
    },
    react: {
      section: "dependencies",
      minimum: "19.2.7",
      allowedMajors: [19],
      reason: "React must stay paired with react-dom for RSC/client rendering security fixes.",
    },
    "react-dom": {
      section: "dependencies",
      minimum: "19.2.7",
      allowedMajors: [19],
      reason: "react-dom must match React and receive the same RSC-related security fixes.",
    },
    vite: {
      section: "devDependencies",
      minimum: "8.1.3",
      allowedMajors: [8],
      reason: "Vite is used by the dashboard test harness and must stay past patched dev-server releases.",
    },
    "@vitejs/plugin-react": {
      section: "devDependencies",
      minimum: "6.0.3",
      allowedMajors: [6],
      reason: "The Vite React plugin must track the Vite 8 toolchain used by Vitest.",
    },
    tailwindcss: {
      section: "devDependencies",
      minimum: "4.3.2",
      allowedMajors: [4],
      reason: "Tailwind 4.x is the supported styling toolchain for the dashboard.",
    },
    "@tailwindcss/postcss": {
      section: "devDependencies",
      minimum: "4.3.2",
      allowedMajors: [4],
      reason: "The PostCSS adapter must track the Tailwind 4.x runtime.",
    },
    eslint: {
      section: "devDependencies",
      minimum: "9.39.2",
      allowedMajors: [9],
      reason: "ESLint stays on the latest 9.x line supported by Next's lint plugin ecosystem.",
    },
    "eslint-config-next": {
      section: "devDependencies",
      minimum: "16.2.10",
      allowedMajors: [16],
      reason: "Next's ESLint config must match the Next runtime patch line.",
    },
    jsdom: {
      section: "devDependencies",
      minimum: "29.1.1",
      allowedMajors: [29],
      reason: "jsdom carries the browser-like unit-test runtime and transitive WebSocket surface.",
    },
    vitest: {
      section: "devDependencies",
      minimum: "4.1.9",
      allowedMajors: [4],
      reason: "Vitest must stay on the current Vite-compatible test runner line.",
    },
  },
};

function readPackageJson() {
  try {
    return JSON.parse(fs.readFileSync(packagePath, "utf8"));
  } catch (error) {
    const reason = error instanceof Error ? error.message : String(error);
    throw new Error(`Failed to parse ${packagePath}: ${reason}`);
  }
}

function parseVersion(spec) {
  if (typeof spec !== "string") {
    return null;
  }
  const trimmed = spec.trim();
  if (trimmed === "" || /[*xX]/.test(trimmed)) {
    return null;
  }
  const match = trimmed.match(/^(?:npm:)?(?:[~^<>= ]*v?)(\d+)\.(\d+)\.(\d+)(?:[-+][0-9A-Za-z.-]+)?$/);
  if (!match) {
    return null;
  }
  return match.slice(1, 4).map(Number);
}

function compareVersions(left, right) {
  for (let i = 0; i < 3; i += 1) {
    if (left[i] !== right[i]) {
      return left[i] > right[i] ? 1 : -1;
    }
  }
  return 0;
}

function packageCheck(pkg, name, rule) {
  const section = pkg[rule.section] ?? {};
  const spec = section[name];
  if (!spec) {
    return {
      name,
      status: "fail",
      current: null,
      minimum: rule.minimum,
      details: `Missing ${rule.section}.${name}`,
    };
  }

  const current = parseVersion(spec);
  const minimum = parseVersion(rule.minimum);
  if (!current || !minimum) {
    return {
      name,
      status: "fail",
      current: spec,
      minimum: rule.minimum,
      details: `Version spec ${JSON.stringify(spec)} is not a concrete semver range this policy can evaluate`,
    };
  }
  if (!rule.allowedMajors.includes(current[0])) {
    return {
      name,
      status: "fail",
      current: spec,
      minimum: rule.minimum,
      details: `Major ${current[0]} is outside supported majors: ${rule.allowedMajors.join(", ")}`,
    };
  }
  if (compareVersions(current, minimum) < 0) {
    return {
      name,
      status: "fail",
      current: spec,
      minimum: rule.minimum,
      details: `Version ${spec} is below required minimum ${rule.minimum}`,
    };
  }
  return {
    name,
    status: "ok",
    current: spec,
    minimum: rule.minimum,
    details: rule.reason,
  };
}

function fileCheck(name, filePath, details) {
  return {
    name,
    status: fs.existsSync(filePath) ? "ok" : "fail",
    details,
  };
}

function scriptCheck(pkg, name, expected, details) {
  const current = pkg.scripts?.[name];
  return {
    name: `script:${name}`,
    status: current === expected ? "ok" : "fail",
    current,
    minimum: expected,
    details,
  };
}

export function generateDependencyHealthReport() {
  const pkg = readPackageJson();
  const packageChecks = Object.entries(dependencyPolicy.packages).map(([name, rule]) =>
    packageCheck(pkg, name, rule),
  );
  const checks = [
    ...packageChecks,
    scriptCheck(
      pkg,
      "deps:audit",
      "pnpm audit --audit-level high",
      "Security audit gate must fail on high or critical advisories.",
    ),
    fileCheck("pnpm-lock.yaml", lockfilePath, "Lockfile must be committed so audit results are reproducible."),
    fileCheck("pnpm-workspace.yaml", workspacePath, "Workspace file keeps pnpm commands scoped to the dashboard package."),
  ];

  return {
    status: checks.every((check) => check.status === "ok") ? "ok" : "fail",
    generatedAt: new Date().toISOString(),
    packageManager: pkg.packageManager,
    engines: pkg.engines ?? {},
    policy: dependencyPolicy,
    commands: {
      auditHigh: "pnpm run deps:audit",
      healthCheck: "pnpm run deps:health:check",
      fullSecurityCheck: "pnpm run security:check",
    },
    checks,
  };
}

function renderMarkdown(report) {
  const rows = report.checks
    .map(
      (check) =>
        `| ${check.status === "ok" ? "OK" : "FAIL"} | ${check.name} | ${check.current ?? ""} | ${
          check.minimum ?? ""
        } | ${check.details} |`,
    )
    .join("\n");

  return [
    "# Dashboard Dependency Health",
    "",
    `Status: ${report.status}`,
    `Generated: ${report.generatedAt}`,
    `Package manager: ${report.packageManager}`,
    "",
    "## Policy",
    "",
    `- Cadence: ${report.policy.cadence}`,
    `- Critical response: ${report.policy.criticalResponse}`,
    "",
    "## Checks",
    "",
    "| Status | Check | Current | Minimum | Details |",
    "| --- | --- | --- | --- | --- |",
    rows,
    "",
    "## Commands",
    "",
    `- High audit: \`${report.commands.auditHigh}\``,
    `- Version policy: \`${report.commands.healthCheck}\``,
    `- Combined gate: \`${report.commands.fullSecurityCheck}\``,
    "",
  ].join("\n");
}

const args = new Set(process.argv.slice(2));
const report = generateDependencyHealthReport();

if (args.has("--json")) {
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
} else {
  process.stdout.write(renderMarkdown(report));
}

if (args.has("--check") && report.status !== "ok") {
  process.exitCode = 1;
}
