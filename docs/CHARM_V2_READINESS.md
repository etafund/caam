# Charm v2 Readiness

Bead: `caam-l19o.1.33`

Last reviewed: 2026-07-03

## Summary

`caam` is still on the v1-era Charm stack. A Charm v2 upgrade is now a real
major-version migration, not just prerelease tracking: official Charm release
pages list stable v2 releases for Bubble Tea, Lip Gloss, and Bubbles. The safe
path is to keep the current pins on `main`, build a separate compatibility
branch or allow-failure CI lane against v2, and migrate the TUI APIs in one
coordinated pass.

The repository now has a non-blocking compatibility workflow at
`.github/workflows/charm-v2-compat.yml`. It sets `TUI_CHARM_V2=1`, copies the
repository into the runner's temp directory, rewrites only the temporary copy's
Charm imports to v2 module paths, and runs a compile-only TUI test. Failures are
expected and do not block normal CI until the migration branch is ready.

## Current Dependency State

From `go.mod` and `go.sum`:

| Module | Current direct version | Notes |
| --- | --- | --- |
| `github.com/charmbracelet/bubbletea` | `v1.2.4` | Direct TUI framework dependency. |
| `github.com/charmbracelet/bubbles` | `v0.20.0` | Direct component dependency. |
| `github.com/charmbracelet/lipgloss` | `v1.1.1-0.20250404203927-76690c660834` | Direct pseudo-version newer than `v1.1.0`, but still v1 import path. |
| `github.com/charmbracelet/x/ansi` | `v0.8.0` | Direct ANSI helper dependency used by tests. |
| `github.com/charmbracelet/glamour` | `v0.10.0` | Indirect; also Charm-owned. |
| `github.com/charmbracelet/colorprofile` | `v0.2.3-0.20250311203215-f60798e515dc` | Indirect, relevant to v2 color handling. |
| `github.com/charmbracelet/harmonica` | `v0.2.0` | Indirect. |
| `github.com/charmbracelet/x/cellbuf` | `v0.0.13` | Indirect Bubble Tea rendering dependency. |
| `github.com/charmbracelet/x/exp/slice` | `v0.0.0-20250327172914-2fdc97757edf` | Indirect. |
| `github.com/charmbracelet/x/term` | `v0.2.1` | Indirect. |

Official upstream state verified from Charm GitHub release pages:

| Module | Current upstream v2 line observed | Important upstream note |
| --- | --- | --- |
| Bubble Tea | `v2.0.8` listed as latest | v2 uses `charm.land/bubbletea/v2`, introduces declarative `tea.View`, splits key, paste, and mouse messages, and changes key fields. |
| Lip Gloss | `v2.0.5` listed as latest | v2 uses `charm.land/lipgloss/v2`, makes Bubble Tea responsible for terminal I/O/color context, and removes automatic adaptive-color behavior from old-style use. |
| Bubbles | `v2.1.0` listed as latest | v2 uses `charm.land/bubbles/v2` and all subpackages move with it. Several components changed constructors, fields, styles, and light/dark style selection. |

## Repo Surface Area

Read-only inspection found this Charm usage:

| Area | Files observed | Migration sensitivity |
| --- | ---: | --- |
| Bubble Tea imports | 13 Go files under `internal/tui` | High. The main `Model` implements the v1 `tea.Model` shape and tests construct v1 messages directly. |
| Bubbles imports | 8 Go files under `internal/tui` | Medium to high. Current code uses `spinner`, `progress`, `key`, and `textinput`. |
| Lip Gloss imports | 19 Go files under `internal/tui` | High visual-regression risk. Layout tests rely heavily on rendered width and ANSI stripping. |
| `x/ansi` imports | TUI tests only | Medium. Keep as a pinned helper unless v2 upgrades pull a required newer version. |

Current code patterns that will need review:

- `internal/tui/model.go` returns `string` from `View()` and starts programs with
  `tea.NewProgram(m, tea.WithAltScreen())`.
- TUI update paths and tests use `tea.KeyMsg`, `tea.KeyRunes`, `msg.Type`, and
  `msg.Runes`.
- Dialog and interaction tests construct `tea.KeyMsg` values directly.
- `internal/tui/progress.go` sets `progress.Model.Width` directly.
- `internal/tui/dialog.go` sets `textinput.Model.Width` directly.
- Theme code uses `lipgloss.AdaptiveColor` and `lipgloss.NoColor`.
- Layout code uses `lipgloss.Width`, `Height`, `Place`, `JoinHorizontal`, and
  `JoinVertical` throughout; these are not automatically unsafe, but rendered
  output can change with v2 color and grapheme handling.

## Expected Migration Risks

### Bubble Tea

Risk level: high.

Primary changes to account for:

- Import path changes from `github.com/charmbracelet/bubbletea` to
  `charm.land/bubbletea/v2`.
- `View()` changes from returning `string` to returning `tea.View`. This affects
  the root TUI model and any helper/test that treats `View()` as a raw string.
- Alt screen, mouse mode, focus reporting, bracketed paste, cursor, window title,
  and related runtime features move toward declarative fields on `tea.View`.
- Key handling changes materially. v2 splits key messages into press/release
  forms, keeps `tea.KeyMsg` as a broader match type, and changes key internals
  from `Type`/`Runes` toward `Code`/`Text` and modifier data.
- Paste and mouse events are now more explicit message types. Even if `caam`
  does not currently handle mouse or paste deeply, tests should assert no
  accidental behavior changes.

Practical impact for `caam`:

- Start by adapting test helpers that synthesize key messages. If tests cannot
  create the v2 messages centrally, the migration will become noisy and brittle.
- Add a small translation helper for key events used in tests before touching
  the production key handling.
- Keep the root model's rendered text accessible through a helper so tests can
  compare content without depending on whether `View()` returns `string` or
  `tea.View`.

### Lip Gloss

Risk level: high for visual behavior, medium for compile behavior.

Primary changes to account for:

- Import path changes from `github.com/charmbracelet/lipgloss` to
  `charm.land/lipgloss/v2`.
- Background color detection and adaptive color selection are now more explicit.
  Official guidance is to request background color through Bubble Tea and handle
  `tea.BackgroundColorMsg` when used inside a Bubble Tea app.
- `lipgloss.AdaptiveColor` is not the right long-term abstraction for v2. Current
  `ThemeAuto` code depends on it, so theme initialization needs a v2 design.
- Standalone printing/downsampling uses Lip Gloss writer helpers in v2, but
  `caam` mostly renders inside Bubble Tea for this surface.

Practical impact for `caam`:

- Move theme construction toward explicit light/dark mode before the dependency
  bump. That can reduce the v2 diff and make `NO_COLOR`, `TERM=dumb`, and config
  overrides easier to reason about.
- Keep visual snapshot tests focused on semantic regions and width constraints,
  not exact ANSI byte sequences.
- Audit all `lipgloss.Width` based truncation after the upgrade, especially
  profile names, status bars, dialogs, and help text.

### Bubbles

Risk level: medium to high.

Primary changes to account for:

- Import path changes from `github.com/charmbracelet/bubbles/...` to
  `charm.land/bubbles/v2/...`.
- Light/dark default styles are explicit for components such as `help`, `list`,
  `textarea`, and `textinput`.
- `textinput.Model.Width` changes to methods in the v2 release notes. Current
  dialog code sets `ti.Width` directly.
- `progress.Model.Width` should be checked against v2 APIs during the compile
  spike.
- `spinner.Tick()` package-level usage is removed upstream, but `caam` already
  uses `Model.Tick()` for its wrapper.
- If `viewport`, `list`, or `table` are introduced during parallel TUI work, they
  should use v2-style constructors from the start in the compatibility branch.

Practical impact for `caam`:

- Treat `textinput`, `progress`, and `spinner` wrappers as the migration
  boundary. Do not spread raw Bubbles models farther into the TUI while v2 work
  is pending.
- Centralize component style initialization around the app theme so explicit
  light/dark style selection is testable.

## Blockers

- The codebase is pinned to v1-era import paths. Go's semantic import versioning
  means v2 is not a drop-in module version bump.
- The main TUI API currently assumes `View() string`; Bubble Tea v2 changes that
  return contract.
- Tests synthesize v1 key messages across several files. A v2 spike will fail
  noisily until key construction is centralized.
- Theme code depends on `lipgloss.AdaptiveColor`, while v2 expects explicit
  background-mode handling.
- The requested bead includes CI and feature-flag work, but this doc-only task
  cannot edit `.github`, `go.mod`, `internal/tui`, or `.beads`.

## Test Matrix

Use this matrix before merging any real v2 upgrade:

| Layer | Command or check | Required result |
| --- | --- | --- |
| Baseline v1 | `go test ./internal/tui ./cmd/caam/cmd` | Green on current pins. |
| Compatibility compile | `TUI_CHARM_V2=1` allow-failure workflow rewrites a temporary copy to `charm.land/*/v2`, then runs `go test ./internal/tui -run '^$' -count=1` | Compile failures are cataloged by package/component. |
| TUI unit tests | `go test ./internal/tui -run 'Test(NewTheme|Model|Dialog|Progress|Spinner|Help|Panels|E2E)'` | Green after API adaptation. |
| Visual layout | Existing TUI render tests plus targeted widths: 40x12, 80x24, 100x50, 140x40 | No overflow; dialogs and status bars stay within width. |
| Color modes | `NO_COLOR=1`, `TERM=dumb`, explicit light, explicit dark, auto/background query | Styles remain readable and tests do not depend on unavailable terminal I/O. |
| Input behavior | Synthetic key press, escape, enter, tab, slash search, question-mark help, ctrl-c/quit | v2 key model preserves current shortcuts. |
| Component wrappers | Spinner ticks, progress width/percent, textinput dialogs, command palette | Wrapper APIs hide Bubbles v2 changes from higher-level model code. |
| Runtime smoke | `caam tui` in a real terminal, with and without alt screen | Starts, resizes, accepts navigation, exits cleanly. |
| CI allow-failure | `.github/workflows/charm-v2-compat.yml` | Fails do not block normal PRs until the migration branch is ready. |

## Safe Upgrade Plan

1. Keep current Charm pins on `main`.
2. Create a short-lived compatibility branch. Do not combine this with unrelated
   TUI polish work.
3. Add a central test helper for key messages and rendered-view extraction while
   still on v1. This is the highest-value preparatory refactor.
4. Use the `TUI_CHARM_V2=1` compatibility workflow to collect compile failures.
   Keep it CI-only unless a short-lived migration branch needs build tags.
5. In the compatibility branch, switch imports together:
   `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, and
   `charm.land/lipgloss/v2`.
6. Update the root model to return `tea.View`, including alt-screen intent in
   the view rather than relying only on startup options.
7. Replace direct `tea.KeyMsg{Type, Runes}` usage in tests and handlers with v2
   key press helpers and `msg.String()`/keystroke matching where practical.
8. Replace `lipgloss.AdaptiveColor` based auto theme behavior with explicit
   light/dark mode selection driven by config, env, and optionally
   `tea.BackgroundColorMsg`.
9. Update Bubbles wrappers:
   text input width methods, progress width APIs, explicit component styles, and
   any new components introduced by concurrent TUI work.
10. Run the full test matrix above. Only promote the dependency bump after the
    v2 compatibility lane is green and visual regressions are reviewed.

## Decision

Do not upgrade Charm dependencies directly on `main` yet. The stable v2 line is
available upstream, but `caam` needs an API migration branch because its TUI and
tests use several v1-specific contracts. The lowest-risk next step is a
compile-only v2 branch or allow-failure CI job that records exact breakages
without disturbing current TUI work.

## References

- Bubble Tea releases: https://github.com/charmbracelet/bubbletea/releases
- Bubble Tea v2 overview and upgrade notes:
  https://github.com/charmbracelet/bubbletea/discussions/1374
- Lip Gloss releases: https://github.com/charmbracelet/lipgloss/releases
- Lip Gloss v2 overview and upgrade notes:
  https://github.com/charmbracelet/lipgloss/discussions/506
- Bubbles releases and v2 upgrade notes:
  https://github.com/charmbracelet/bubbles/releases
