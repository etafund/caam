#!/usr/bin/env bash
# caam-mainstream-reconciler.sh
# -----------------------------------------------------------------------------
# WHY THIS EXISTS
# The box auto-runs `acfs update` nightly (~4am timer) + Sunday weekly, which
# reinstalls caam from `releases/latest`. Until upstream cuts a release that
# contains the #19 fix, `releases/latest` == v0.1.11 (April) — WITHOUT the
# refresh-token-reuse account-bricking fix. So the nightly update silently
# DOWNGRADES caam and re-exposes the swarm to account bricking on rotation.
#
# This reconciler runs AFTER the nightly update and:
#   * if a caam release NEWER than v0.1.11 exists  -> assumes the fix shipped,
#     leaves the official release in place, removes itself, and notifies
#     (the box is now fully mainstream — goal achieved);
#   * else, if the installed caam is the no-fix v0.1.11 release (commit 7c604c4),
#     rebuilds the OFFICIAL main fix (commit 0bdd715+, `Fixes #19`) and reinstalls.
#
# Upstream: caam #19 (CLOSED, fix merged to main as 0bdd715, not yet released);
# related ntm #194. Runbook: etafund/gigaserver_setup docs/caam-ntm-localfix-runbook.md
# -----------------------------------------------------------------------------
set -uo pipefail
export PATH="/home/ubuntu/.local/bin:/usr/local/go/bin:/usr/bin:/bin:$PATH"

REPO="Dicklesworthstone/coding_agent_account_manager"
BASE_RELEASE="v0.1.11"                 # last release WITHOUT the #19 fix
NOFIX_COMMIT="7c604c4"                 # build hash of the v0.1.11 release
SRC="/data/projects/caam-localfix/caam-src"
CAAM="/home/ubuntu/.local/bin/caam"
LOG="/data/projects/caam-localfix/reconciler.log"
MARKER="/data/projects/caam-localfix/MARKER.json"
NOTIFY="/home/ubuntu/.acfs/scripts/lib/notifications.sh"

ts(){ date -Is 2>/dev/null; }
log(){ echo "$(ts) $*" >>"$LOG" 2>/dev/null; }
notify(){ [ -f "$NOTIFY" ] && bash "$NOTIFY" send "caam-localfix" "$1" >/dev/null 2>&1 || true; }

installed_ver="$($CAAM version 2>/dev/null | head -1)"
latest="$(gh release view -R "$REPO" --json tagName -q .tagName 2>/dev/null || echo "")"
log "check: latest_release='$latest' base=$BASE_RELEASE installed='$installed_ver'"

# (1) A newer release exists -> the fix is presumably shipped. Go fully mainstream + retire.
if [ -n "$latest" ] && [ "$latest" != "$BASE_RELEASE" ]; then
  log "RETIRE: caam release $latest > $BASE_RELEASE; leaving acfs's mainstream binary in place."
  notify "caam $latest released (should include the #19 fix). Local-fix reconciler retiring — box is now fully mainstream. Verify with: caam version"
  # remove our cron line + marker so the box runs pure upstream from here on
  ( crontab -l 2>/dev/null | grep -v 'caam-mainstream-reconciler.sh' | crontab - ) 2>/dev/null || true
  rm -f "$MARKER" 2>/dev/null || true
  exit 0
fi

# (2) Still no fixed release. If caam already has the fix (any main build, not the
#     no-fix release), we're done.
if ! printf '%s' "$installed_ver" | grep -q "$NOFIX_COMMIT"; then
  log "OK: installed caam is not the no-fix release; fix present. nothing to do."
  exit 0
fi

# (3) acfs downgraded caam to the no-fix v0.1.11 release -> re-apply official main fix.
log "REAPPLY: installed caam is the no-fix v0.1.11 release ($NOFIX_COMMIT); rebuilding official main."
if [ ! -d "$SRC/.git" ]; then
  git clone --depth 5 "https://github.com/$REPO.git" "$SRC" >>"$LOG" 2>&1
fi
git -C "$SRC" fetch --depth 5 origin main >>"$LOG" 2>&1
git -C "$SRC" checkout -f origin/main >>"$LOG" 2>&1
( cd "$SRC" && make build >>"$LOG" 2>&1 )

if [ -x "$SRC/caam" ] && "$SRC/caam" version >/dev/null 2>&1 && "$SRC/caam" status >/dev/null 2>&1; then
  if pgrep -x caam >/dev/null 2>&1; then
    log "DEFER: a caam process is running; will re-apply on the next run."
    exit 0
  fi
  cp "$SRC/caam" "$CAAM"
  log "REAPPLIED: $($CAAM version 2>/dev/null | head -1)"
  notify "Re-applied the caam #19 fix: acfs had downgraded caam to $BASE_RELEASE (no fix). Box protected again."
else
  log "ERROR: official-main rebuild failed; leaving existing caam untouched. Inspect $LOG."
  notify "caam-localfix reconciler: rebuild FAILED — caam may be on the no-fix $BASE_RELEASE. Check $LOG."
fi
