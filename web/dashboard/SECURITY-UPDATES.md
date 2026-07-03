# Web Dashboard Dependency Security

This policy applies only to `web/dashboard`.

## Required Gate

Run this before merging dashboard dependency changes:

```bash
pnpm run security:check
```

The gate runs:

- `pnpm audit --audit-level critical`
- `node scripts/dependency-health.mjs --check`

## Supported Version Windows

The dashboard must stay on these supported direct dependency lines:

| Package | Minimum | Supported major | Why |
| --- | --- | --- | --- |
| `next` | `16.1.4` | `16` | App Router and RSC server surface |
| `react` | `19.2.3` | `19` | RSC/client rendering fixes |
| `react-dom` | `19.2.3` | `19` | Must match React for RSC fixes |
| `tailwindcss` | `4.1.18` | `4` | Dashboard styling toolchain |
| `@tailwindcss/postcss` | `4.1.18` | `4` | Tailwind 4 PostCSS adapter |
| `vitest` | `3.2.6` | `3` | Patches GHSA-5xrq-8626-4rwp in the optional UI server |

Update `scripts/dependency-health.mjs` in the same commit whenever the minimum supported version changes.

## Patch Cadence

Review patch releases weekly. Patch-level upgrades for Next, React, React DOM, Tailwind, ESLint, TypeScript, Vitest, and Playwright are expected to be routine when `lint`, `typecheck`, and tests pass.

Avoid broad major upgrades unless the dashboard work item explicitly calls for it. Major upgrades need a separate compatibility pass.

## Critical Advisory Response

For critical or actively exploited advisories, especially React Server Components or Next.js server-side rendering issues:

1. Open a hotfix branch scoped to `web/dashboard`.
2. Upgrade only the affected package set and lockfile entries.
3. Run `pnpm run security:check`, `pnpm lint`, `pnpm typecheck`, and `pnpm test`.
4. Release as soon as those checks pass.
5. Backfill a normal dependency review issue if non-critical follow-up remains.

## Dependency Health Report

Generate a local report with:

```bash
pnpm run deps:health
```

Use JSON output for automation:

```bash
node scripts/dependency-health.mjs --json
```
