# Plan: caam monitor false-error fix + display expansion (beads only)

**Status:** Diagnosis/design workflow running. **Run ID:** `wf_d74f2ccc-c90`
**Script (self-contained, re-runnable):** `/home/ubuntu/orch-homes/cc-cabot/.claude/projects/-data-projects-caam/b6dc6b7f-364f-4db8-bc07-471c0d49a132/workflows/scripts/caam-monitor-beads-wf_d74f2ccc-c90.js`
**Resume:** `Workflow({scriptPath: <above>, resumeFromRunId: "wf_d74f2ccc-c90"})` — completed agents are cached.
**Journal (actual agent returns):** `<transcript dir>/journal.jsonl` under `.../subagents/workflows/wf_d74f2ccc-c90`.

## Mission (from user goal, 2026-07-03)
Two deliverables, BOTH as beads filed via `br` (follow `/beads-workflow`), executed later by a
ChatGPT-5.5 swarm. **Do NOT implement fixes** — diagnose, design, specify. Every bead labeled
`required_provider:any`, cold-start self-contained.

1. **Fix false auth/usage errors** in `caam monitor`: probe reports 429s + "auth expired" for
   healthy accounts (spawning Claude Code against same account works with live /usage).
2. **Expand monitor display** per account: 5H + weekly windows; Claude: Fable usage;
   Codex: GPT-5.3-Codex-Spark 5H + weekly. Constraints: polished human-readable presentation;
   minimal lift (extend existing renderer, no redesign, no new TUI).

## User's observed output (evidence)
```
CLAUDE
[WARN] arthur@feta.fund     no usage: API error: status 429
[WARN] benson@feta.fund     no usage: auth expired (re-login)
[WARN] cabot@feta.fund      no usage: auth expired (re-login)
[WARN] saumil@feta.fund     no usage: API error: status 429
[WARN] saumil_2@feta.fund   no usage: auth expired (re-login)
CODEX
[WARN] arthur@feta.fund     ######--------------  34% | unknown
[WARN] saumil@feta.fund     ##########----------  54% | unknown
```

## Facts already verified in code
- Probe reads tokens from **vault profile copies**, not live dirs: `internal/monitor/monitor.go:288-316`
  (`readAccessToken` → `vault.ProfilePath(...)/.credentials.json` etc.). No refresh in the probe path.
- Claude fetch: `internal/usage/claude.go` GET `api.anthropic.com/api/oauth/usage`, headers
  `anthropic-beta: oauth-2025-04-20`, `User-Agent: caam/1.0`. Already parses `five_hour`,
  `seven_day`, `opus` → Primary/Secondary/TertiaryWindow (so much of deliverable 2 is a render gap).
- "auth expired (re-login)" is a display remap of real API 401/403 at `internal/monitor/alerts.go:94-96`.
- Prime hypothesis: stale vault token copies (live Claude Code refreshes OAuth; vault copies don't).
  429 semantics + codex "unknown" origin were assigned to workflow agents for live evidence.
- `caam monitor --format json --once` exists for repro. `br` 0.2.16; bead ids `caam-XXXX`.

## User decisions (user was AFK; recommended defaults adopted — record in beads for veto)
- **D1:** Live read-only probes with real tokens allowed (GET only, ~6 max, no mutation).
- **D2:** Fix shape: leaning "monitor may refresh tokens + persist to vault", diagnosis decides;
  bead states recommendation + read-only alternative.
- **D3:** Codex "unknown" WARN rows are in scope for deliverable 1.
- **D4:** Per-model usage (Fable / GPT-5.3-Codex-Spark) not exposed by API ⇒ graceful omit.

## Orchestrator steps after workflow completes
1. Read `finalBeadSet` from workflow return (or journal.jsonl if empty).
2. Invoke `/beads-workflow` skill; file beads via `br`: epics first, then tasks; wire `depends_on`
   edges; **every** bead labeled `required_provider:any`; check `filing_notes` for dedup ids
   against existing beads before creating. `br sync --flush-only` when done.
3. Oracle adversarial review (background, 5–30 min): export filed bead bodies to
   `scratch/oracle-caam-monitor-beads-input.md`; have a Sonnet subagent run `/rewrite-prompt` on the
   brief; then:
   `oracle --engine browser --model gpt-5.5-pro --browser-thinking-time extended --slug "caam-monitor-beads-review" --write-output "scratch/oracle-caam-monitor-beads-review.md" --heartbeat 30 -p "<brief>" --file AGENTS.md --file scratch/oracle-caam-monitor-beads-input.md`
   On return: apply accepted refinements via `br update`, record accept/reject per bead comment.
4. Final user report: root-cause narrative, bead ids + dependency graph, D1–D4 defaults for veto.

## Constraints
- No implementation, no commits of source changes. Beads + scratch notes only.
- Never print token values (8-char prefix max). No writes to vault/live credential files.
- Sole scheduler: skip file reservations / Agent Mail / pane identity ceremony.

## OUTCOME (2026-07-03)
Filed 8 beads (2 epics + 6 children), all `required_provider:any`, flushed to JSONL. Prefix set to `caam` (`br config set issue_prefix caam`).
- Epic `caam-fix-monitor-false-errors-gk10` (open): .1 refresh/auth [OPEN, reopened], .2 429-pacing [OPEN], .3 pool-status-noise [CLOSED].
- Epic `caam-expand-monitor-display-gqds` (open): .1 model-windows [CLOSED], .2 detail-line [IN_PROGRESS], .3 json/brief parity [OPEN].

### Adversarial review applied (oracle gpt-5.5-pro [truncated to summary] + Sonnet repo-verified pass)
- **CRITICAL — gk10.1 re-scoped:** original "refresh Claude tokens via refresh.RefreshProfile" is FORBIDDEN by CLAUDE-006 (refresh.go:100-114, claude.go:31, docs/CLAUDE_AUTH_INVENTORY.md) — a no-op for Claude. Reopened + re-scoped to: no Claude refresh; read live token for the active profile (Vault.ActiveProfile authfile.go:953); honest "cached·inactive" label for stale inactive profiles. Full live-usage-for-inactive-Claude would require reversing CLAUDE-006 → flagged for HUMAN decision.
- **gk10.2 corrected:** rebased onto the post-gk10.3 renderer refactor (tableIndicator/tableStatusSuffix); rate-limited caveat attaches in tableStatusSuffix via a RetryAfter/RateLimited field; added hard dep gk10.2→gk10.3.
- Minor findings (health Critical-vs-Warning; omitempty on ResetsAt; line drift) recorded as bead comments.

### SITUATION: a LIVE swarm is already implementing these beads (codex --yolo / claude --dangerously-skip-permissions procs). Contradicts the "sole scheduler / later execution" assumption. Work is uncommitted in the working tree (+954 lines across internal/monitor, internal/usage). gk10.1 was CLOSED by the swarm against the broken spec → reopened.

### NOT committed to git (on main, pre-existing uncommitted .beads changes) — left for user.
