# OpenCode Auth Research for Claude Code Recovery

Date: 2026-07-03

Related beads: `caam-xbwf`, `caam-6sao.6`

## Summary

The reviewed OpenCode sources did not show a documented reusable Claude Code login automation flow for CAAM. Its documented provider flow is `/connect`, with provider credentials stored in `~/.local/share/opencode/auth.json`, and its Claude-related behavior is centered on API/provider authentication rather than driving Claude Code's terminal `/login` challenge flow.

The useful lessons for CAAM are around operator ergonomics and credential cache invalidation, not direct OAuth token reuse. CAAM should continue treating Claude Code as the authority for Claude subscription authentication and automate the official `/login` terminal flow where needed.

## Sources Reviewed

- OpenCode provider docs: https://opencode.ai/docs/providers/
- OpenCode repository: https://github.com/opencode-ai/opencode
- OpenCode issue on Claude Code OAuth request wiring: https://github.com/anomalyco/opencode/issues/417
- OpenCode issue on Claude Code credentials being rejected for non-Claude-Code use: https://github.com/anomalyco/opencode/issues/7456
- Community `opencode-claude-auth` plugin: https://github.com/griffinmartin/opencode-claude-auth
- `opencode-claude-auth` cache invalidation issue: https://github.com/griffinmartin/opencode-claude-auth/issues/219

## Findings

### 1. OpenCode's documented auth path is provider key connection, not Claude Code login automation

The OpenCode provider documentation says users add provider credentials with `/connect`; those credentials are stored in OpenCode's own auth file. The OpenAI section documents a ChatGPT Plus/Pro browser authentication option, but there is no equivalent documented Claude Code subscription login automation flow that CAAM can reuse.

Impact for CAAM:

- Do not model WezTerm recovery on OpenCode's provider connection flow.
- Keep CAAM's recovery model pane-centric: detect Claude Code state, inject `/login`, extract OAuth URLs/challenge codes, and resume the original pane.

### 2. Older OpenCode Claude Code token wiring is API replay, not terminal recovery

OpenCode issue #417 describes a request-path approach: Anthropic provider calls using a bearer access token, an OAuth beta header, and removal of API-key headers. That is a request transport technique, not a terminal login state machine.

Impact for CAAM:

- This is not a replacement for `/login`.
- Reusing this path would require CAAM to own Claude OAuth request semantics that are undocumented and have changed over time.
- CAAM docs already state that Claude Code refresh is unsupported externally; this research reinforces that decision.

### 3. Claude Code credentials may be rejected outside Claude Code

OpenCode issue #7456 reports a case where Claude Code credentials were rejected in non-Claude-Code use; treat them as unsupported for third-party API clients unless explicitly verified.

Impact for CAAM:

- CAAM should not promise that Claude Code subscription OAuth tokens can be used with third-party API clients.
- Error messages and docs should keep separating "switch Claude Code's own auth files" from "use Claude Code tokens as Anthropic API credentials."
- Any future "OpenCode integration" should be explicit about whether it is switching OpenCode API keys, switching Claude Code homes, or driving Claude Code's own login UI.

### 4. The community plugin has useful credential-source and cache-invalidation lessons

The `opencode-claude-auth` plugin reads Claude Code credentials from macOS Keychain first and `~/.claude/.credentials.json` as a fallback, caches credentials briefly, and can use `opencode auth login` as a switcher on macOS. Its issue #219 is especially relevant: reading a credential file once at startup can miss external account switches and later write stale tokens back over a newer account.

Impact for CAAM:

- Any CAAM daemon or recovery worker that caches credential identity must re-read or invalidate on file change before writing auth files.
- Recovery should record the credential source used for decisions, such as `keychain`, `credentials_file`, or `vault_profile`.
- A short TTL is not enough by itself; external account switches need explicit file freshness checks before write-back.
- Tests should include "external credential file changed while worker is running" scenarios before any feature writes refreshed Claude credentials.

## Recommendations for CAAM

Adopt:

- Clear state names and operator-visible transitions similar to CAAM's existing WezTerm recovery states.
- Credential-source precedence in docs and diagnostics.
- Cache invalidation tests for any long-lived recovery process that reads auth files.
- A conservative "unsupported externally" message when users try to treat Claude Code credentials as generic Anthropic API credentials.

Avoid:

- Replaying Claude Code OAuth tokens directly against Anthropic APIs as a CAAM core feature.
- Writing refreshed Claude credentials from a long-lived in-memory copy without first checking the on-disk file.
- Claiming OpenCode solves Claude Code login automation; the relevant OpenCode material is provider auth and request routing.

## Mapping to Existing Work

No immediate code changes are recommended by this research alone.

Existing recovery work should use these findings as acceptance checks:

- WezTerm recovery tests should keep asserting pane-state transitions rather than provider API behavior.
- Any future auth daemon that writes Claude credentials needs a stale-cache regression test.
- Docs for Claude refresh should continue pointing users to Claude Code's `/login`, not an external CAAM refresh path.
