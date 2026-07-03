#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"

default_flows="main,search,help,sync,usage,palette"
flows_csv="${default_flows}"
output_dir=""
format="gif"
width="1200"
height="760"
font_size="16"
dry_run=0
binary=""
tui_command="${CAAM_TUI_SNAPSHOT_COMMAND:-}"

usage() {
  cat <<'EOF'
Capture sanitized CAAM TUI visual snapshots with VHS.

Usage:
  bash scripts/tui-snapshots.sh [options]

Options:
  --help, -h              Show this help.
  --dry-run               Print the capture plan without requiring VHS or writing files.
  --list-flows            List supported flow names.
  --flows LIST            Comma-separated flows to capture.
                          Default: main,search,help,sync,usage,palette
  --flow NAME             Capture a single flow. Alias for --flows NAME.
  --output-dir DIR        Artifact directory. Default: /tmp/caam-tui-snapshots/<timestamp>_<pid>
  --format FORMAT         VHS output format: gif, mp4, or webm. Default: gif
  --binary PATH           CAAM binary to run inside VHS.
  --command COMMAND       Shell command to launch the TUI inside VHS.
                          Overrides --binary. Example: --command 'go run ./cmd/caam'
  --width COLS            VHS viewport width in pixels. Default: 1200
  --height PX             VHS viewport height in pixels. Default: 760
  --font-size PX          VHS font size. Default: 16

Examples:
  bash scripts/tui-snapshots.sh --dry-run
  make build
  bash scripts/tui-snapshots.sh --binary ./caam --flows main,search,help
  bash scripts/tui-snapshots.sh --command 'go run ./cmd/caam' --flow usage --format mp4

Actual captures require the `vhs` command. Dry-run mode does not.
EOF
}

list_flows() {
  cat <<'EOF'
main     Start the TUI and capture the default dashboard/list view.
search   Open search with / and type a sanitized query.
help     Open the help screen with ?.
sync     Open the sync panel with S.
usage    Open the usage panel with u.
palette  Open the command palette overlay with Ctrl+P.
EOF
}

fail() {
  printf 'tui-snapshots: %s\n' "$*" >&2
  exit 1
}

is_uint() {
  [[ "${1:-}" =~ ^[0-9]+$ ]]
}

tape_quote() {
  local value="${1}"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"' "${value}"
}

shell_quote() {
  printf '%q' "$1"
}

parse_args() {
  while (($# > 0)); do
    case "$1" in
      --help|-h)
        usage
        exit 0
        ;;
      --dry-run)
        dry_run=1
        shift
        ;;
      --list-flows)
        list_flows
        exit 0
        ;;
      --flows)
        [[ $# -ge 2 ]] || fail "--flows requires a value"
        flows_csv="$2"
        shift 2
        ;;
      --flow)
        [[ $# -ge 2 ]] || fail "--flow requires a value"
        flows_csv="$2"
        shift 2
        ;;
      --output-dir)
        [[ $# -ge 2 ]] || fail "--output-dir requires a value"
        output_dir="$2"
        shift 2
        ;;
      --format)
        [[ $# -ge 2 ]] || fail "--format requires a value"
        format="$2"
        shift 2
        ;;
      --binary)
        [[ $# -ge 2 ]] || fail "--binary requires a value"
        binary="$2"
        shift 2
        ;;
      --command)
        [[ $# -ge 2 ]] || fail "--command requires a value"
        tui_command="$2"
        shift 2
        ;;
      --width)
        [[ $# -ge 2 ]] || fail "--width requires a value"
        width="$2"
        shift 2
        ;;
      --height)
        [[ $# -ge 2 ]] || fail "--height requires a value"
        height="$2"
        shift 2
        ;;
      --font-size)
        [[ $# -ge 2 ]] || fail "--font-size requires a value"
        font_size="$2"
        shift 2
        ;;
      *)
        fail "unknown option: $1"
        ;;
    esac
  done
}

validate_options() {
  case "${format}" in
    gif|mp4|webm) ;;
    *) fail "--format must be gif, mp4, or webm" ;;
  esac

  is_uint "${width}" || fail "--width must be a positive integer"
  is_uint "${height}" || fail "--height must be a positive integer"
  is_uint "${font_size}" || fail "--font-size must be a positive integer"

  if [[ -z "${output_dir}" ]]; then
    local stamp
    stamp="$(date -u +%Y%m%dT%H%M%SZ)"
    output_dir="${TMPDIR:-/tmp}/caam-tui-snapshots/${stamp}_$$"
  fi
}

split_flows() {
  local raw
  local flow
  IFS=',' read -r -a raw <<< "${flows_csv}"
  flows=()
  for flow in "${raw[@]}"; do
    flow="${flow//[[:space:]]/}"
    [[ -n "${flow}" ]] || continue
    case "${flow}" in
      main|search|help|sync|usage|palette)
        flows+=("${flow}")
        ;;
      *)
        fail "unknown flow ${flow}; run --list-flows"
        ;;
    esac
  done
  ((${#flows[@]} > 0)) || fail "no flows selected"
}

resolve_tui_command() {
  if [[ -n "${tui_command}" ]]; then
    printf '%s' "${tui_command}"
    return 0
  fi

  if [[ -n "${binary}" ]]; then
    local binary_path="${binary}"
    if [[ "${binary_path}" != /* ]]; then
      binary_path="${repo_root}/${binary_path#./}"
    fi
    [[ -x "${binary_path}" ]] || return 1
    shell_quote "${binary_path}"
    return 0
  fi

  if [[ -x "${repo_root}/caam" ]]; then
    shell_quote "${repo_root}/caam"
    return 0
  fi

  return 1
}

print_plan() {
  local command_status="${1}"

  printf 'CAAM TUI snapshot plan\n'
  printf '  repo:       %s\n' "${repo_root}"
  printf '  output:     %s\n' "${output_dir}"
  printf '  format:     %s\n' "${format}"
  printf '  viewport:   %sx%s @ %spx font\n' "${width}" "${height}" "${font_size}"
  printf '  flows:      %s\n' "${flows[*]}"
  printf '  vhs:        %s\n' "$(command -v vhs || printf 'not found')"
  printf '  freeze:     %s\n' "$(command -v freeze || printf 'not found (optional)')"
  printf '  command:    %s\n' "${command_status}"
  printf '\n'
  printf 'Dry-run only: no directories, tapes, screenshots, or videos were written.\n'
}

ensure_tools_for_capture() {
  command -v vhs >/dev/null 2>&1 || fail "vhs is required for capture; install charmbracelet/vhs or rerun with --dry-run"
}

ensure_output_dir() {
  if [[ -e "${output_dir}" && ! -d "${output_dir}" ]]; then
    fail "output path exists and is not a directory: ${output_dir}"
  fi
  if [[ -d "${output_dir}" ]]; then
    if [[ -n "$(find "${output_dir}" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
      fail "output directory already exists and is not empty: ${output_dir}"
    fi
  fi
  mkdir -p "${output_dir}"
}

write_json() {
  local path="$1"
  local body="$2"
  printf '%s\n' "${body}" > "${path}"
}

prepare_demo_state() {
  local caam_home="${output_dir}/runtime/caam-home"
  local vault="${caam_home}/data/vault"

  mkdir -p \
    "${vault}/codex/demo-codex" \
    "${vault}/claude/demo-claude" \
    "${vault}/gemini/demo-gemini" \
    "${output_dir}/runtime/home/.codex" \
    "${output_dir}/runtime/home/.gemini" \
    "${output_dir}/runtime/xdg-config/claude-code" \
    "${output_dir}/runtime/xdg-data" \
    "${output_dir}/runtime/xdg-cache"

  write_json "${vault}/codex/demo-codex/auth.json" \
    '{"account_id":"demo-codex","email":"demo-codex@example.invalid","access_token":"redacted-demo-token"}'
  write_json "${vault}/codex/demo-codex/meta.json" \
    '{"description":"Sanitized Codex demo profile for TUI snapshots"}'

  write_json "${vault}/claude/demo-claude/.credentials.json" \
    '{"claudeAiOauth":{"account":{"email":"demo-claude@example.invalid"},"accessToken":"redacted-demo-token"}}'
  write_json "${vault}/claude/demo-claude/meta.json" \
    '{"description":"Sanitized Claude demo profile for TUI snapshots"}'

  write_json "${vault}/gemini/demo-gemini/settings.json" \
    '{"selectedAuthType":"oauth-personal","account":"demo-gemini@example.invalid"}'
  write_json "${vault}/gemini/demo-gemini/meta.json" \
    '{"description":"Sanitized Gemini demo profile for TUI snapshots"}'
}

flow_actions() {
  local flow="$1"
  local screenshot="$2"

  case "${flow}" in
    main)
      cat <<EOF
Sleep 2s
Screenshot $(tape_quote "${screenshot}")
EOF
      ;;
    search)
      cat <<EOF
Sleep 2s
Type "/"
Sleep 300ms
Type "codex"
Sleep 1s
Screenshot $(tape_quote "${screenshot}")
EOF
      ;;
    help)
      cat <<EOF
Sleep 2s
Type "?"
Sleep 1s
Screenshot $(tape_quote "${screenshot}")
EOF
      ;;
    sync)
      cat <<EOF
Sleep 2s
Type "S"
Sleep 1s
Screenshot $(tape_quote "${screenshot}")
EOF
      ;;
    usage)
      cat <<EOF
Sleep 2s
Type "u"
Sleep 2s
Screenshot $(tape_quote "${screenshot}")
EOF
      ;;
    palette)
      cat <<EOF
Sleep 2s
Ctrl+P
Sleep 1s
Screenshot $(tape_quote "${screenshot}")
EOF
      ;;
  esac
}

write_tape() {
  local flow="$1"
  local command="$2"
  local flow_dir="${output_dir}/${flow}"
  local tape="${flow_dir}/capture.tape"
  local screenshot="${flow_dir}/snapshot.png"
  local video="${flow_dir}/capture.${format}"
  local setup_command

  mkdir -p "${flow_dir}"
  setup_command="cd $(shell_quote "${repo_root}") && clear"

  {
    printf 'Output %s\n' "$(tape_quote "${video}")"
    printf 'Set Shell bash\n'
    printf 'Set Width %s\n' "${width}"
    printf 'Set Height %s\n' "${height}"
    printf 'Set FontSize %s\n' "${font_size}"
    printf 'Set TypingSpeed 20ms\n'
    printf 'Env HOME %s\n' "$(tape_quote "${output_dir}/runtime/home")"
    printf 'Env CAAM_HOME %s\n' "$(tape_quote "${output_dir}/runtime/caam-home")"
    printf 'Env XDG_CONFIG_HOME %s\n' "$(tape_quote "${output_dir}/runtime/xdg-config")"
    printf 'Env XDG_DATA_HOME %s\n' "$(tape_quote "${output_dir}/runtime/xdg-data")"
    printf 'Env XDG_CACHE_HOME %s\n' "$(tape_quote "${output_dir}/runtime/xdg-cache")"
    printf 'Env CODEX_HOME %s\n' "$(tape_quote "${output_dir}/runtime/home/.codex")"
    printf 'Env GEMINI_HOME %s\n' "$(tape_quote "${output_dir}/runtime/home/.gemini")"
    printf 'Env CLAUDE_CONFIG_DIR %s\n' "$(tape_quote "${output_dir}/runtime/xdg-config/claude-code")"
    printf 'Env TERM "xterm-256color"\n'
    printf 'Env COLORTERM "truecolor"\n'
    printf 'Env CAAM_TUI_THEME "dark"\n'
    printf 'Env CAAM_TUI_REDUCED_MOTION "1"\n'
    printf 'Env CAAM_TUI_MOUSE "0"\n'
    printf 'Env CAAM_TUI_DENSITY "cozy"\n'
    printf 'Env ANTHROPIC_API_KEY ""\n'
    printf 'Env CODEX_API_KEY ""\n'
    printf 'Env GEMINI_API_KEY ""\n'
    printf 'Env GOOGLE_API_KEY ""\n'
    printf 'Env OPENAI_API_KEY ""\n'
    printf '\n'
    printf 'Hide\n'
    printf 'Type %s\n' "$(tape_quote "${setup_command}")"
    printf 'Enter\n'
    printf 'Sleep 500ms\n'
    printf 'Show\n'
    printf 'Type %s\n' "$(tape_quote "${command}")"
    printf 'Enter\n'
    flow_actions "${flow}" "${screenshot}"
    printf 'Ctrl+C\n'
    printf 'Sleep 1s\n'
  } > "${tape}"

  printf '%s\n' "${tape}"
}

write_summary() {
  local command="$1"
  local summary="${output_dir}/run_summary.txt"
  local flow

  {
    printf 'CAAM TUI visual snapshot run\n'
    printf 'Generated: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'Repo: %s\n' "${repo_root}"
    printf 'Command: %s\n' "${command}"
    printf 'Format: %s\n' "${format}"
    printf 'Viewport: %sx%s font=%s\n' "${width}" "${height}" "${font_size}"
    printf 'Sanitized runtime root: %s/runtime\n' "${output_dir}"
    printf '\n'
    printf 'Artifacts:\n'
    for flow in "${flows[@]}"; do
      printf '  %s/capture.tape\n' "${flow}"
      printf '  %s/capture.%s\n' "${flow}" "${format}"
      printf '  %s/snapshot.png\n' "${flow}"
    done
  } > "${summary}"
}

run_capture() {
  local command="$1"
  local flow
  local tape

  ensure_tools_for_capture
  ensure_output_dir
  prepare_demo_state

  for flow in "${flows[@]}"; do
    tape="$(write_tape "${flow}" "${command}")"
    printf 'Capturing %s with %s\n' "${flow}" "${tape}"
    vhs "${tape}"
  done

  write_summary "${command}"
  printf 'Snapshots written to %s\n' "${output_dir}"
}

main() {
  parse_args "$@"
  validate_options
  split_flows

  local resolved_command
  if ! resolved_command="$(resolve_tui_command)"; then
    if ((dry_run)); then
      resolved_command="<unresolved: build ./caam, pass --binary, or pass --command>"
    else
      fail "no TUI command found; run make build, pass --binary ./caam, or pass --command 'go run ./cmd/caam'"
    fi
  fi

  if ((dry_run)); then
    print_plan "${resolved_command}"
    return 0
  fi

  run_capture "${resolved_command}"
}

main "$@"
