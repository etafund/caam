# caam / codex account-switching — operations runbook

**Owner:** saumil@feta.fund · **Box:** gigaserver ACFS VPS · **Last major update:** 2026-06-22

This runbook supersedes the narrative in `../caam-debug-2026-06-19/NOTES.md`. Read this first.

---

## 0. The one-paragraph truth

`caam` is **not** broken. `caam next/switch/activate codex` rewrites `$CODEX_HOME/auth.json` correctly every time. Two *other* things make account switching look broken and can brick accounts:

1. **codex's persistent `app-server` daemon caches the account in memory** → new `cod` sessions ignore the file caam just wrote. (This is the "stuck on saumil" symptom.)
2. **Multiple codex processes sharing/copying one account's *rotating* refresh-token** → reuse-detection revokes the whole token family server-side. (This is the "account bricked / please log out and sign in again" symptom — the same hazard as caam issue #19.)

The durable fix is operational, not a code change: **daemon-free switching home + single-holder-per-account discipline.**

---

## 1. Problem A — "switch doesn't take" (daemon cache)

**Symptom:** `caam next codex` says `Switched codex to 'arthur'`, `~/.codex/auth.json` *is* arthur on disk, but `cod` → `/status` still shows the old account.

**Tell:** `/status` shows a line `Remote: unix:///home/ubuntu/.codex/app-server-control/app-server-control.sock`. That means this `cod` is a **thin client of a long-lived `codex app-server` daemon** which loaded the account once at startup and never re-reads `auth.json`.

**Diagnose:**
```bash
ls -l ~/.codex/app-server-control/app-server-control.sock   # exists => daemon present
pgrep -af 'codex app-server'                                 # the daemon process(es)
ps -o etime= -p <pid>                                        # how long it has cached the account
```

**Fix (pick one):**
- **Kill the daemon** so the next `cod` runs standalone (fresh-read):
  ```bash
  pkill -f 'codex app-server --listen'    # socket disappears; new cod reads auth.json fresh
  ```
  (`codex app-server daemon restart` / `codex remote-control stop` only work on a *standalone-installer* codex; the bun-installed codex here is "unmanaged", so kill is the lever.)
- **Or run switchable cod from a daemon-free `CODEX_HOME`** (recommended, see §4). A plain `cod` in a home that has no `app-server-control/` socket is standalone and always reads `auth.json` fresh.

The daemon comes back only if something re-bootstraps it (`codex app-server daemon bootstrap`, `codex remote-control start`, or a codex **remote-control-over-SSH** session). It does **not** auto-respawn from a plain `cod`, and there is no systemd unit / shell hook starting it here.

---

## 2. Problem B — account bricking (refresh-token reuse)

**Symptom:** `401 token_revoked` / `refresh_token_invalidated` / "Please log out and sign in again", even though the JWT `exp` claims look valid. Once a family is revoked, **no client-side fix works — you must re-auth** (§3).

**Mechanism:** codex rotates the refresh-token on every refresh. If two processes refresh from the **same** refresh-token (because they hold divergent **copies** of one account's `auth.json`), the IdP revokes the entire family. The caam #19 `codexLiveIsNewer` guard prevents *caam* from replaying a stale token on a same-account restore, but it **cannot** see other codex processes. Amplifier: an **expired `id_token`** (with a still-valid `access_token`) forces every fresh `codex` start to refresh.

**How we bricked accounts (avoid this):**
- running several `CODEX_HOME=/tmp/x codex exec` canaries off **copies** of one account's `auth.json`;
- a long-lived `app-server` daemon holding the same family while copies were exercised elsewhere;
- two independent refreshes raced → family revoked.

**Rules to never brick again:**
- One account = **one in-process holder** of its live token. Either a single `app-server` daemon (swarm) or one interactive `cod` at a time.
- Do **not** fan one login's `auth.json` into multiple `CODEX_HOME`s that run codex concurrently. Per-pane isolation requires **distinct logins**, not copies.
- Don't run ad-hoc `codex exec` canaries against a live/rotating token to "test" it.

---

## 3. Recovery procedure (revoked account → swarm restored)  ✅ used 2026-06-22

1. **Re-auth** (only the human can do the browser step):
   ```bash
   CODEX_HOME=/home/ubuntu/.codex-relogin codex login --device-auth
   # open https://auth.openai.com/codex/device, enter the one-time code, sign in as the right account
   ```
   (Plain `codex login` starts a localhost:1455 flow unreachable on this headless box; use `--device-auth`.)
2. **Capture → vault, deploy → global:**
   ```bash
   CODEX_HOME=/home/ubuntu/.codex-relogin caam backup codex <account>
   caam activate codex <account>          # writes fresh token to global ~/.codex
   ```
3. **Kill the stale daemon** so the fresh token is what new cod loads:
   ```bash
   pkill -f 'codex app-server --listen'
   ```
4. **Resume each swarm pane without losing context** (session id from the running codex's open rollout fd):
   ```bash
   # per pane: find the codex pid under the pane, then:
   ls -l /proc/<codex_pid>/fd | grep -o 'rollout-[^ ]*\.jsonl'   # -> UUID is the session id
   # in the pane: Esc, C-c C-c (quit), then:
   cod resume <session-uuid>
   ```
   Verify each pane's status bar shows `5h … left · weekly … left` (alive) and not "log out".

A helper that maps all `fetaos--spine` panes → session ids lives at
`../<scratch>/spine_sessions.json` style; the open-fd method is in this file and in the prior NOTES.

---

## 4. The persistent fix / correct operating model

| Use case | Home | Daemon? | Why |
|---|---|---|---|
| **Your interactive account-hopping** | dedicated `~/.codex-switch` (built 2026-06-22) | none | `caam next codex` + `cod` always maps correctly; zero impact on swarm/remote |
| **The shared swarm (one account)** | global `~/.codex` | one daemon = single holder (or accept standalone) | single refresher avoids reuse revocation; doesn't need to switch |
| **codex remote-control over Tailscale SSH** | global `~/.codex` | its own daemon | keep it off the home you switch on |

`~/.codex-switch` = copy of `~/.codex/config.toml` + its own `auth.json` (caam-managed) + `skills` symlink; **no** `sessions` symlink (that triggers a heavy state-db backfill) and **no** `app-server-control/` daemon. To use it:
```bash
export CODEX_HOME=/home/ubuntu/.codex-switch
caam next codex        # switch
cod                    # new instance reads the switched token; /status shows the expected account
```

---

## 5. Upstream (filed 2026-06-22, by etafund)

- **caam #21** (NEW) — `caam next/switch/activate codex` silently no-ops for new `cod` when an `app-server` daemon is running; caam should detect the control socket and warn/offer reload. https://github.com/Dicklesworthstone/coding_agent_account_manager/issues/21
- **caam #19** (CLOSED, fixed) — added field comment: the freshness guard is necessary but can't stop multi-process/divergent-copy reuse. https://github.com/Dicklesworthstone/coding_agent_account_manager/issues/19
- **ntm #194** (CLOSED) — added field comment: rotation can be byte-correct yet not take effect (daemon cache); shared-global is itself the brick hazard; recommend per-pane `CODEX_HOME`. https://github.com/Dicklesworthstone/ntm/issues/194
- **Not filed on codex/OpenAI** (per owner instruction), though the daemon-cache + no-reload-on-change behavior is ultimately codex-side.

---

## 6. The #19 binary reconciler (still active, unrelated to the above)

`caam-mainstream-reconciler.sh` (cron daily 05:00) re-applies the upstream-main `#19` fix if `acfs update` downgrades caam to the no-fix `v0.1.11` release. Installed caam is currently `e366025` (has #19 + #20). **Keep `caam-localfix/` intact** until upstream cuts a release > v0.1.11 containing the fix — then the reconciler auto-retires (see `MARKER.json`).

---

## 7. Current state (2026-06-22 ~15:00 PT)

- saumil: re-authed, fresh, live on global `~/.codex` (5h 100% / weekly 79%). Vault updated.
- arthur: re-authed, valid, **weekly-capped until 6/24**. Vault updated.
- 10 `fetaos--spine` codex panes: resumed on saumil with context intact, 0 auth errors.
- Stale `app-server` daemon: killed (no respawn). Global `cod` = standalone fresh-read.
- `~/.codex-switch`: ready for daemon-free interactive switching (verified `caam next codex` → `cod` → `/status` shows the expected account, both directions).
