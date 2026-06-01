#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/append-generated-release-notes.sh --notes-file FILE --generated-file FILE --output FILE
EOF
}

notes_file=""
generated_file=""
output_file=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --notes-file)
      notes_file="${2:-}"
      shift 2
      ;;
    --generated-file)
      generated_file="${2:-}"
      shift 2
      ;;
    --output)
      output_file="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "append-generated-release-notes: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "$notes_file" || -z "$generated_file" || -z "$output_file" ]]; then
  echo "append-generated-release-notes: --notes-file, --generated-file, and --output are required" >&2
  usage >&2
  exit 1
fi

if [[ ! -f "$notes_file" ]]; then
  echo "append-generated-release-notes: notes file does not exist: $notes_file" >&2
  exit 1
fi

if [[ ! -f "$generated_file" ]]; then
  echo "append-generated-release-notes: generated file does not exist: $generated_file" >&2
  exit 1
fi

mkdir -p "$(dirname "$output_file")"

if ! grep -q '[^[:space:]]' "$generated_file"; then
  cp "$notes_file" "$output_file"
  exit 0
fi

{
  cat "$notes_file"
  printf '\n## Auto-generated changelog\n\n'
  cat "$generated_file"
} >"$output_file"
