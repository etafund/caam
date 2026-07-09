# CAAM Fork Deletion Evidence

Collected at `2026-07-09T00:56:43Z` from `/data/projects/caam` and `/data/projects`.

## Policy Context

The governing `/data/projects/AGENTS.md` forbids file deletion without express permission and treats irreversible commands such as `rm -rf` or GitHub repository deletion as requiring exact-command approval and confirmation. This evidence packet is only for deciding whether deletion would be technically justified.

## Local Primary Checkout

Directory: `/data/projects/caam`

`git status --short --branch` after fetching both remotes:

```text
## main...origin/main
 M web/dashboard/playwright-report/index.html
```

There is an uncommitted modification in `web/dashboard/playwright-report/index.html`.

`git remote -v`:

```text
origin   https://github.com/etafund/caam.git (fetch)
origin   https://github.com/etafund/caam.git (push)
upstream https://github.com/Dicklesworthstone/coding_agent_account_manager.git (fetch)
upstream https://github.com/Dicklesworthstone/coding_agent_account_manager.git (push)
```

GitHub repo metadata:

```text
Dicklesworthstone/coding_agent_account_manager:
  defaultBranchRef: main
  isFork: false
  viewerPermission: READ
  url: https://github.com/Dicklesworthstone/coding_agent_account_manager

etafund/caam:
  defaultBranchRef: main
  isFork: true
  parent: Dicklesworthstone/coding_agent_account_manager
  viewerPermission: ADMIN
  url: https://github.com/etafund/caam
```

Remote heads after fetch / ls-remote:

```text
upstream/main   = 98c05c7bf78438ef2c4b2829c47d8d024bed0872
upstream/master = 98c05c7bf78438ef2c4b2829c47d8d024bed0872
origin/main     = bd15600f47127b346947045c070e0d879140762c
origin/master   = bd15600f47127b346947045c070e0d879140762c
local main      = bd15600f47127b346947045c070e0d879140762c
merge-base(local main, upstream/main) = 304e037569802c887e3548ea8bdc8cdb76db8d22
```

Upstream commits not in local fork:

```text
98c05c7 fix(config): make `caam config set` reflection-driven so it can write every key `show`/`get` expose (#54)
1d9225c test(daemon): deflake TestDaemon_RunLoop_MultipleIterations via polling
65f1e17 fix(usage): treat Claude usage API utilization as percent, not fraction (#52); clear error for `login claude` (#53)
```

Fork commits reachable from `main` but not from `upstream/main`:

```text
bd15600 Merge upstream/main at 304e037
d281f35 Cherry-pick upstream robot and Codex fixes
eaa0b4a Standardize CLI JSON encoding
5d88282 Harden Claude shallow env guardrails
5006136 Polish shallow Codex config sanitization
581cef2 Cherry-pick upstream shallow Agent View guard
6be203c Cherry-pick upstream shallow daemon fixes
1794a23 Harden caam update integrity checks
5fdc1d5 Test self-update rollback path
a37b776 Fix self-update release selection
fb61d5b Propagate coordinator token to auth agent
acd8c7a Harden coordinator auth request lifecycle
0f709f4 Fix stale auth restore manifests
5594fd7 Harden shallow profiles and rate-limit detection
a85cf37 Polish coordinator context handling
f77c253 Wire dashboard to live API data
4f63deb fix: complete TUI sync management
f1d434c fix: complete audit-found shell and monitor gaps
c6aa57c fix: complete caam convergence hardening
fc0eed2 feat(monitor): fix false auth/usage errors and expand usage display
1839268 Merge remote-tracking branch 'upstream/main'
08ce054 fix(shallow): scope codex daemon reloads
debf294 feat(cmd): add shallow-spawn --reload-daemon for codex
92512c5 feat(shallow): register Antigravity (agy) provider layout
ca9f07c fix(monitor): stop live table staircase in raw mode; explain missing usage (Closes #37)
80250ad fix(exec): propagate child exit code and explain non-TTY failures (Closes #36)
55a56b6 harden(shallow): fail closed on unreadable metadata in --force/spawn/doctor (oracle r7 note)
ca6e8ce fix(shallow): searchable-dir preflight + Meta.RealHome binding invariant (oracle r6)
aa072aa fix(shallow): inner-root preflight + ValidateProfileShape real-HOME/foreign-root guards (oracle r5)
41d9d64 fix(shallow): real-HOME ancestor guard + cross-provider fail-closed auth + realHome preflight (oracle r4)
b229fdb fix(shallow): complete --force data-loss guards + real-HOME guard + SameFile containment (oracle r3)
0de2e26 feat+fix(shallow): doctor command + --force preflight hardening + honest isolation docs (oracle r2)
e09fb88 test(shallow): fuzz + golden + mock-free binary E2E hardening
1945540 fix(shallow): harden isolation + CLI ergonomics from review (oracle + 2 reviewers)
2fa0e48 feat(shallow): generalized multi-harness shallow profiles — Codex support + Layout registry + Phase 3.5 isolation hardening (#34)
57ccf47 docs: detailed implementation plan for generalized multi-harness shallow profiles (Codex + Layout registry, Phase 3.5 isolation hardening)
f4aebe6 chore: gitignore .ntm and scratch/; untrack committed .ntm logs
3c7ffe9 docs: add PLAN.md
```

`git cherry -v upstream/main main` reported all of the above fork-side commits with `+`, meaning Git did not find patch-equivalent commits upstream by patch-id.

Net diff from upstream to local fork is large. `git diff --stat upstream/main..main` reports:

```text
246 files changed, 36472 insertions(+), 6761 deletions(-)
```

The inverse comparison `git diff --stat main..upstream/main` reports:

```text
246 files changed, 6762 insertions(+), 36473 deletions(-)
```

The diff includes substantial changes in:

```text
README.md
cmd/caam/cmd/*.go
internal/authfile/authfile.go
internal/monitor/*
internal/shallow/*
internal/tui/*
internal/update/*
web/dashboard/*
PLAN.md
docs/*
```

It also includes local fork files absent upstream such as `PLAN.md`, `cmd/caam/cmd/prompt.go`, `cmd/caam/cmd/schema.go`, many test files, dashboard UI primitives, and multiple docs. Upstream has `.beads/*` and recent fixes absent from the fork.

## Secondary Local Directory: `/data/projects/caam-localfix`

This directory is not itself a git repo at the top level. It contains a nested checkout at `/data/projects/caam-localfix/caam-src`.

Top-level docs say it is a managed temporary local-fix workspace:

```text
This directory is a managed local-fix workspace for caam while an important
upstream fix has not yet shipped in a released build.
```

The README's "When it is safe to remove" criteria:

```text
Retire it only when all of the following are true:
1. A release newer than v0.1.11 is available and confirmed to include the 0bdd715 fix.
2. ACFS/autoupdate behavior will no longer downgrade to the no-fix build.
3. The reconciler is no longer needed and is disabled/retired.
4. The release notes and this repository's operational docs are updated to the same effect.
```

Nested checkout `/data/projects/caam-localfix/caam-src` status:

```text
## HEAD (no branch)
origin = https://github.com/Dicklesworthstone/coding_agent_account_manager.git
HEAD detached at origin/main = 304e037
local branch main = 0bdd715 [origin/main: ahead 3, behind 5]
origin/main in this nested checkout is stale at 304e037; top-level upstream currently has 98c05c7.
```

The localfix runbook says the reconciler existed because release `v0.1.11` lacked fix `0bdd715`, and that it should stay until a newer release including the fix is available and update behavior no longer downgrades.

## Initial Local Interpretation

The current local evidence does not prove that the fork has been fully upstreamed. It shows:

- `etafund/caam` is still divergent from upstream by dozens of non-patch-equivalent commits.
- The net diff is huge, not just metadata drift.
- Upstream is ahead by three commits, but that does not eliminate the fork-only commits.
- `/data/projects/caam-localfix` is a separate temporary local-fix workspace with explicit retirement criteria; it should not be conflated with the primary fork checkout.

The open question for the advisor is whether any reasonable interpretation of "our local fork's changes are now reflected upstream" survives this evidence, or whether the safe verdict is "do not delete yet."
