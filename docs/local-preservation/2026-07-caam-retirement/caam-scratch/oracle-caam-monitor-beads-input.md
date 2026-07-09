# caam monitor bead set — review input

## Root cause summary

caam monitor probes provider usage APIs with access tokens read straight from the VAULT profile copies (internal/monitor/monitor.go:288-316) and never refreshes them. Live probing confirmed all 5 Claude vault tokens were 4-9.5h expired on disk while still holding valid refresh tokens: live Claude Code refreshes its OAuth token on spawn but never writes it back to the vault, so non-active vault copies go stale and the api.anthropic.com/api/oauth/usage probe returns real 401s that alerts.go:90-98 remaps to the misleading 'auth expired (re-login)'. The '429' rows are not a per-account condition (isolated spaced probes returned 401, never 429) but the unthrottled 5-way concurrent request burst in usage/multi.go:55-108 tripping Anthropic's rate limiter. The Codex '| unknown' + [WARN] on successful rows is a third, shared defect: cmd/caam/cmd/monitor.go:116 injects a nil *authpool.AuthPool, so PoolStatus defaults to Unknown for every profile; it is masked on Claude only because Claude fetches currently fail, and will surface on Claude the moment the auth fix lands. Deliverable 1 (epic fix-monitor-false-errors) fixes all three; deliverable 2 (epic expand-monitor-display) minimally extends the existing table renderer to show 5H + weekly windows plus per-model usage — Codex GPT-5.3-Codex-Spark is confirmed available via the wham/usage additional_rate_limits[] array; a Claude 'Fable' bucket is not exposed today, so it is captured tolerantly and omitted gracefully per D4.


## Filing notes / dependency model

8 beads: 2 epics + 6 children. Create both epics first, then children with --parent set to the epic id and --labels required_provider:any on EVERY bead. Cross-epic/inter-task blocking edges (br dep add): display-detail-line depends on display-model-windows AND monitor-pool-status-noise; display-json-brief-parity depends on display-model-windows AND display-detail-line. The fix children have no inter-dependencies (parallelizable). Priorities: epics P1; refresh-stale-tokens & 429-pacing P1; pool-status-noise P2 (but note in-body it must ship WITH the auth fix); display-model-windows & display-detail-line P1; json-brief-parity P2. Dedup: no existing beads overlap (checked br list — caam-ewnc/caam-7pi3.4/caam-l19o.1.31 are unrelated coordinator/dashboard/TUI work). Run 'br sync --flush-only' after filing. Evidence file:line anchors verified against the current tree.


## Filed bead ids (slug -> id)

- fix-monitor-false-errors -> caam-fix-monitor-false-errors-gk10  [epic/P1] parent=None deps=[]
- monitor-refresh-stale-tokens -> caam-fix-monitor-false-errors-gk10.1  [bug/P1] parent=fix-monitor-false-errors deps=[]
- monitor-429-pacing -> caam-fix-monitor-false-errors-gk10.2  [bug/P1] parent=fix-monitor-false-errors deps=[]
- monitor-pool-status-noise -> caam-fix-monitor-false-errors-gk10.3  [bug/P2] parent=fix-monitor-false-errors deps=[]
- expand-monitor-display -> caam-expand-monitor-display-gqds  [epic/P1] parent=None deps=[]
- display-model-windows -> caam-expand-monitor-display-gqds.1  [task/P1] parent=expand-monitor-display deps=[]
- display-detail-line -> caam-expand-monitor-display-gqds.2  [task/P1] parent=expand-monitor-display deps=['display-model-windows', 'monitor-pool-status-noise']
- display-json-brief-parity -> caam-expand-monitor-display-gqds.3  [task/P2] parent=expand-monitor-display deps=['display-model-windows', 'display-detail-line']

---


## BEAD caam-fix-monitor-false-errors-gk10 — Epic: fix caam monitor false auth/usage errors for healthy accounts

type=epic priority=P1 parent=None depends_on=[]

# Epic: `caam monitor` reports false auth/usage errors for healthy accounts

## User report (2026-07-03)
Every Claude account is flagged broken, yet all accounts are healthy — `caam shallow-spawn cc-cabot -- claude --dangerously-skip-permissions` comes up logged in with a working `/usage`. Observed output:

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

## Root causes — three independent defects, all confirmed by live probing + code trace
1. **Stale vault tokens → false "auth expired".** `Monitor.Refresh()` reads access tokens straight from the vault profile copies via `readAccessToken()` (internal/monitor/monitor.go:288-316) and never refreshes them. Live probe confirmed all 5 Claude vault `.credentials.json` access tokens were expired on disk (4.1h–9.5h past `expiresAt`), each still carrying a valid `refreshToken`. Live Claude Code refreshes its OAuth token on spawn but never writes the refreshed token back to the vault, so non-active vault copies always go stale. A stale vault token returns a clean **HTTP 401** `authentication_error` from `GET https://api.anthropic.com/api/oauth/usage`; the same token used fresh returns **200** with full usage. `internal/monitor/alerts.go:90-98 shortUsageError()` remaps the 401 to the misleading "auth expired (re-login)". Child: `monitor-refresh-stale-tokens`.
2. **Self-inflicted 429 burst.** The two "status 429" rows were NOT a per-account condition: re-probing arthur and saumil individually (spaced >=2s) returned the same clean **401** as the others, never a 429. `internal/usage/multi.go:55-108 FetchAllProfiles()` fires one goroutine per profile with no stagger, concurrency cap, or backoff — 5 simultaneous GETs to the same endpoint from one IP every 30s (default interval, monitor.go:88). That burst trips Anthropic's rate limiter nondeterministically; whichever 2 requests lose the race surface as 429. Child: `monitor-429-pacing`.
3. **"| unknown" + [WARN] on rows whose fetch SUCCEEDED.** The Codex bar rendering proves the fetch worked. `cmd/caam/cmd/monitor.go:116` declares `var pool *authpool.AuthPool` and **never assigns it**, then passes it at :135 `monitor.WithAuthPool(pool)` — a permanently nil pool. `buildProfileState` (monitor.go:318-336) nil-guards on `m.pool`, so `PoolStatus` stays at its zero value `authpool.PoolStatusUnknown` for **every profile of every provider**, which `render_table.go:71` prints literally as "unknown". It is only visible on Codex today because Codex fetches succeed (reaching the success render branch); Claude currently fails earlier. **Sequencing consequence: the moment child 1 makes Claude fetches succeed, Claude rows will start showing "| unknown" too** — this is a shared, compounding defect and must ship in the same pass. Child: `monitor-pool-status-noise`.

## Definition of done
`caam monitor --once` (both `--format table` and `--format json`) on a machine with healthy Claude + Codex profiles shows real usage bars for every account, with **no** "auth expired", **no** "status 429", **no** "| unknown", and **no** [WARN] on any row whose fetch succeeded and is below alert thresholds.

## Recorded user decisions (defaults adopted 2026-07-03 while user AFK — user may veto)
- **D1**: Live read-only probes with real tokens are permitted for verification (GET only, ~6 requests max, spaced >=2s, no logins). Token *refresh* is allowed only per D2 and only through the existing `internal/refresh` code paths.
- **D2**: Fix shape — **recommended**: the monitor probe may refresh expired tokens and persist them to the vault via `internal/refresh.RefreshProfile`. The read-only alternative (label the row "token stale — run `caam refresh`" without fetching usage) is documented as the veto fallback.
- **D3**: Codex "unknown" WARN rows are in scope for deliverable 1.
- **D4**: If a per-model bucket (Claude "Fable", Codex "GPT-5.3-Codex-Spark") is not exposed by the endpoint, display it only when present and omit it cleanly when absent — no `--`/`n/a`/placeholder.

## Operational note for the executing agent (verified live)
The real multi-profile vault is at `/home/ubuntu/.local/share/caam/vault` (`authfile.DefaultVaultPath()` resolves `CAAM_HOME` > `XDG_DATA_HOME` > `~/.local/share/caam/vault`, internal/authfile/authfile.go:336-347). A `caam shallow-spawn` sub-home has its OWN empty vault, so any manual `caam monitor` reproduction must run with the real operator `HOME` (e.g. `HOME=/home/ubuntu`) or it will report zero profiles.

## Security constraints
- Never log or print token values (at most first 8 chars + length in debug output).
- Read-only GETs against provider usage APIs; the only writes are vault credential updates performed inside `internal/refresh`.
- The monitor must never write to the live `~/.claude` / `~/.codex` credential files.

## Verification
```
go test ./internal/monitor/... ./internal/usage/... ./internal/refresh/...
HOME=/home/ubuntu caam monitor --format json --once
HOME=/home/ubuntu caam monitor --format table --once
```
Label: `required_provider:any`.



## BEAD caam-fix-monitor-false-errors-gk10.1 — Monitor: refresh expired vault tokens before declaring 'auth expired'

type=bug priority=P1 parent=fix-monitor-false-errors depends_on=[]

# Bug: "auth expired (re-login)" shown for healthy accounts because vault tokens are stale on disk

Part of epic `fix-monitor-false-errors`. Label: `required_provider:any`.

## Problem
`[WARN] benson@feta.fund  no usage: auth expired (re-login)` (also cabot, saumil_2) while those profiles spawn healthy and serve `/usage`.

## Root cause (confirmed live + in code)
- `Monitor.Refresh()` (internal/monitor/monitor.go:129) builds a token map from `readAccessToken()` (monitor.go:288-316), reading `vault.ProfilePath(provider,name)/.credentials.json` (claude; fallbacks `.claude.json`, `auth.json`) or `auth.json` (codex). **No refresh happens anywhere in the probe.**
- Live evidence: all 5 Claude vault `.credentials.json` access tokens were 4.1h–9.5h past their `expiresAt` (epoch-ms) at probe time; each still had a valid `refreshToken` and identical scopes. The live cc-cabot orch-home token (post real `claude` launch) was a *different, fresh* token than the vault's cabot copy at the same instant — direct proof Claude Code refreshes transparently but never syncs back to the vault.
- A stale vault token → `GET https://api.anthropic.com/api/oauth/usage` returns **HTTP 401** `{"type":"error","error":{"type":"authentication_error",...}}` (no Retry-After, no rate-limit headers). `internal/usage/claude.go:80-89` maps 401/403 to "unauthorized: token expired or invalid"; `alerts.go:90-98 shortUsageError()` displays "auth expired (re-login)".
- **Codex is NOT affected**: codex `auth.json` tokens are long-lived JWTs (exp 3–5.6 days out); vault staleness is a Claude-only problem today.
- **Reuse point already exists**: `internal/refresh/refresh.go:40 RefreshProfile(ctx, provider, profile string, vault *authfile.Vault, store *health.Storage) error` refreshes claude/codex tokens using the vault refresh token, writes updated creds back to the vault, guards against clobbering live files, and returns `*refresh.RefreshTokenReusedError` (errors.go:57) when the refresh token was already rotated. `ShouldRefresh(h, threshold)` at refresh.go:26.

## Decision D2 (recommended; veto fallback documented)
**Implement**: the monitor may refresh expired tokens and persist them to the vault, exclusively via `refresh.RefreshProfile`. Rationale: it is the only fix that lets the probe report truthful usage for non-active profiles, and the refresh package already handles rotation races.
**Rejected read-only fallback** (keep documented in case of veto): detect expiry locally and display "token stale — run `caam refresh <profile>`" instead of "auth expired"; rejected because usage stays unavailable for healthy accounts.

## Fix spec (internal/monitor/monitor.go)
1. In the token-collection loop (~monitor.go:167-192), read the expiry alongside the token. If the access token is missing/expired/expires within a 2-minute skew, call `refresh.RefreshProfile(ctx, provider, name, m.vault, m.health)` once, then re-read the token from the vault. (The `ReadClaudeCredentials`/`ReadCodexCredentials` helpers already return a second value — use/extend it to surface `expiresAt`.)
2. Add a single 401-retry path: after `FetchAllProfiles`, for any result whose `UsageInfo.Error` is the unauthorized string, attempt exactly ONE `RefreshProfile` + ONE re-fetch for that profile. Never loop.
3. Throttle: at most one refresh attempt per (provider, profile) per monitor interval — track last-attempt timestamps on the Monitor struct so the 30s loop cannot hammer the token endpoint after a hard failure.
4. Failure semantics: on `*RefreshTokenReusedError` or any refresh error, keep today's behavior (row shows "auth expired (re-login)") — now a TRUE statement.
5. Make refresh injectable for tests: add a `refreshFn func(ctx, provider, name) error` field defaulting to the real implementation (mirror the existing `WithFetcher` option, monitor.go:56).

## Behavior before/after
- Before: healthy but non-active Claude profiles show "auth expired (re-login)" forever.
- After: first `Refresh()` transparently refreshes stale vault tokens; rows show real usage. Only genuinely dead refresh tokens show a re-login hint (after exactly one attempt/interval).

## Acceptance criteria
- [ ] `caam monitor --once` on a host with healthy-but-inactive Claude profiles shows real usage for all of them; no "auth expired" rows.
- [ ] A profile with a truly invalid refresh token still shows a re-login hint after exactly one refresh attempt per interval.
- [ ] Tests are offline (httptest / injected `refreshFn`); no token value is ever logged; monitor never writes to live `~/.claude`.

## Test guidance
`internal/monitor/monitor_test.go`: (a) expired-expiry credential in a temp vault triggers `refreshFn` exactly once and re-reads the new token; (b) a fetch error "unauthorized: token expired or invalid" triggers one refresh + one re-fetch, and the successful re-fetch lands in state; (c) `refreshFn` failure leaves the original error and does not retry within the same `Refresh()`; (d) two `Refresh()` calls within the throttle window attempt refresh at most once. Reuse the existing fake-fetcher patterns.

## Security constraints
- Never log or print token values (at most first 8 chars + length in debug output).
- Read-only GETs against provider usage APIs; the only writes are vault credential updates performed inside `internal/refresh`.
- The monitor must never write to the live `~/.claude` / `~/.codex` credential files.



## BEAD caam-fix-monitor-false-errors-gk10.2 — Monitor: stop self-inflicted 429s (space per-provider fetches, honor Retry-After, keep last-good)

type=bug priority=P1 parent=fix-monitor-false-errors depends_on=[]

# Bug: monitor's concurrent request burst provokes 429s, rendered as fatal-looking errors

Part of epic `fix-monitor-false-errors`. Label: `required_provider:any`.

## Problem
`[WARN] arthur@feta.fund  no usage: API error: status 429` (also saumil) while the same accounts work interactively.

## Root cause (confirmed live + in code)
- Re-probing arthur and saumil **individually**, spaced >=2s, returned clean **HTTP 401** (stale token) — never a 429. So the 429s are not a per-account property.
- `internal/usage/multi.go:55-108 MultiProfileFetcher.FetchAllProfiles()` launches one goroutine per profile (`go func(...)` at multi.go:66) with no stagger, concurrency cap, or backoff → 5 simultaneous GETs to `api.anthropic.com/api/oauth/usage` from one IP.
- `monitor.go:88` default interval is 30s and `Start()` re-runs the burst every tick.
- `internal/usage/claude.go` maps any non-200/401/403 to "API error: status N", ignores `Retry-After`, and each `Refresh()` builds a fresh `MonitorState` (monitor.go:130) so the row's previous good usage is discarded.

## Fix spec — pacing (primary) + presentation (safety net)
### 1. Pace requests — `internal/usage/multi.go`
- Serialize per-provider (or cap concurrency at 1–2) and space successive same-provider requests by ~750ms ±250ms jitter. Providers still run in parallel with each other (the per-provider fan-out in monitor.go:199-205 is fine). Sort profile names for deterministic test order. Respect `ctx` cancellation during spacing.

### 2. 429 handling — `internal/usage/claude.go` (+ codex.go for symmetry)
- Add a distinct `http.StatusTooManyRequests` case: set a machine-recognizable "rate limited" error (add a sentinel/helper in `internal/usage/usage.go`) and parse `Retry-After` (integer seconds AND HTTP-date) into a new `UsageInfo.RetryAfter time.Duration`. Do NOT retry inside the fetcher.

### 3. State retention + display — `internal/monitor`
- In `Refresh()` (monitor.go:129): when a fetch fails "rate limited" AND the previous state holds real usage (`MostConstrainedWindow() != nil`), carry the previous `UsageInfo` forward (preserve its original `FetchedAt` to mark it stale). The row keeps its last-known bar.
- In `alerts.go shortUsageError()` (alerts.go:89): map the rate-limited error to `"rate limited (retrying)"` — ASCII only (see the byte-width padding note at alerts.go:82).
- A 429 row must NOT get a WARN/CRIT indicator on its own (it is transient) — coordinate with `monitor-pool-status-noise`, which reworks the indicator.

## Behavior before/after
- Before: 5-way burst every 30s; 429 rows show "no usage: API error: status 429" with WARN and lose their bar.
- After: requests are spaced and the endpoint is not provoked; a residual 429 keeps the last-known bar with a calm "rate limited (retrying)" note.

## Acceptance criteria
- [ ] With 5 healthy Claude profiles, repeated `caam monitor --once` shows real usage for all (no 429 rows).
- [ ] A unit test proves `FetchAllProfiles` never exceeds the configured in-flight cap per provider (httptest server counting concurrent requests).
- [ ] A 429 with `Retry-After: 30` populates `UsageInfo.RetryAfter` and yields the "rate limited" error string (test both integer-seconds and HTTP-date).
- [ ] A rate-limited profile with prior good usage keeps its bar in the rendered table.

## Test guidance
`internal/usage/multi_test.go` (create if absent): concurrency-cap + context-cancellation-during-spacing. `internal/usage/claude_test.go` / `codex_test.go`: 429 + Retry-After parsing. `internal/monitor/monitor_test.go`: previous-state retention. `internal/monitor/alerts_test.go`: `shortUsageError` mapping for the new string. All offline.

## Security constraints
- Never log or print token values (at most first 8 chars + length in debug output).
- Read-only GETs against provider usage APIs; the only writes are vault credential updates performed inside `internal/refresh`.
- The monitor must never write to the live `~/.claude` / `~/.codex` credential files.



## BEAD caam-fix-monitor-false-errors-gk10.3 — Monitor table: drop false 'unknown' pool status and WARN on successful rows

type=bug priority=P2 parent=fix-monitor-false-errors depends_on=[]

# Bug: successful rows render as `[WARN] ... 34% | unknown`

Part of epic `fix-monitor-false-errors`. In scope per decision **D3**. Label: `required_provider:any`.

## Problem
```
CODEX
[WARN] arthur@feta.fund     ######--------------  34% | unknown
[WARN] saumil@feta.fund     ##########----------  54% | unknown
```
The bar proves the fetch SUCCEEDED (real 34%/54% from `chatgpt.com/backend-api/wham/usage`). Both `[WARN]` and `unknown` are noise unrelated to the fetch.

## Root cause (confirmed live + in code)
- **"| unknown"**: `cmd/caam/cmd/monitor.go:116` declares `var pool *authpool.AuthPool` and **never assigns it** (no `authpool.NewAuthPool(...)`), then passes it at :135 `monitor.WithAuthPool(pool)`. `buildProfileState` (internal/monitor/monitor.go:318-336) does `if m.pool != nil { state.PoolStatus = ... }`, so with a nil pool `PoolStatus` stays at its zero value `authpool.PoolStatusUnknown` (default set at monitor.go:324). `render_table.go:71` prints `p.PoolStatus.String()` == "unknown" — but only on the success branch (when `usageUnavailable()` returns empty). Live `auth_pool_state.json` has `"profiles": {}` and no caam daemon is running, so nothing populates the pool by any path. The correct construction pattern exists at `cmd/caam/cmd/pool.go:73` (`authpool.NewAuthPool(authpool.WithVault(vault))`).
- **`[WARN]` prefix**: `render_table.go:57 healthEmoji(p.Health)`; `p.Health` comes from `buildProfileState` reading the health store (`health.CalculateStatus`), whose stale expiry timestamps / past error counts yield Warning even when the fetch just succeeded. The freshly fetched `UsageInfo` is never fed back into the displayed health.
- **Compounding / shared bug**: this is currently masked on Claude because Claude fetches fail and take the "auth expired" branch (which never touches PoolStatus). Once `monitor-refresh-stale-tokens` makes Claude fetches succeed, **Claude rows will also show "| unknown"** — so this MUST land in the same pass as the auth fix.

## Fix spec (minimal lift — do NOT redesign the renderer)
`internal/monitor/render_table.go` (success branch, ~lines 66-77):
1. When `PoolStatus == PoolStatusUnknown`, do not print "unknown". Instead:
   - in cooldown → keep existing `cooldown Xm` text (render_table.go:72-74);
   - else if the constrained window has a reset time → show `resets <compact dur>` via the existing `formatDuration` (render.go:198) and `UsageInfo.MostConstrainedWindow().ResetsAt`;
   - else → omit the `| ...` suffix entirely.
   Only print a real pool status when it is not Unknown.
2. Indicator honesty: when the row has real usage data (`MostConstrainedWindow() != nil`) and no error, drive the indicator from the usage-based alert (`p.Alert` via `evaluateAlert`) — show OK unless Warning/Critical/Exhausted. Health-store status may still drive the indicator for rows WITHOUT fresh usage. Keep `healthEmoji`/`alertEmoji` as-is; change only which one the success row uses.
3. ASCII only (byte-width padding, alerts.go:82). Apply identically to all providers.
4. `--format json` unchanged (JSON exposes `pool_status` separately, render.go:60-67) — this bead is table-only.

(A deeper alternative — actually construct the AuthPool in `cmd/caam/cmd/monitor.go` mirroring pool.go:73 — is NOT required and is heavier: `LoadFromVault` itself sets status Unknown per its own doc (authpool/pool.go:510-511), so it would not remove the "unknown" without more wiring. The renderer fix is the minimal-lift solution and is provider-agnostic.)

## Behavior before/after
- Before: `[WARN] arthur@feta.fund  ######--------------  34% | unknown`
- After:  `[OK] arthur@feta.fund   ######--------------  34% | resets 2h 10m` (suffix omitted if no reset time; WARN/CRIT/EXH only when usage alert thresholds are crossed or the fetch failed).

## Acceptance criteria
- [ ] No literal "unknown" in any table row; no WARN indicator on any row whose fetch succeeded and is below alert thresholds.
- [ ] Rows crossing the usage alert threshold still show WARN/CRIT/EXH; pool-enrolled profiles still show real pool status; cooldown display unchanged.
- [ ] Table borders stay aligned (byte-width padding; ASCII only).

## Test guidance
`internal/monitor/render_test.go` (add `render_table_test.go` if cleaner): (a) success + PoolStatusUnknown + reset time → `resets ...` suffix, OK indicator; (b) success at 96% util → CRIT indicator; (c) failed fetch → unchanged "no usage: ..." + health-driven indicator; (d) PoolStatus active → real status printed; (e) every rendered line identical byte length. Mirror the existing table-render test style.

## Security constraints
- Never log or print token values (at most first 8 chars + length in debug output).
- Read-only GETs against provider usage APIs; the only writes are vault credential updates performed inside `internal/refresh`.
- The monitor must never write to the live `~/.claude` / `~/.codex` credential files.



## BEAD caam-expand-monitor-display-gqds — Epic: expand caam monitor per-account display (5H + weekly + per-model usage)

type=epic priority=P1 parent=None depends_on=[]

# Epic: expand `caam monitor` per-account display

## Goal
`caam monitor --format table` (default `--width 75`) currently shows ONE bar per account, derived from the 5-hour window only. Expand it so every account shows, in a genuinely pleasant, human-readable way:
- **Claude**: 5H window, weekly (7-day) window, and Fable model usage *if the API exposes it*.
- **Codex**: 5H window, weekly window, and GPT-5.3-Codex-Spark usage (5H + weekly) *if exposed*.

## What the data layer already gives us (verified live + in code)
- Claude parser (internal/usage/claude.go:99-138) already fills `PrimaryWindow` (5H) and `SecondaryWindow` (7-day); Codex parser (codex.go:166-186) fills Primary/Secondary from `rate_limit.{primary_window,secondary_window}` (confirmed `limit_window_seconds` 18000=5h / 604800=weekly). **The renderer discards the weekly window today** — `render_table.go:60-78` collapses everything to a single primary-window bar via `usagePercent()` (alerts.go:110).
- `UsageInfo.ModelWindows map[string]*UsageWindow` (internal/usage/usage.go:55, `json:"model_windows,omitempty"`) already exists and is **populated nowhere** — the natural home for per-model data.
- **Confirmed API reality (drives D4 graceful omission):**
  - Claude `GET .../api/oauth/usage` (fresh token, 200) top-level keys include `five_hour`, `seven_day`, `seven_day_opus`, `seven_day_sonnet`, and several currently-null internal codenames — but **no key named "fable"**, and no `five_hour_<model>` analogs. So a Claude "Fable" bucket is not reliably identifiable today and must gracefully omit; `seven_day_opus`/`seven_day_sonnet` DO exist and may be surfaced when populated.
  - Codex `GET .../wham/usage` (200) returns a top-level **`additional_rate_limits[]`** array; entry 0 is `{"limit_name":"GPT-5.3-Codex-Spark","metered_feature":"codex_bengalfox","rate_limit":{"primary_window":{...,limit_window_seconds:18000},"secondary_window":{...,limit_window_seconds:604800}}}` — a literal match for the requested Spark 5H+weekly. The current `codexUsageResponse` struct has no field for it, so it is silently discarded.
  - Pre-existing bug to fix here: `claude.go:36` decodes `json:"opus"` but the real field is `seven_day_opus`, so `TertiaryWindow` never populates (dead code).

## Hard constraints (user decisions; user may veto)
- **Minimal lift**: extend `internal/monitor/render_table.go` + the two parsers. No redesign, no new TUI, no new dependencies.
- **D4 graceful omission**: when a window/per-model field is absent, it is cleanly absent — no `--`, no `n/a`, no dangling `|`.
- `--format json` / `brief` stay coherent (see `display-json-brief-parity`).

## Recommended layout (exact spec lives in `display-detail-line`)
75-col table (innerWidth 73): keep the existing account row untouched, add ONE indented detail line under each account that has usage data.
```
+-------------------------------------------------------------------------+
|  CLAUDE                                                                 |
|  [OK] cabot@feta.fund       ########------------  42% | resets 1h12m    |
|       5H 42% (resets 1h12m) | WK 63% (resets 2d4h)                      |
|  CODEX                                                                  |
|  [OK] arthur@feta.fund      ######--------------  34% | resets 3h1m     |
|       5H 34% (resets 3h1m) | WK 54% (resets 4d18h) | SPARK 5H 22% WK 41%|
+-------------------------------------------------------------------------+
```
(`FABLE n%` appears in the Claude detail line only if a fable window is ever present — omitted otherwise per D4. If a detail line would overflow innerWidth, drop reset hints before percentages.)
Rejected alternatives: multi-column per-account grid (redesign); a second full bar per window (vertical bloat); folding weekly into the status column (breaks status semantics at render_table.go:71-76).

## Task breakdown
1. `display-model-windows` — tolerantly capture per-model windows into `ModelWindows` (both providers) + fix the `opus` json-tag bug + add a `FindModelWindow` helper.
2. `display-detail-line` — render the indented 5H/WK/per-model detail line with day-aware durations.
3. `display-json-brief-parity` — verify JSON carries the new data, keep brief unchanged, add regression tests.

## Dependency note
End-to-end verification depends on the deliverable-1 fix (stale tokens → live rows show usage), and `display-detail-line` shares `render_table.go` with `monitor-pool-status-noise`, so it is wired after that bead to avoid a merge conflict. Implementation + unit tests are NOT blocked (all render/parse tests use fixtures).

## Recorded user decisions (defaults adopted 2026-07-03 while user AFK — user may veto)
- **D1**: Live read-only probes with real tokens are permitted for verification (GET only, ~6 requests max, spaced >=2s, no logins). Token *refresh* is allowed only per D2 and only through the existing `internal/refresh` code paths.
- **D2**: Fix shape — **recommended**: the monitor probe may refresh expired tokens and persist them to the vault via `internal/refresh.RefreshProfile`. The read-only alternative (label the row "token stale — run `caam refresh`" without fetching usage) is documented as the veto fallback.
- **D3**: Codex "unknown" WARN rows are in scope for deliverable 1.
- **D4**: If a per-model bucket (Claude "Fable", Codex "GPT-5.3-Codex-Spark") is not exposed by the endpoint, display it only when present and omit it cleanly when absent — no `--`/`n/a`/placeholder.

## Operational note for the executing agent (verified live)
The real multi-profile vault is at `/home/ubuntu/.local/share/caam/vault` (`authfile.DefaultVaultPath()` resolves `CAAM_HOME` > `XDG_DATA_HOME` > `~/.local/share/caam/vault`, internal/authfile/authfile.go:336-347). A `caam shallow-spawn` sub-home has its OWN empty vault, so any manual `caam monitor` reproduction must run with the real operator `HOME` (e.g. `HOME=/home/ubuntu`) or it will report zero profiles.

## Acceptance (epic-level)
- [ ] `caam monitor --once` at width 75 renders the detail lines cleanly for accounts with data; error rows unchanged; `--no-emoji` stays aligned.
- [ ] Accounts missing weekly/per-model data show only the segments that exist; an account with no windows gets no detail line.
- [ ] `go test ./internal/usage/... ./internal/monitor/...` passes; `go vet ./...` clean.

Label: `required_provider:any`.



## BEAD caam-expand-monitor-display-gqds.1 — Parse per-model usage windows (Codex Spark; Claude per-model) into UsageInfo.ModelWindows

type=task priority=P1 parent=expand-monitor-display depends_on=[]

# Task: capture per-model usage windows into `UsageInfo.ModelWindows`

Part of epic `expand-monitor-display`. Label: `required_provider:any`.

## Context
`internal/usage/usage.go:55` defines `ModelWindows map[string]*UsageWindow` (`json:"model_windows,omitempty"`), populated nowhere today. Goal: fill it so the renderer can show `SPARK 5H 22% WK 41%` (Codex) and `FABLE n%` (Claude, if ever present). Per **D4**, if the endpoint exposes no per-model data, `ModelWindows` stays nil and downstream omits gracefully. Parse tolerantly; do not fabricate schema.

## Confirmed schema (verified against the live endpoints)
- **Codex** `GET https://chatgpt.com/backend-api/wham/usage` returns a top-level `additional_rate_limits` **array**. Each element: `{"limit_name": string, "metered_feature": string, "rate_limit": {"primary_window": <window>, "secondary_window": <window>}}`, where `<window>` has the SAME shape as `codexWindow` today (`used_percent`, `reset_at`, `limit_window_seconds`; codex.go:48-52). The Spark entry is `limit_name="GPT-5.3-Codex-Spark"`, `metered_feature="codex_bengalfox"`, primary=18000s (5h), secondary=604800s (weekly). The current `codexUsageResponse` (codex.go:37-41: `RateLimit`, `Credits`) has NO field for `additional_rate_limits`, so it is dropped by `json.Decode`.
- **Claude** `GET .../api/oauth/usage`: top-level keys observed include `five_hour`, `seven_day`, `seven_day_opus`, `seven_day_sonnet` (each `{utilization, resets_at, ...}`), plus null internal codenames. **No "fable" key exists today.** `claude.go:33-42 claudeUsageResponse` decodes only `five_hour`/`seven_day`/`opus`, and `opus` is the wrong tag (real field is `seven_day_opus`) so `TertiaryWindow` is dead code.

## Spec
### Codex — `internal/usage/codex.go`
1. Add `AdditionalRateLimits []codexAdditionalLimit \`json:"additional_rate_limits"\`` to `codexUsageResponse`, with `type codexAdditionalLimit struct { LimitName string \`json:"limit_name"\`; MeteredFeature string \`json:"metered_feature"\`; RateLimit *codexRateLimit \`json:"rate_limit"\` }` (reuse the existing `codexRateLimit`/`codexWindow`).
2. After the existing top-level parse (codex.go:166-186), for each additional limit: derive a stable model key — prefer `metered_feature` if non-empty else a normalized `limit_name` (lowercased, spaces→`-`). Store `info.ModelWindows[key+"/5h"]` from `primary_window` and `info.ModelWindows[key+"/weekly"]` from `secondary_window`, using the SAME conversion as codex.go:167-185 (`Utilization: used_percent/100`, `UsedPercent`, `ResetsAt: time.Unix(reset_at,0)`, `WindowDuration` from `limit_window_seconds`). Also store the human `limit_name` where useful for display.

### Claude — `internal/usage/claude.go`
1. **Fix the tag bug**: rename the `opus` field to decode `json:"seven_day_opus"` (keep `TertiaryWindow` mapping), OR fold it into the tolerant pass below.
2. Two-pass tolerant decode: decode the body into `map[string]json.RawMessage`; unmarshal the known keys (`five_hour`, `seven_day`) into `claudeWindow` exactly as today (keep the >1 percentage normalization at claude.go:99-125). For every OTHER key whose value unmarshals cleanly into `claudeWindow` (numeric `utilization`; `resets_at` optional) — e.g. `seven_day_opus`, `seven_day_sonnet`, a future `*fable*` — add it to `info.ModelWindows[key]` with the same normalization and `WindowDuration` 168h for `seven_day_*` keys / 5h for any `five_hour_*` key. Keys that don't unmarshal as a window are skipped silently. Do NOT change the 401/403/error branches (owned by deliverable 1).

### Helper — `internal/usage/usage.go`
```go
// FindModelWindow returns the first ModelWindows entry whose key contains substr
// (case-insensitive); optional suffix filter ("5h","weekly","seven_day",...) may be "".
// Deterministic on ties (lexicographically smallest key). nil-safe.
func (u *UsageInfo) FindModelWindow(substr, suffix string) *UsageWindow
```

## Acceptance criteria
- [ ] Existing `internal/usage` tests pass unmodified; accounts returning today's known schema produce byte-identical `UsageInfo` apart from the newly-populated `ModelWindows` (and the now-correctly-populated `TertiaryWindow`).
- [ ] Codex fixture with `additional_rate_limits:[{limit_name:"GPT-5.3-Codex-Spark",metered_feature:"codex_bengalfox",rate_limit:{primary_window:{used_percent:22,limit_window_seconds:18000,reset_at:...},secondary_window:{used_percent:41,limit_window_seconds:604800,reset_at:...}}}]` → `ModelWindows` has a 5h entry (22%) and weekly entry (41%); fixture without the array → nil.
- [ ] Claude fixture with `seven_day_opus`/`seven_day_sonnet` (+ an unknown `plan:"max"` string key) → those two captured into `ModelWindows`; `plan` skipped; `five_hour`/`seven_day` unchanged. Fixture with no extra keys → `ModelWindows` nil.
- [ ] `FindModelWindow` covered: `("spark","weekly")`, `("spark","5h")`, case-insensitivity, tie-break, nil-safety.
- [ ] Tests use fixtures only; no live API calls; never log token values.

## Security constraints
- Never log or print token values (at most first 8 chars + length in debug output).
- Read-only GETs against provider usage APIs; the only writes are vault credential updates performed inside `internal/refresh`.
- The monitor must never write to the live `~/.claude` / `~/.codex` credential files.



## BEAD caam-expand-monitor-display-gqds.2 — Render 5H/WK/per-model detail line under each monitor account row

type=task priority=P1 parent=expand-monitor-display depends_on=['display-model-windows', 'monitor-pool-status-noise']

# Task: render an indented 5H/WK/per-model detail line per account

Part of epic `expand-monitor-display`. Label: `required_provider:any`.
Depends on `display-model-windows` (data) and `monitor-pool-status-noise` (both edit `render_table.go` — land after it to avoid a merge conflict).

## Context
`internal/monitor/render_table.go` renders one row per profile in a bordered 75-col box (default `Width` 75 → `innerWidth` 73; floor 40, lines 15-26). Data rows (lines 60-78) are `"  %s%-20s %s %s | %s"` = indicator, 20-char name, 20-char bar (`progressBar()`, render.go:183), percent, status. `writeLine` (render_table.go:103) truncates+pads to innerWidth so content can never break the box. `formatDuration` (render.go:198-214) currently maxes at hours ("5h 3m"), no day unit. Data source: `ProfileState.Usage *usage.UsageInfo` with `PrimaryWindow` (5H), `SecondaryWindow` (weekly), `ModelWindows` (from `display-model-windows`); each `*UsageWindow{UsedPercent int, ResetsAt time.Time}`.

## Spec
After the existing per-profile line (render_table.go:78), if `usageUnavailable(p.Usage) == ""` (fetch succeeded) AND at least one of PrimaryWindow / SecondaryWindow / a relevant model window is non-nil, write ONE detail line, indented 7 spaces, segments joined by `" | "`:
- `5H {UsedPercent}%` + optional ` (resets {dur})` from PrimaryWindow.
- `WK {UsedPercent}%` + optional ` (resets {dur})` from SecondaryWindow.
- Claude (`p.Provider=="claude"`): `FABLE {n}%` from `p.Usage.FindModelWindow("fable","")` — omitted entirely when nil (the confirmed reality today, per D4).
- Codex: `SPARK 5H {n}% WK {m}%` from `FindModelWindow("spark","5h")` / `("spark","weekly")` (also try substring `"codex-spark"` / `"bengalfox"`). If only one window exists, render just that one.

Rules:
1. **Graceful omission (D4)**: a nil window → NO segment (no placeholder, no dangling `|`). Zero segments → no detail line. Reset hint omitted when `ResetsAt.IsZero()` or already past.
2. **Width discipline**: build with reset hints; if `len(line) > innerWidth`, rebuild WITHOUT reset hints (percentages always win). writeLine truncation stays only as the width-40 safety net.
3. **Duration format**: add day support so weekly reads `2d4h` not `52h`. Extend `formatDuration` (render.go:198) — or add `formatDurationCompact` if changing it breaks cooldown tests: `>=24h` → `NdMh` (omit `Mh` when 0); `<1h` → `Nm`. Use `time.Until(w.ResetsAt)`.
4. **No redesign**: don't touch header, borders, provider grouping (lines 49-53), error rows, or the primary row format. `ShowEmoji=false` must not change the (constant) detail indent.

## Acceptance criteria
- [ ] A unit test at Width 75 with a fabricated MonitorState (claude: 5H 42% reset 1h12m, WK 63% reset 2d4h; codex: 34%/54% + Spark 5H 22% WK 41%) reproduces the exact detail-line strings and asserts every output line is exactly 75 chars.
- [ ] Omission cases: only PrimaryWindow → `5H 42% (resets 1h12m)` alone; no windows → no detail line; error row → no detail line; codex without Spark → `5H ... | WK ...` only.
- [ ] Width 40 renders without panic (40-char lines; truncation acceptable). `--no-emoji` alignment tested.
- [ ] Existing `internal/monitor/render_test.go` passes (update only expectations that legitimately gained a detail line). `go vet ./...` clean; no new deps.

## Security constraints
- Never log or print token values (at most first 8 chars + length in debug output).
- Read-only GETs against provider usage APIs; the only writes are vault credential updates performed inside `internal/refresh`.
- The monitor must never write to the live `~/.claude` / `~/.codex` credential files.



## BEAD caam-expand-monitor-display-gqds.3 — JSON/brief parity + regression tests for expanded monitor data

type=task priority=P2 parent=expand-monitor-display depends_on=['display-model-windows', 'display-detail-line']

# Task: keep JSON/brief formats coherent + lock behavior with tests

Part of epic `expand-monitor-display`. Label: `required_provider:any`.
Depends on `display-model-windows` and `display-detail-line`.

## Context
`caam monitor` supports table/brief/json/alerts. JSON: `encodeJSONState` (internal/monitor/render.go:111-152) embeds the whole `*usage.UsageInfo` as `jsonProfile.Usage` (render.go:61), so `ModelWindows` flows through automatically via `json:"model_windows,omitempty"` (usage.go:55). Brief (`render_brief.go`) prints one worst-profile percent per provider (capped ~80 chars) and — per minimal-lift — should NOT gain per-model data.

## Spec
1. **JSON** (likely no code change): add a test rendering a state whose `UsageInfo` has PrimaryWindow, SecondaryWindow, and `ModelWindows{"gpt-5.3-codex-spark/weekly": ...}` through the JSON renderer; assert output contains `model_windows` with the entry and both windows, and that a profile WITHOUT `ModelWindows` omits the key (verify `omitempty`). Add `omitempty` to any newly-touched field missing it.
2. **Brief**: assert byte-identical output before/after for a representative state (guards against coupling; `usagePercent` at alerts.go:110 must stay primary-window-based).
3. **Alerts**: no change; one smoke assertion that AlertRenderer output is unchanged with vs. without `ModelWindows`.
4. Do NOT rename existing JSON keys (`usage_percent`, `usage`, `health`, `pool_status`, render.go:57-67) — external consumers parse them.

## Acceptance criteria
- [ ] `go test ./internal/monitor/...` passes; new tests cover `model_windows` present-when-set / absent-when-nil, brief unchanged, alerts unchanged.
- [ ] No live API calls (fixtures only).

## Security constraints
- Never log or print token values (at most first 8 chars + length in debug output).
- Read-only GETs against provider usage APIs; the only writes are vault credential updates performed inside `internal/refresh`.
- The monitor must never write to the live `~/.claude` / `~/.codex` credential files.

