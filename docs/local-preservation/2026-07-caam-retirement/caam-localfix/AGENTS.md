# AGENTS.md — caam-localfix workspace

## Scope and purpose

`/data/projects/caam-localfix/` is a **temporary operational hotfix workspace** used to
keep a non-released `caam` fix (`0bdd715` for `caam#19`) active while upstream
release binaries lag behind.

It is not intended to replace `/data/projects/caam`; it exists to preserve:

- a local build that includes the fix,
- a reconciler process that prevents unintended downgrade by release automation,
- and recovery/runbook artifacts required during the transition.

## Non-removal policy

Do not remove this directory unless the migration criteria below are satisfied.
This repository may contain critical runtime protections; deleting it early can
reintroduce known fixed behavior.

### Do not delete these paths without explicit authorization

- The whole workspace `/data/projects/caam-localfix/`
- `caam-src/` and `caam-src/.git/`
- `caam-mainstream-reconciler.sh`
- `reconciler.log`
- `MARKER.json`
- `PLAN.md` and `RUNBOOK.md`

## What to preserve

- `caam-src/caam` (fixed local binary)
- `caam.installed-backup...` files (historical rollback)
- reconciliation artifacts and patch files
- operational notes in `PLAN.md`, `RUNBOOK.md`, and `MARKER.json`

## Allowed work

- Read and update documentation or scripts in this directory to maintain the local-fix process.
- Run the reconciler/diagnostic tooling as documented if needed.
- Apply targeted fixes for this environment only when requested, then log intent and rationale.

## Safe retirement checklist (minimum conditions)

1. Confirm a published `caam` release newer than `v0.1.11` that includes commit
   `0bdd715` (or equivalent fix content).
2. Confirm automatic update paths will no longer reinstall a no-fix release.
3. Confirm `caam-localfix` reconciler is no longer required and can be removed.
4. Ensure `/data/projects/caam` has fully adopted any needed operational docs.
5. Remove/retire this workspace only with explicit human approval, then archive any
   needed logs or binaries you want to keep.

## Operational rule for LLM agents

- Treat `caam-localfix` as a live safety layer, not a disposable scratch folder.
- If uncertain about deleting anything here, stop and ask for explicit permission first.
