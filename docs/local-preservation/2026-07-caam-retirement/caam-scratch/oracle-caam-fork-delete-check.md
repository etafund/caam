Do not delete yet.

## Findings

1. **Deletion of both `etafund/caam` and `/data/projects/caam` is not supported.** The evidence identifies `/data/projects/caam` as the primary checkout whose `origin` is `https://github.com/etafund/caam.git` and whose `upstream` is `https://github.com/Dicklesworthstone/coding_agent_account_manager.git`; GitHub metadata confirms `etafund/caam` is a fork of the upstream repo. The same evidence shows `origin/main` and local `main` at `bd15600`, while `upstream/main` is at `98c05c7`, with merge-base `304e037`, so the fork and upstream are not simply the same repository state. There is also an uncommitted local modification in `web/dashboard/playwright-report/index.html`, which alone blocks deletion of the local checkout under a deletion-safety standard.  

2. **The blocker is not merely “the same feature may exist upstream”; the blocker is that the fork still has remaining non-patch-equivalent commits and a large content delta.** The evidence lists many commits reachable from fork `main` but not from `upstream/main`, including update integrity, self-update, coordinator auth, dashboard, TUI, monitor, exec, shallow-profile, Antigravity layout, docs, and plan commits. More importantly, `git cherry -v upstream/main main` reported those fork-side commits with `+`, meaning Git did not find patch-equivalent commits upstream by patch-id. The net diff is also large: `246 files changed, 36472 insertions(+), 6761 deletions(-)`, with substantial changes in CLI, auth, monitor, shallow, TUI, update, dashboard, docs, and files absent upstream such as `PLAN.md`, `cmd/caam/cmd/prompt.go`, `cmd/caam/cmd/schema.go`, many tests, dashboard UI primitives, and multiple docs. That is evidence of remaining fork-specific content, not evidence that the fork has no remaining valuable unique commits.  

3. **`/data/projects/caam-localfix` does not make deletion safer and should be treated separately.** The evidence says `/data/projects/caam-localfix` is not the primary fork checkout and is not a git repo at the top level; it contains a nested checkout at `/data/projects/caam-localfix/caam-src`. Its own docs describe it as a managed temporary local-fix workspace and give separate retirement criteria: a release newer than `v0.1.11` must include fix `0bdd715`, ACFS/autoupdate must no longer downgrade to the no-fix build, the reconciler must be disabled or retired, and release notes plus operational docs must be updated. The nested checkout is stale relative to current upstream, and the runbook says it should stay until the newer-release and no-downgrade conditions are met. 

4. **Policy context reinforces the non-deletion verdict.** AGENTS.md requires clear written permission before deleting any file or folder and treats `rm -rf` or other deletion/overwrite commands as forbidden unless the user provides the exact command and states that they understand and want the irreversible consequences. It also says to stop and seek approval if there is uncertainty. Here the technical evidence is not merely uncertain; it affirmatively shows divergence and uncommitted local state, so no destructive action is justified.  

## Recommended checks

A local agent should rely on read-only or non-destructive confirmation commands like these:

```bash
cd /data/projects/caam

git status --short --branch
git remote -v

git fetch origin
git fetch upstream

git rev-parse main origin/main origin/master upstream/main upstream/master
git merge-base main upstream/main

git log --oneline upstream/main..main
git cherry -v upstream/main main

git diff --stat upstream/main..main
git diff --name-status upstream/main..main
git diff --name-only --diff-filter=A upstream/main..main
```

For the GitHub fork identity and remote heads:

```bash
gh repo view etafund/caam --json nameWithOwner,isFork,parent,defaultBranchRef,viewerPermission,url
gh repo view Dicklesworthstone/coding_agent_account_manager --json nameWithOwner,isFork,defaultBranchRef,viewerPermission,url

git ls-remote origin refs/heads/main refs/heads/master
git ls-remote upstream refs/heads/main refs/heads/master
```

For `/data/projects/caam-localfix`, keep it separate from the fork-deletion decision:

```bash
find /data/projects/caam-localfix -maxdepth 2 -name .git -o -name README.md -o -name '*runbook*'
git -C /data/projects/caam-localfix/caam-src status --short --branch
git -C /data/projects/caam-localfix/caam-src remote -v
git -C /data/projects/caam-localfix/caam-src rev-parse HEAD main origin/main
```

A verdict could only flip to “safe” after the fork-side commit list is empty or demonstrably non-valuable, the large diff is explained away or reconciled, local uncommitted state is preserved or intentionally disposed of under policy, and the GitHub fork and local checkout are confirmed to contain no unique work worth retaining.

## Residual uncertainty

The evidence does not adjudicate the business value of every fork-only commit, nor does it prove that no upstream commits semantically reimplemented some fork features without patch equivalence. But deletion requires proof that there are no remaining unique valuable commits, not merely a plausible feature-level resemblance. The current evidence is ambiguous at best on semantic equivalence and clearly negative on patch/history equivalence, so the safe answer remains: **do not delete yet.**
