#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(cd -- "$ROOT_DIR/.." && pwd)
CASES_FILE="$ROOT_DIR/cases.tsv"
RUN_ID="${SMOKE_RUN_ID:-$(date +%Y%m%d-%H%M%S)-$$}"
OUTPUT_ROOT="$ROOT_DIR/out/$RUN_ID"
LATEST_LINK="$ROOT_DIR/out/latest"
SHARED_GRADLE_HOME="$OUTPUT_ROOT/gradle-user-home"
RESULTS_FILE="$OUTPUT_ROOT/results.tsv"
DEFAULT_TOOLS=(codex opencode)
AGENT_TIMEOUT_SECONDS="${AGENT_TIMEOUT_SECONDS:-600}"
CASE_FILTERS=()
TOOLS=()

usage() {
  cat <<'EOF'
Usage:
  smoke/run-agent-smoke.sh [--case CASE_ID] [--timeout SECONDS] [tool...]

Examples:
  smoke/run-agent-smoke.sh
  smoke/run-agent-smoke.sh opencode
  smoke/run-agent-smoke.sh --case springboot-tests opencode
  smoke/run-agent-smoke.sh --timeout 300 codex opencode

Notes:
  - Each case is still run as a separate agent invocation.
  - --case can be repeated to run a smaller subset.
  - --timeout applies per agent invocation; timed out cases are recorded as TIMEOUT.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --case)
      [[ $# -lt 2 ]] && { echo "missing value for --case" >&2; exit 2; }
      CASE_FILTERS+=("$2")
      shift 2
      ;;
    --timeout)
      [[ $# -lt 2 ]] && { echo "missing value for --timeout" >&2; exit 2; }
      AGENT_TIMEOUT_SECONDS="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      TOOLS+=("$1")
      shift
      ;;
  esac
done

if [[ ${#TOOLS[@]} -eq 0 ]]; then
  TOOLS=("${DEFAULT_TOOLS[@]}")
fi

mkdir -p "$OUTPUT_ROOT" "$SHARED_GRADLE_HOME"
ln -sfn "$OUTPUT_ROOT" "$LATEST_LINK"
: > "$RESULTS_FILE"
printf 'tool\tcase_id\tstatus\texit_code\tused_build_brief\tbuild_brief_count\texpectation\ttranscript\n' >> "$RESULTS_FILE"

export PATH="$HOME/.local/bin:$PATH"
export BUILD_BRIEF_GRADLE_USER_HOME="$SHARED_GRADLE_HOME"
export ANDROID_HOME="${ANDROID_HOME:-$HOME/Library/Android/sdk}"
export ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-$ANDROID_HOME}"

if ! command -v build-brief >/dev/null 2>&1; then
  echo "build-brief not found on PATH; install it first (expected in ~/.local/bin or PATH)." >&2
  exit 1
fi

if command -v gradle >/dev/null 2>&1; then
  gradle --gradle-user-home "$SHARED_GRADLE_HOME" --status > "$OUTPUT_ROOT/daemon-status-before.txt" 2>&1 || true
fi

case_selected() {
  local case_id=$1
  if [[ ${#CASE_FILTERS[@]} -eq 0 ]]; then
    return 0
  fi

  local selected
  for selected in "${CASE_FILTERS[@]}"; do
    if [[ "$selected" == "$case_id" ]]; then
      return 0
    fi
  done

  return 1
}

run_with_timeout() {
  local transcript=$1
  shift
  python3 - "$transcript" "$AGENT_TIMEOUT_SECONDS" "$@" <<'PY'
from pathlib import Path
import subprocess
import sys

transcript = Path(sys.argv[1])
timeout_seconds = int(sys.argv[2])
command = sys.argv[3:]

transcript.parent.mkdir(parents=True, exist_ok=True)
with transcript.open("w") as handle:
    try:
        completed = subprocess.run(
            command,
            stdin=subprocess.DEVNULL,
            stdout=handle,
            stderr=subprocess.STDOUT,
            timeout=timeout_seconds,
            check=False,
        )
    except subprocess.TimeoutExpired:
        handle.write(f"\n[build-brief smoke] timed out after {timeout_seconds}s\n")
        raise SystemExit(124)

raise SystemExit(completed.returncode)
PY
}

run_codex() {
  local project_dir=$1
  local prompt=$2
  local transcript=$3
  run_with_timeout "$transcript" \
    codex exec \
    --cd "$project_dir" \
    --skip-git-repo-check \
    --dangerously-bypass-approvals-and-sandbox \
    "$prompt"
}

run_opencode() {
  local project_dir=$1
  local prompt=$2
  local transcript=$3
  run_with_timeout "$transcript" \
    opencode run --dir "$project_dir" --print-logs "$prompt"
}

should_skip_case() {
  local skip_when=$1
  case "$skip_when" in
    "") return 1 ;;
    missing-android-sdk)
      [[ ! -d "$ANDROID_HOME/platforms" ]]
      return
      ;;
    *) return 1 ;;
  esac
}

count_build_brief_usage() {
  local transcript=$1
  python3 - "$transcript" <<'PY'
from pathlib import Path
import re
import sys

path = Path(sys.argv[1])
if not path.exists():
    print(0)
    raise SystemExit(0)

text = path.read_text(errors="ignore")
ansi = re.compile(r"\x1b\[[0-9;]*m")
patterns = (
    re.compile(r"^/bin/zsh -lc '.*\bbuild-brief\b"),
    re.compile(r"^\$ .*?\b(?:rtk\s+)?build-brief\b"),
)
count = 0
for line in text.splitlines():
    stripped = ansi.sub("", line).strip()
    if any(pattern.search(stripped) for pattern in patterns):
        count += 1
print(count)
PY
}

while IFS=$'\t' read -r case_id project_rel prompt_file expect_snippet skip_when; do
  [[ "$case_id" == "case_id" ]] && continue
  case_selected "$case_id" || continue
  project_dir="$ROOT_DIR/$project_rel"
  prompt=$(cat "$ROOT_DIR/$prompt_file")

  if should_skip_case "$skip_when"; then
    for tool in "${TOOLS[@]}"; do
      printf '%s\t%s\tSKIPPED\t-\t-\t0\t%s\t-\n' "$tool" "$case_id" "$skip_when" >> "$RESULTS_FILE"
    done
    continue
  fi

  for tool in "${TOOLS[@]}"; do
    transcript="$OUTPUT_ROOT/${tool}-${case_id}.log"
    printf 'RUN\t%s\t%s\n' "$tool" "$case_id"
    run_exit=0
    case "$tool" in
      codex)
        run_codex "$project_dir" "$prompt" "$transcript" || run_exit=$?
        ;;
      opencode)
        run_opencode "$project_dir" "$prompt" "$transcript" || run_exit=$?
        ;;
      *)
        printf '%s\t%s\tSKIPPED\t-\t-\t0\tunknown-tool\t-\n' "$tool" "$case_id" >> "$RESULTS_FILE"
        continue
        ;;
    esac

    used_build_brief=no
    build_brief_count=$(count_build_brief_usage "$transcript")
    if [[ "$build_brief_count" != "0" ]]; then
      used_build_brief=yes
    fi

    expectation=missing
    if rg -q --fixed-strings "$expect_snippet" "$transcript"; then
      expectation=matched
    fi

    status=PASS
    if [[ "$run_exit" == "124" ]]; then
      status=TIMEOUT
    elif [[ "$used_build_brief" != "yes" || "$expectation" != "matched" ]]; then
      status=FAIL
    fi

    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$tool" "$case_id" "$status" "$run_exit" "$used_build_brief" "$build_brief_count" "$expectation" "$transcript" >> "$RESULTS_FILE"
  done

done < "$CASES_FILE"

if command -v gradle >/dev/null 2>&1; then
  gradle --gradle-user-home "$SHARED_GRADLE_HOME" --status > "$OUTPUT_ROOT/daemon-status-after.txt" 2>&1 || true
fi

cat "$RESULTS_FILE"
