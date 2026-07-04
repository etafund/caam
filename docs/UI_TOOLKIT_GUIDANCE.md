# UI Toolkit Guidance

Date: 2026-07-03

Related bead: `caam-l19o.1.35`

This document pins the UI toolkit choices for CAAM's CLI, TUI, optional Rust subtools, and web dashboard. It is a contribution guide for future UI work: prefer the stacks below, keep automation-friendly fallbacks, and do not introduce a second UI framework for the same surface without updating this document first.

## Baselines

| Surface | Toolkit | Current baseline |
| --- | --- | --- |
| Shell prompts | `gum` | Optional external dependency, detected by `caam doctor`; no required runtime dependency |
| Go TUI | Bubble Tea, Bubbles, Lip Gloss, Glamour | `bubbletea v1.2.4`, `bubbles v0.20.0`, `lipgloss v1.1.1-0.20250404203927-76690c660834`, `glamour v0.10.0` |
| Rust TUI subtools | ratatui + crossterm | No Rust TUI crate is pinned in this repo yet; future Rust subtools must pin an MSRV and ratatui major version explicitly |
| Web dashboard | Next.js, React, Tailwind, Lucide, Framer Motion | `next 16.2.10`, `react 19.2.7`, `tailwindcss 4.3.2`, `lucide-react 1.23.0`, `framer-motion 12.42.2` |

## Shell UX With Gum

`gum` is optional polish, not a scripting dependency.

Rules:

- Use `gum` only when stdin and the prompt output stream are TTYs; confirmations render prompts on stderr so stdout can stay machine-readable.
- Always provide a plain-text fallback with the same choices and defaults.
- Respect `NO_COLOR`, `TERM=dumb`, `CAAM_NO_TUI`, and `NO_TUI`.
- Never use `gum` for `--json`, `--plain`, CI, `TERM=dumb`, `CAAM_NO_TUI`, `NO_TUI`, or non-interactive paths.
- In `--json` mode, require explicit confirmation-skip flags such as `--force` or `--yes`; do not emit prompts alongside machine output.
- Keep prompt text stable enough for documentation and tests.
- Do not require users to install `gum`; `caam doctor` should report it as optional.

Existing reference points:

- Optional dependency detection: `cmd/caam/cmd/doctor.go`
- TUI preference commands and non-TUI toggles: `cmd/caam/cmd/config.go`

## Go TUI

The primary CAAM TUI is Bubble Tea with Lip Gloss styling. Keep this as the only Go TUI stack.

Current pins are in `go.mod`:

- `github.com/charmbracelet/bubbletea v1.2.4`
- `github.com/charmbracelet/bubbles v0.20.0`
- `github.com/charmbracelet/lipgloss v1.1.1-0.20250404203927-76690c660834`
- `github.com/charmbracelet/glamour v0.10.0` through the help renderer

Rules:

- Centralize visual tokens in `internal/tui/styles.go`.
- Keep layout sizing deterministic and covered by tests before adding new panels.
- Use `lipgloss.Width` or `ansi.Strip` in tests when asserting terminal cell widths.
- Respect `NO_COLOR`, `TERM=dumb`, high contrast, reduced motion, and density preferences.
- Keep debug output out of stdout; TUI diagnostics belong in logs or explicit debug overlays.
- Avoid introducing another Go TUI framework.

Charm v2 migration policy:

- Stay on Bubble Tea v1/Lip Gloss v1 until a planned migration bead updates the pins and tests together.
- Before upgrading, run the full TUI test suite and add focused compatibility tests for `tea.Model`, command batching, mouse events, width calculations, Glamour rendering, and Lip Gloss style output.
- Do not mix v1 and v2 idioms in new code.

## Rust TUI Subtools

CAAM is a Go project today. If a future helper is written in Rust and needs a TUI, use ratatui with crossterm unless there is a concrete reason to diverge.

Rules for any future Rust TUI crate:

- Declare `rust-version` in `Cargo.toml`; that value is the MSRV.
- Use Rust edition 2024 only when the declared MSRV supports it in the build fleet; otherwise use edition 2021.
- Pin ratatui and crossterm with explicit major versions, and document the chosen MSRV in the crate README.
- Keep terminal rendering covered by snapshot tests or deterministic string tests.
- Keep non-interactive `--json` or `--plain` output separate from the TUI.
- Do not add ratatui to this repo until there is an actual Rust subtool.

## Web Dashboard

The dashboard lives under `web/dashboard` and uses the package pins in `web/dashboard/package.json`:

- `next 16.2.10`
- `react 19.2.7`
- `react-dom 19.2.7`
- `tailwindcss 4.3.2`
- `lucide-react 1.23.0`
- `framer-motion 12.42.2`
- Node `>=22.0.0`, pnpm `>=10.0.0`, package manager `pnpm@10.23.0`

Rules:

- Use Lucide icons for tool actions instead of handmade icon SVGs.
- Keep Tailwind design tokens centralized; avoid one-off color palettes in page code.
- Use Framer Motion for meaningful state transitions only, and respect reduced-motion preferences.
- Keep the app useful on the first screen; avoid marketing-only landing pages for internal tools.
- Add tests for new component behavior with Vitest and Playwright where user flow matters.
- Keep dependency security visible through `pnpm audit`, GitHub advisories, or a documented equivalent.

## Update Cadence

- Patch security releases promptly when they affect Next.js, React, browser automation, terminal rendering, or auth-related UI.
- Batch non-security UI library updates into explicit maintenance beads with focused before/after screenshots or snapshots.
- Update this document in the same change as any major framework migration.
- Run the relevant test slice before closing UI work:
  - Go TUI: `go test ./internal/tui`
  - Web dashboard: `cd web/dashboard && pnpm lint && pnpm typecheck && pnpm test`
  - Rust TUI subtools, if added: `cargo test` plus snapshot tests

## Migration Notes

- A Bubble Tea/Lip Gloss v2 migration should be all-at-once for the Go TUI, with layout snapshots and interaction tests updated in the same bead.
- A Next/React major update should include package pins, lockfile updates, a production build, lint/typecheck/test output, and one Playwright smoke test.
- A future ratatui helper should start with a minimal crate, an explicit MSRV, and a snapshot harness before adding interactive features.
