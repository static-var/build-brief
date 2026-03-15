#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/prepare-release.sh [--bump patch|minor|major] [--version x.y.z] [--output-dir DIR]
EOF
}

bump="patch"
explicit_version=""
output_dir=".release"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bump)
      bump="${2:-}"
      shift 2
      ;;
    --version)
      explicit_version="${2:-}"
      shift 2
      ;;
    --output-dir)
      output_dir="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "prepare-release: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

mkdir -p "$output_dir"

version_file="internal/app/version.go"
changelog_file="CHANGELOG.md"

semver_regex='^[0-9]+\.[0-9]+\.[0-9]+$'

validate_version() {
  local value="$1"
  if [[ ! "$value" =~ $semver_regex ]]; then
    echo "prepare-release: expected semantic version x.y.z, got: $value" >&2
    exit 1
  fi
}

bump_version() {
  local base="$1"
  local part="$2"
  local major minor patch
  IFS='.' read -r major minor patch <<<"$base"
  case "$part" in
    patch)
      patch=$((patch + 1))
      ;;
    minor)
      minor=$((minor + 1))
      patch=0
      ;;
    major)
      major=$((major + 1))
      minor=0
      patch=0
      ;;
    *)
      echo "prepare-release: unsupported bump type: $part" >&2
      exit 1
      ;;
  esac
  printf '%s.%s.%s\n' "$major" "$minor" "$patch"
}

latest_tag="$(git describe --tags --abbrev=0 2>/dev/null || true)"
base_version="${latest_tag#v}"

if [[ -n "$explicit_version" ]]; then
  next_version="${explicit_version#v}"
  validate_version "$next_version"
else
  if [[ -z "$base_version" ]]; then
    base_version="0.0.0"
  else
    validate_version "$base_version"
  fi
  next_version="$(bump_version "$base_version" "$bump")"
fi

tag="v${next_version}"

if git rev-parse --verify --quiet "refs/tags/${tag}" >/dev/null; then
  echo "prepare-release: tag already exists: ${tag}" >&2
  exit 1
fi

if [[ -n "$latest_tag" ]]; then
  commit_range="${latest_tag}..HEAD"
else
  commit_range="HEAD"
fi

commit_list="$(git log --no-merges --pretty=format:'- %s (%h)' ${commit_range})"
if [[ -z "$commit_list" ]]; then
  commit_list="- Initial release."
fi

release_date="$(date -u +%Y-%m-%d)"
notes_file="${output_dir}/release-notes.md"
section_file="${output_dir}/changelog-section.md"

cat >"$notes_file" <<EOF
## ${tag} - ${release_date}

${commit_list}
EOF

cp "$notes_file" "$section_file"

python3 - "$version_file" "$next_version" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
version = sys.argv[2]
path.write_text(f'package app\n\nvar Version = "{version}"\n', encoding="utf-8")
PY

python3 - "$changelog_file" "$section_file" <<'PY'
from pathlib import Path
import sys

changelog_path = Path(sys.argv[1])
section = Path(sys.argv[2]).read_text(encoding="utf-8").strip() + "\n\n"
header = "# Changelog\n\n"

if changelog_path.exists():
    existing = changelog_path.read_text(encoding="utf-8")
    if existing.startswith(header):
        body = existing[len(header):].lstrip()
    elif existing.startswith("# Changelog\n"):
        body = existing[len("# Changelog\n"):].lstrip()
    else:
        body = existing.lstrip()
else:
    body = "Releases prepend new entries here.\n"

changelog_path.write_text(header + section + body, encoding="utf-8")
PY

printf '%s\n' "$next_version" >"${output_dir}/version.txt"
printf '%s\n' "$tag" >"${output_dir}/tag.txt"

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  {
    printf 'version=%s\n' "$next_version"
    printf 'tag=%s\n' "$tag"
    printf 'notes_file=%s\n' "$notes_file"
  } >>"$GITHUB_OUTPUT"
fi
