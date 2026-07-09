# PLAN: Add shallow-profile support for Codex CLI identities in caam

This plan describes how to implement GitHub issue #34:

https://github.com/Dicklesworthstone/coding_agent_account_manager/issues/34

Goal: extend `caam shallow-profile` and `caam shallow-spawn` so they support
Codex CLI identities in addition to the existing Claude Code shallow identities.

The desired behavior is not the same as `caam activate codex ...`, which swaps
the currently active auth file in place. It is also not the same as fully
isolated `caam profile add codex ...`, which creates a separate HOME-like
environment. A Codex shallow profile should create a per-identity shallow HOME
where only Codex auth-bearing and auth-policy files are real, while normal
developer HOME state remains shared through symlinks.

## Desired user experience

The following should work after implementation:

```sh
# One-time: save Codex accounts in the normal caam vault.
caam backup codex alice@example.com
caam backup codex bob@example.com

# Create shallow Codex identities.
caam shallow-profile create codex-alice --tool codex --from-vault codex/alice@example.com --json
caam shallow-profile create codex-bob --tool codex --from-vault codex/bob@example.com --json

# The tool can be inferred from --from-vault when the vault spec names codex.
caam shallow-profile create codex-alice --from-vault codex/alice@example.com --json

# Create from an arbitrary auth.json file.
caam shallow-profile create codex-alice --tool codex --from-file /path/to/auth.json --json

# Run concurrent Codex sessions with distinct auth files.
caam shallow-spawn codex-alice -- codex
caam shallow-spawn codex-bob -- codex

# Agent-friendly dry run / wrapper support.
caam shallow-spawn codex-alice --print-env
```

For a Codex shallow profile, `--print-env` should include at least:

```text
HOME=<shallow-home>
SHALLOW_PROFILE=<name>
CODEX_HOME=<shallow-home>/.codex
```

For backward compatibility, existing Claude shallow commands should continue to
work:

```sh
caam shallow-profile create alice --from-vault claude/alice@example.com
caam shallow-profile create bob --from-file /tmp/bob.credentials.json
caam shallow-spawn alice -- claude
```

## Current code shape

The current implementation is deliberately Claude-shaped.

Files to inspect first:

- `internal/shallow/shallow.go`
- `cmd/caam/cmd/shallow.go`
- `internal/provider/codex/codex.go`
- `internal/authfile/authfile.go`
- `internal/codexd/codexd.go`
- `cmd/caam/cmd/codex_daemon.go`

Important current assumptions:

- `internal/shallow/shallow.go` has package-level `realEntries`:
  - `.claude/.credentials.json`
  - `.claude/.credentials.lock`
  - `.claude.json`
  - `.caam-shallow.json`
- `internal/shallow/shallow.go` has package-level `realDirs` containing only
  `.claude`.
- `Manager.Create` always writes credentials to
  `<shallow-home>/.claude/.credentials.json`.
- `Manager.Create` always creates `.claude/.credentials.lock`.
- `Manager.Create` always creates or copies `.claude.json`.
- `cmd/caam/cmd/shallow.go` has `resolveVaultCredential`, which rejects every
  `--from-vault <tool>/<profile>` where `tool != "claude"`.
- `shallow-spawn` sets `HOME` and `SHALLOW_PROFILE`, and strips
  `CLAUDE_CONFIG_DIR`, but it does not set `CODEX_HOME`.

Useful existing Codex support:

- `internal/provider/codex/codex.go` already models Codex auth as
  `$CODEX_HOME/auth.json`.
- `codex.EnsureFileCredentialStore(home)` already creates or updates
  `config.toml` with `cli_auth_credentials_store = "file"`.
- `codex.Provider.Env` already sets both `CODEX_HOME` and `HOME` for fully
  isolated profiles.
- `internal/authfile/authfile.go` has `CodexAuthFiles()` with the authoritative
  Codex auth file path.
- Issue #21 is already represented by `internal/codexd` and
  `cmd/caam/cmd/codex_daemon.go`. That code warns about long-lived Codex
  daemons caching auth in-process after an auth-file swap.

## Implementation approach

Do not implement this as a one-off `if provider == "codex"` branch scattered
through the current Claude code. The correct shape is a small provider-aware
layout layer inside `internal/shallow`. Claude becomes one layout, Codex becomes
another layout, and future providers can be added without cloning the whole
manager.

The implementation should preserve the public behavior of existing Claude
shallow profiles. Old `.caam-shallow.json` files that do not have a provider
field should be treated as `provider=claude`.

## Phase 1: Introduce provider-aware shallow layouts

Edit `internal/shallow/shallow.go`.

Add a layout descriptor. The exact names can differ, but the implementation
needs to capture these concepts:

```go
type Layout struct {
    Provider string

    // Top-level directories that must be real directories in the shallow HOME.
    // Example: ".claude" or ".codex".
    RealDirs []string

    // Relative files that must be real files, not symlinks. Use slash-form
    // paths in definitions and convert through filepath.FromSlash at use sites.
    RealFiles []string

    // Real top-level directories where non-sensitive inner entries should be
    // symlinked back to the corresponding real HOME directory.
    InnerSymlinkRoots []string

    // Additional inner names to skip without creating a symlink. This is for
    // entries that are not necessarily created by caam but must not point back
    // to the real HOME.
    InnerSkip map[string][]string

    // The primary credential destination for this provider.
    CredentialRelPath string

    // Provider-specific real-file creation after the symlink farm is laid down.
    CreateManagedFiles func(m *Manager, home string, opts CreateOptions) error

    // Provider-specific environment mutations for shallow-spawn.
    SpawnEnv func(home, name string, env map[string]string)
}
```

Recommended concrete layouts:

```go
func ClaudeLayout() Layout {
    return Layout{
        Provider:          "claude",
        RealDirs:          []string{".claude"},
        RealFiles:         []string{".claude/.credentials.json", ".claude/.credentials.lock", ".claude.json"},
        InnerSymlinkRoots: []string{".claude"},
        CredentialRelPath: ".claude/.credentials.json",
        CreateManagedFiles: createClaudeManagedFiles,
        SpawnEnv: func(home, name string, env map[string]string) {
            env["HOME"] = home
            env["SHALLOW_PROFILE"] = name
            delete(env, "CLAUDE_CONFIG_DIR")
        },
    }
}

func CodexLayout() Layout {
    return Layout{
        Provider:          "codex",
        RealDirs:          []string{".codex"},
        RealFiles:         []string{".codex/auth.json", ".codex/config.toml"},
        InnerSymlinkRoots: []string{".codex"},
        InnerSkip: map[string][]string{
            ".codex": {"auth.json", "config.toml", "app-server-control"},
        },
        CredentialRelPath: ".codex/auth.json",
        CreateManagedFiles: createCodexManagedFiles,
        SpawnEnv: func(home, name string, env map[string]string) {
            env["HOME"] = home
            env["SHALLOW_PROFILE"] = name
            env["CODEX_HOME"] = filepath.Join(home, ".codex")
            delete(env, "CLAUDE_CONFIG_DIR")
        },
    }
}
```

Notes:

- Always include `.caam-shallow.json` in the real-file skip set, even if it is
  not in each layout's `RealFiles`.
- `app-server-control` must not be symlinked from the real `.codex`.
  A symlink to the real Codex daemon control socket could cause a shallow
  session to talk to a daemon that cached another identity. It is safer to skip
  it and let Codex create a shallow-profile-local control directory if needed.
- It is acceptable not to create `.codex/app-server-control` proactively.

Add provider normalization helpers:

```go
const (
    ProviderClaude = "claude"
    ProviderCodex  = "codex"
)

func NormalizeProvider(provider string) (string, error)
func SupportedProviders() []string
func LayoutForProvider(provider string) (Layout, error)
```

Behavior:

- Empty provider means `claude`, for backward compatibility inside the shallow
  package.
- Supported providers are exactly `claude` and `codex` for this issue.
- Errors should say something like:
  `unsupported shallow provider "gemini" (supported: claude, codex)`.

## Phase 2: Add provider to metadata

Edit `internal/shallow/shallow.go`.

Change metadata:

```go
type Meta struct {
    Name           string    `json:"name"`
    Provider       string    `json:"provider,omitempty"`
    CreatedAt      time.Time `json:"created_at"`
    CredentialFrom string    `json:"credential_from,omitempty"`
    RealHome       string    `json:"real_home"`
    Version        int       `json:"version"`
}
```

When writing new metadata:

- Set `Provider` to the normalized provider.
- Consider bumping `Version` from `1` to `2` for new provider-aware profiles.

When reading metadata:

- If `Provider` is empty, set it to `claude`.
- If `Provider` is unsupported, keep the raw value in `Meta.Provider` but make
  commands that need the layout return a clear unsupported-provider error.

This provides compatibility with existing Claude shallow profiles.

## Phase 3: Refactor Manager.Create around layouts

Edit `internal/shallow/shallow.go`.

Extend `CreateOptions`:

```go
type CreateOptions struct {
    Provider string

    // Existing field, now interpreted as "copy this file to the layout's
    // CredentialRelPath".
    CredentialSource string

    // Claude-only. Error if used with provider=codex.
    SourceClaudeJSON string

    CredentialFromLabel string
    Force bool
}
```

Update `Manager.Create`:

1. Normalize `opts.Provider`; default to `claude`.
2. Load `layout := LayoutForProvider(provider)`.
3. Create the profile home.
4. Create `layout.RealDirs` as real directories.
5. Populate top-level symlinks using a skip set derived from:
   - top-level components of `layout.RealDirs`
   - top-level components of `layout.RealFiles`
   - top-level component of `.caam-shallow.json`
   - `alwaysSkip`
   - the shallow base-dir nesting guard
6. Populate inner symlinks for each `layout.InnerSymlinkRoots`, skipping:
   - real files under that root
   - explicit names in `layout.InnerSkip[root]`
7. Call `layout.CreateManagedFiles(m, home, opts)`.
8. Write metadata with `Provider`.

Change these helper signatures:

```go
func (m *Manager) populateSymlinks(home string, layout Layout) error
func (m *Manager) populateInnerSymlinks(home string, layout Layout, root string) error
```

Be careful with path separators:

- Define layout paths as slash-form strings.
- Convert to OS paths with `filepath.FromSlash`.
- When extracting the top-level component, normalize to the OS path form first.

Keep `Manager.CredentialPath(name string)` working for existing tests if
possible. Either:

- Continue returning the Claude credential path for backward compatibility, and
  add a new provider-aware method such as `ManagedCredentialPath(name string)`,
  or
- Change it to load profile metadata and return the layout credential path.

The second option is cleaner, but it can break tests that call `CredentialPath`
before metadata exists. If in doubt, add a new provider-aware method and leave
the old method as a Claude-specific compatibility helper.

## Phase 4: Implement Claude managed-file creation

Move the current Claude-specific file writes out of `Manager.Create` into:

```go
func createClaudeManagedFiles(m *Manager, home string, opts CreateOptions) error
```

It should preserve current behavior:

- Copy `opts.CredentialSource` to `.claude/.credentials.json` with mode `0600`.
- If no credential source, write an empty `.claude/.credentials.json` with mode
  `0600`.
- Always create `.claude/.credentials.lock` as a real empty file with mode
  `0600`.
- For `.claude.json`:
  - if `opts.SourceClaudeJSON` is set, copy it with mode `0600`;
  - else if real HOME has `.claude.json`, copy it with mode `0600`;
  - else write `{}` with mode `0600`.

Add an explicit error if `opts.SourceClaudeJSON` is used with a non-Claude
provider. This can live either in `Manager.Create` after provider normalization
or in the Codex managed-file creator.

## Phase 5: Implement Codex managed-file creation

Edit `internal/shallow/shallow.go`.

Import the Codex provider package:

```go
codexprovider "github.com/Dicklesworthstone/coding_agent_account_manager/internal/provider/codex"
```

This should not create an import cycle because `internal/provider/codex` does
not import `internal/shallow`.

Add:

```go
func createCodexManagedFiles(m *Manager, home string, opts CreateOptions) error
```

Required behavior:

1. Destination directory is `<home>/.codex`.
2. Destination credential path is `<home>/.codex/auth.json`.
3. If `opts.CredentialSource` is set, copy it to `auth.json` with mode `0600`.
4. If no credential source is set, write an empty `auth.json` with mode `0600`.
   This mirrors the existing Claude empty-credential behavior.
5. Seed `config.toml`:
   - If real HOME has `~/.codex/config.toml`, copy it to
     `<home>/.codex/config.toml` with mode `0600`.
   - Then call `codexprovider.EnsureFileCredentialStore(<home>/.codex)`.
   - If real HOME has no config, just call
     `codexprovider.EnsureFileCredentialStore(<home>/.codex)`.
6. Do not symlink `.codex/auth.json`, `.codex/config.toml`, or
   `.codex/app-server-control` from the real HOME.

Why copy the real `config.toml` first:

- It preserves useful non-auth user settings.
- It still makes `config.toml` a real per-shallow-profile policy file.
- `EnsureFileCredentialStore` enforces the required
  `cli_auth_credentials_store = "file"` setting after the copy.

Error text should mention Codex-specific paths, for example:

- `copy codex auth.json: ...`
- `seed codex config.toml: ...`
- `configure codex credential store: ...`

## Phase 6: Make shallow-spawn provider-aware

Edit `cmd/caam/cmd/shallow.go`.

Current behavior:

- `--print-env` prints only `HOME` and `SHALLOW_PROFILE`.
- Actual exec environment sets only `HOME` and `SHALLOW_PROFILE`, then deletes
  `CLAUDE_CONFIG_DIR`.

New behavior:

1. Load the profile metadata through `mgr.Get(name)`.
2. Determine provider:
   - if `prof.Meta == nil` or `prof.Meta.Provider == ""`, use `claude`;
   - otherwise use `prof.Meta.Provider`.
3. Get the layout with `shallow.LayoutForProvider(provider)`.
4. Build an environment map from `os.Environ()`.
5. Call `layout.SpawnEnv(prof.Path, name, envMap)`.
6. For `--print-env`, print the provider-specific keys in stable order.

Recommended `--print-env` output order:

- `HOME`
- `SHALLOW_PROFILE`
- `CODEX_HOME` if present

For unsupported provider metadata, return:

```text
shallow profile "name" uses unsupported provider "x" (supported: claude, codex)
```

Codex invariant:

- `CODEX_HOME` must always be set to `<shallow-home>/.codex`.
- Any inherited `CODEX_HOME` must be overwritten.
- `CLAUDE_CONFIG_DIR` should still be removed. This is harmless for Codex and
  keeps shallow sessions from accidentally using a parent Claude override when
  users run mixed tooling from the same shell.

Do not call `checkCodexDaemon` from `shallow-spawn`.

Reason:

- `checkCodexDaemon` is designed for auth-file swapping in `activate` and
  `next`.
- Shallow Codex sessions avoid the real daemon by setting a distinct
  `CODEX_HOME`.
- The important guard here is not symlinking `.codex/app-server-control` from
  the real HOME and forcing `CODEX_HOME` to the shallow directory.

## Phase 7: Update shallow-profile create CLI

Edit `cmd/caam/cmd/shallow.go`.

Add a flag:

```go
shallowProfileCreateCmd.Flags().String("tool", "", "shallow provider/tool: claude or codex")
```

Update help text:

- Mention both Claude and Codex.
- Clarify that `--from-vault <tool>/<profile>` can infer the tool.
- Clarify that `--from-file` defaults to the existing Claude behavior unless
  `--tool codex` is supplied.

Recommended compatibility behavior:

- `caam shallow-profile create bob --from-file /tmp/bob.credentials.json`
  remains Claude, because this is existing behavior.
- `caam shallow-profile create bob --tool codex --from-file /tmp/auth.json`
  creates Codex.
- `caam shallow-profile create scratch` remains Claude, because this is
  existing behavior.
- `caam shallow-profile create scratch --tool codex` creates an empty Codex
  shallow profile.
- `caam shallow-profile create name --from-vault codex/alice` infers Codex.
- If both `--tool` and `--from-vault` are set and disagree, error:
  `--tool codex does not match --from-vault tool claude`.

Add helper logic:

```go
type shallowVaultRef struct {
    Tool string
    Profile string
}

func parseShallowVaultRef(spec string) (shallowVaultRef, error)
func inferShallowProvider(toolFlag, fromVault string) (string, error)
func resolveShallowVaultCredential(spec string) (provider, sourcePath, label string, err error)
```

Provider-specific vault mapping:

- Claude:
  - vault directory: `vault.ProfilePath("claude", profile)`
  - credential filename: `.credentials.json`
  - destination: `.claude/.credentials.json`
- Codex:
  - vault directory: `vault.ProfilePath("codex", profile)`
  - credential filename: `auth.json`
  - destination: `.codex/auth.json`

Use `vault.ProfilePath(tool, profile)` for consistency with existing code.

Error messages:

- For unsupported vault tool:
  `--from-vault tool "gemini" is not supported for shallow profiles (supported: claude, codex)`.
- For missing Codex auth:
  `vault profile codex/alice missing auth.json: ...`.
- For missing Claude auth:
  `vault profile claude/alice missing .credentials.json: ...`.

Update create output:

```go
type shallowCreateOutput struct {
    Success        bool     `json:"success"`
    Name           string   `json:"name"`
    Provider       string   `json:"provider"`
    Path           string   `json:"path"`
    CredentialFrom string   `json:"credential_from,omitempty"`
    ManagedFiles   []string `json:"managed_files,omitempty"`
    Error          string   `json:"error,omitempty"`
}
```

`ManagedFiles` can be the absolute paths of layout real files that caam
actively manages for this profile. Include at least:

- Claude:
  - `<home>/.claude/.credentials.json`
  - `<home>/.claude/.credentials.lock`
  - `<home>/.claude.json`
  - `<home>/.caam-shallow.json`
- Codex:
  - `<home>/.codex/auth.json`
  - `<home>/.codex/config.toml`
  - `<home>/.caam-shallow.json`

Update human output:

- Print `Provider: codex` or `Provider: claude`.
- For next steps:
  - Claude: `caam shallow-spawn <name> -- claude`
  - Codex: `caam shallow-spawn <name> -- codex`

## Phase 8: Update shallow-profile list JSON and human output

Edit `cmd/caam/cmd/shallow.go`.

Update list item:

```go
type shallowListItem struct {
    Name           string    `json:"name"`
    Provider       string    `json:"provider"`
    Path           string    `json:"path"`
    CredentialFrom string    `json:"credential_from,omitempty"`
    CreatedAt      time.Time `json:"created_at,omitempty"`
}
```

For metadata without provider, show `claude`.

Human output should add a provider column:

```text
NAME                    TOOL      CREDENTIALS                       CREATED
alice                   claude    vault:claude/alice                2026-06-28 12:34
codex-alice             codex     vault:codex/alice@example.com     2026-06-28 12:35
```

Update the empty-list hint to mention both:

```text
Create one with:
  caam shallow-profile create alice --from-vault claude/<profile>
  caam shallow-profile create codex-alice --from-vault codex/<profile>
```

## Phase 9: Update tests in internal/shallow

Edit `internal/shallow/shallow_test.go`.

Add Codex data to the fake real HOME helper:

- `.codex/auth.json` with placeholder content
- `.codex/config.toml` with a harmless existing setting
- `.codex/sessions/marker`
- `.codex/logs/marker`
- `.codex/app-server-control/marker` to prove it is not symlinked

Add tests:

1. `TestCreateClaudeLayoutStillWorks`
   - Existing behavior should remain green.
   - It is fine to adapt the existing `TestCreateBuildsSymlinkFarmAndRealAuthFiles`.

2. `TestCreateCodexLayoutFromCredentialSource`
   - Create with `CreateOptions{Provider: "codex", CredentialSource: src}`.
   - Assert `.codex` is a real directory.
   - Assert `.codex/auth.json` is a real file, not a symlink.
   - Assert auth contents match source.
   - Assert auth file mode is `0600`.
   - Assert `.codex/config.toml` is a real file, not a symlink.
   - Assert config contains `cli_auth_credentials_store = "file"`.
   - Assert `.codex/sessions` and `.codex/logs` are symlinks to real HOME.
   - Assert `.codex/app-server-control` is absent or real, but never a symlink
     to the real HOME.

3. `TestCreateCodexLayoutPreservesConfigThenEnforcesFileStore`
   - Real HOME config contains another setting, for example `model = "gpt-5"`.
   - After create, shallow config still contains that setting.
   - It also contains or updates `cli_auth_credentials_store = "file"`.

4. `TestCreateCodexLayoutEmptyAuth`
   - Create with `Provider: "codex"` and no credential source.
   - Assert `.codex/auth.json` exists, is real, and has mode `0600`.

5. `TestMetaRecordsProvider`
   - Create Codex.
   - Read `.caam-shallow.json`.
   - Assert `provider == "codex"`.
   - Assert `credential_from` and `real_home` are correct.

6. `TestReadMetaDefaultsMissingProviderToClaude`
   - Manually write old-style metadata without `provider`.
   - Call `readMeta`.
   - Assert `Provider == "claude"`.

7. `TestCreateRejectsUnsupportedProvider`
   - Create with `Provider: "gemini"`.
   - Assert error mentions supported providers.

8. `TestCreateProducesNoBrokenSymlinksForCodex`
   - Same as existing no-broken-symlinks test, but with Codex layout.

Keep existing tests for:

- default base dir precedence
- duplicate handling
- name validation
- list sorting
- delete safety
- nested base-dir guard

## Phase 10: Update tests in cmd/caam/cmd

Edit `cmd/caam/cmd/shallow_test.go`.

Update the test command builder:

- Add the new `--tool` flag to the cloned create command.

Add tests:

1. `TestShallowCreateCodexFromVault_JSON`
   - Use `shallowEnv`.
   - Stage a vault profile at:
     `<CAAM_HOME>/data/vault/codex/alice/auth.json`.
   - Run:
     `shallow-profile create codex-alice --from-vault codex/alice --json`.
   - Assert JSON `success: true`.
   - Assert JSON `provider: "codex"`.
   - Assert `credential_from: "vault:codex/alice"`.
   - Assert `<base>/codex-alice/.codex/auth.json` exists with copied content.
   - Assert `<base>/codex-alice/.codex/config.toml` contains
     `cli_auth_credentials_store = "file"`.

2. `TestShallowCreateCodexFromFile`
   - Write temp `auth.json`.
   - Run:
     `shallow-profile create codex-file --tool codex --from-file <path> --json`.
   - Assert auth copied to `.codex/auth.json`.

3. `TestShallowCreateVaultToolMismatch`
   - Run:
     `shallow-profile create bad --tool codex --from-vault claude/alice --json`.
   - Assert JSON error mentions mismatch.

4. `TestShallowCreateUnsupportedVaultTool`
   - Run:
     `shallow-profile create bad --from-vault gemini/alice --json`.
   - Assert JSON error mentions supported shallow providers.

5. `TestShallowSpawnPrintEnvCodex`
   - Create Codex shallow profile.
   - Run:
     `shallow-spawn codex-alice --print-env`.
   - Assert output contains:
     - `HOME=<base>/codex-alice`
     - `SHALLOW_PROFILE=codex-alice`
     - `CODEX_HOME=<base>/codex-alice/.codex`

6. `TestShallowSpawnExecsCorrectCodexEnv`
   - Set parent `CODEX_HOME` to a bogus path with `t.Setenv`.
   - Create Codex shallow profile.
   - Inject fake `spawnExec`.
   - Run `shallow-spawn codex-alice -- sh -c 'echo hi'`.
   - Assert env contains `CODEX_HOME=<base>/codex-alice/.codex`.
   - Assert env does not contain the bogus parent `CODEX_HOME`.

7. `TestShallowListJSONIncludesProvider`
   - Create one Claude and one Codex shallow profile.
   - Run `shallow-profile list --json`.
   - Assert each profile has the expected provider.

Update existing tests so they continue to pass for Claude.

Important: current tests use `sh` in `TestShallowSpawnExecsCorrectHome`.
That is fine for Unix CI. If adding cross-platform support is desired, use a
binary that exists on the test platform or test only env construction without
depending on `exec.LookPath("sh")`.

## Phase 11: Update docs and help text

Edit `README.md`.

Update the shallow profiles section:

- Explain that shallow profiles support Claude and Codex.
- Explain the difference between:
  - `caam activate codex <profile>`: swaps active auth file.
  - `caam profile add codex <profile>`: fully isolated profile.
  - `caam shallow-profile create --tool codex`: shared HOME with isolated
    Codex auth.

Add Codex examples:

```sh
caam backup codex alice@example.com
caam backup codex bob@example.com

caam shallow-profile create codex-alice --from-vault codex/alice@example.com
caam shallow-profile create codex-bob --from-vault codex/bob@example.com

caam shallow-spawn codex-alice -- codex
caam shallow-spawn codex-bob -- codex
```

Document Codex shallow layout:

```text
<shallow-home>/.codex/              real directory
<shallow-home>/.codex/auth.json     real file, mode 0600
<shallow-home>/.codex/config.toml   real file, mode 0600, enforces file auth store
<shallow-home>/.codex/sessions      symlink to real ~/.codex/sessions when present
<shallow-home>/.codex/logs          symlink to real ~/.codex/logs when present
```

Document the daemon caveat:

- A long-lived Codex daemon can cache auth in-process.
- Shallow Codex profiles set `CODEX_HOME` to the shallow `.codex` directory so
  they do not use the real HOME's Codex auth.
- `.codex/app-server-control` is intentionally not symlinked from the real HOME.
- If a user starts a daemon inside a shallow Codex profile, that daemon belongs
  to that shallow profile's `CODEX_HOME`.

Update Cobra help in `cmd/caam/cmd/shallow.go` to match the README.

## Phase 12: Format and validate

Run:

```sh
gofmt -w internal/shallow/shallow.go cmd/caam/cmd/shallow.go internal/shallow/shallow_test.go cmd/caam/cmd/shallow_test.go
```

Run focused tests:

```sh
go test ./internal/shallow ./cmd/caam/cmd ./internal/provider/codex
```

Then run a broader test pass:

```sh
go test ./...
```

If full `go test ./...` is too slow or has unrelated known failures, at minimum
record the focused test result and the reason the full suite was not run.

Manual smoke tests:

```sh
tmp="$(mktemp -d)"
export CAAM_HOME="$tmp/caam"
export CAAM_SHALLOW_HOMES_DIR="$tmp/shallow"

mkdir -p "$CAAM_HOME/data/vault/codex/alice"
printf '{"access_token":"fake"}\n' > "$CAAM_HOME/data/vault/codex/alice/auth.json"

caam shallow-profile create codex-alice --from-vault codex/alice --json
caam shallow-spawn codex-alice --print-env

test -f "$CAAM_SHALLOW_HOMES_DIR/codex-alice/.codex/auth.json"
test -f "$CAAM_SHALLOW_HOMES_DIR/codex-alice/.codex/config.toml"
test ! -L "$CAAM_SHALLOW_HOMES_DIR/codex-alice/.codex/auth.json"
test ! -L "$CAAM_SHALLOW_HOMES_DIR/codex-alice/.codex/config.toml"
```

Expected `--print-env` includes:

```text
HOME=<tmp>/shallow/codex-alice
SHALLOW_PROFILE=codex-alice
CODEX_HOME=<tmp>/shallow/codex-alice/.codex
```

## Acceptance checklist

- `caam shallow-profile create <name> --from-vault codex/<profile> --json`
  succeeds when the vault profile has `auth.json`.
- `caam shallow-profile create <name> --tool codex --from-file /path/to/auth.json --json`
  creates a Codex shallow identity with that file at `.codex/auth.json`.
- Codex `--print-env` includes `HOME`, `SHALLOW_PROFILE`, and `CODEX_HOME`.
- Inherited parent `CODEX_HOME` is overwritten for Codex shallow profiles.
- `.codex` in the shallow HOME is a real directory, not a symlink.
- `.codex/auth.json` is a real file, mode `0600`.
- `.codex/config.toml` is a real file, mode `0600`.
- `.codex/config.toml` enforces `cli_auth_credentials_store = "file"`.
- Safe existing `.codex` entries such as `sessions` or `logs` are symlinked
  into the shallow `.codex` directory when they exist.
- `.codex/app-server-control` is not symlinked from the real HOME.
- Missing passthrough sources are skipped without broken symlinks.
- Existing Claude shallow-profile behavior still works.
- Existing old metadata without `provider` is treated as Claude.
- Unsupported shallow provider errors list supported providers.
- JSON create/list output includes provider.
- README and command help document Codex shallow profiles.

## Likely review concerns

1. Backward compatibility
   - Existing Claude commands without `--tool` must continue to work.
   - Existing old metadata must default to Claude.

2. Codex daemon leakage
   - Do not symlink `.codex/app-server-control` from the real HOME.
   - Always set `CODEX_HOME` for Codex shallow-spawn.

3. Config handling
   - `config.toml` must be real, not a symlink.
   - Preserve existing config where possible, then enforce file credential store.

4. Error clarity
   - Do not imply all vault tools are supported by shallow profiles.
   - Provider mismatch errors should be explicit.

5. Test scope
   - Add focused tests rather than relying only on broad CLI coverage.
   - Keep Claude regression tests green.
