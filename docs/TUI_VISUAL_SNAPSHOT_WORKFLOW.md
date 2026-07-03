# TUI Visual Snapshot Workflow

This workflow captures sanitized CAAM TUI videos and static screenshots for QA,
review, and demo material. It is optional tooling and is not part of the normal
test suite.

## Prerequisites

- Build CAAM first, or pass an explicit launch command:
  - `make build`
  - `bash scripts/tui-snapshots.sh --binary ./caam`
  - `bash scripts/tui-snapshots.sh --command 'go run ./cmd/caam'`
- Install `vhs` from Charmbracelet for actual captures.
- `freeze` is optional. The script reports whether it is available, but the
  interactive TUI screenshots are produced with VHS `Screenshot` commands.

Dry-run mode does not require `vhs` or `freeze`:

```bash
bash scripts/tui-snapshots.sh --dry-run
```

## What Gets Captured

The script supports these flows:

| Flow | Keys driven by VHS | Purpose |
| ---- | ------------------ | ------- |
| `main` | none | Default TUI list/dashboard view |
| `search` | `/`, `codex` | Search input and filtered state |
| `help` | `?` | Help screen |
| `sync` | `S` | Sync panel |
| `usage` | `u` | Usage panel |
| `palette` | `Ctrl+P` | Command palette overlay |

Run a small subset while iterating:

```bash
bash scripts/tui-snapshots.sh --binary ./caam --flows main,search,help
```

Run one flow in MP4 format:

```bash
bash scripts/tui-snapshots.sh --binary ./caam --flow usage --format mp4
```

## Sanitization

The capture script does not read your real CAAM, Claude, Codex, or Gemini
credentials. It runs VHS with isolated environment variables:

- `HOME`
- `CAAM_HOME`
- `XDG_CONFIG_HOME`
- `XDG_DATA_HOME`
- `XDG_CACHE_HOME`
- `CODEX_HOME`
- `GEMINI_HOME`
- `CLAUDE_CONFIG_DIR`

It also sets common AI-provider API key variables to empty strings inside the
VHS tape:

- `ANTHROPIC_API_KEY`
- `CODEX_API_KEY`
- `GEMINI_API_KEY`
- `GOOGLE_API_KEY`
- `OPENAI_API_KEY`

Before capture, it creates a tiny demo vault under the artifact directory with
fake `example.invalid` accounts and `redacted-demo-token` placeholder strings.
Those files exist only to make the TUI non-empty. They are not usable
credentials.

Artifact directories are never reused if they already contain files. This keeps
old captures intact and prevents accidental overwrites.

## Generated Artifacts

By default, artifacts are written outside the repository under:

```text
/tmp/caam-tui-snapshots/<timestamp>_<pid>/
```

Each flow gets:

```text
<flow>/capture.tape
<flow>/capture.gif
<flow>/snapshot.png
```

The run also writes:

```text
run_summary.txt
runtime/
```

`runtime/` is the isolated HOME/CAAM state used by the capture. Keep it with
the screenshots when debugging a visual regression because it explains what
data was on screen.

## CI Usage

This should be an explicit CI job or a manual workflow, not a default gate for
ordinary Go tests. A typical CI step is:

```bash
make build
bash scripts/tui-snapshots.sh \
  --binary ./caam \
  --flows main,help \
  --output-dir "${RUNNER_TEMP:-/tmp}/caam-tui-snapshots"
```

If `vhs` is missing, the script fails before creating artifacts with a clear
message. For dependency checks or lint-only jobs, use:

```bash
bash scripts/tui-snapshots.sh --dry-run --flows main,help
```

## Validation

Before committing changes to the workflow:

```bash
bash -n scripts/tui-snapshots.sh
bash scripts/tui-snapshots.sh --help
bash scripts/tui-snapshots.sh --dry-run --flows main,search
```
