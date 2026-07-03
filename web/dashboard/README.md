# CAAM Web Dashboard

Next.js dashboard for the CAAM web control plane.

## Development

```bash
pnpm dev
```

Open [http://localhost:3000](http://localhost:3000) with your browser.

## Validation

```bash
pnpm lint
pnpm typecheck
pnpm test
```

## Dependency Security

The dashboard has a focused dependency security policy in [SECURITY-UPDATES.md](./SECURITY-UPDATES.md).

Use the combined dependency gate before merging dashboard dependency changes:

```bash
pnpm run security:check
```

Generate the local dependency health report with:

```bash
pnpm run deps:health
```
