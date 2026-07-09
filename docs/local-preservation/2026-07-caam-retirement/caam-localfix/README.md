# caam-localfix

This directory is a **managed local-fix workspace** for `caam` while an important
upstream fix has not yet shipped in a released build.

## Why this exists

`/data/projects/caam` is the main workspace. `caam-localfix` is kept separate to
preserve an unreleased hotfix strategy:

- A fix for a known issue (`caam#19`) landed in upstream main at commit `0bdd715`.
- The latest published release in use at the time this was created was
  `v0.1.11`, which does not include that fix.
- A reconciler in this folder reapplies/maintains the fixed build if update tooling
  or release automation would otherwise downgrade `caam` back to the release
  without the fix.

## What is stored here

- `caam-src/` – source checkout used for the local-fix environment
- `caam-src/caam` – built binary for the fixed version
- `caam-mainstream-reconciler.sh` – script that runs reconciliation checks
- `caam.installed-backup...` – backup of previous installed binary
- `PLAN.md`, `RUNBOOK.md` – operational notes and procedures
- `reconciler.log` – reconciliation history
- `*.patch` files – patch artifacts for this environment

## Why it should not be removed yet

Removing this directory now would lose the hotfix mechanism and build artifacts,
and could re-expose the system to the pre-fix behavior until a proper release
is available.

## When it is safe to remove

This workspace is intended to be temporary. Retire it only when **all** of the
following are true:

1. A release newer than `v0.1.11` is available and confirmed to include the
   `0bdd715` fix.
2. ACFS/autoupdate behavior will no longer downgrade to the no-fix build.
3. The reconciler is no longer needed and is disabled/retired.
4. The release notes and this repository’s operational docs are updated to the
   same effect.

Only then should `/data/projects/caam` be the sole authoritative workspace.

## Related files

- `MARKER.json` explains the justification for this directory and its retention
  policy.
- `caam-mainstream-reconciler.sh` is the entry point for the daily reconciliation
  job (logged in `reconciler.log`).
