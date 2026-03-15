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

extract_current_version() {
  python3 - "$version_file" <<'PY'
from pathlib import Path
import re
import sys

content = Path(sys.argv[1]).read_text(encoding="utf-8")
match = re.search(r'var Version = "([^"]+)"', content)
if not match:
    raise SystemExit("prepare-release: could not read current version")
print(match.group(1))
PY
}

write_changelog_entry() {
  local section_file="$1"
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
}

extract_changelog_section() {
  local tag="$1"
  local target_file="$2"
  python3 - "$changelog_file" "$tag" "$target_file" <<'PY'
from pathlib import Path
import sys

changelog_path = Path(sys.argv[1])
tag = sys.argv[2]
target_path = Path(sys.argv[3])

if not changelog_path.exists():
    raise SystemExit("prepare-release: changelog file does not exist")

content = changelog_path.read_text(encoding="utf-8")
needle = f"## {tag}"
start = content.find(needle)
if start == -1:
    raise SystemExit(f"prepare-release: changelog section not found for {tag}")

next_section = content.find("\n## ", start + len(needle))
section = content[start:] if next_section == -1 else content[start:next_section]
target_path.write_text(section.strip() + "\n", encoding="utf-8")
PY
}

latest_tag="$(git describe --tags --abbrev=0 2>/dev/null || true)"
base_version="${latest_tag#v}"
head_subject="$(git log -1 --format=%s)"
head_tag="$(git tag --points-at HEAD | sed -n 's/^\(v[0-9]\+\.[0-9]\+\.[0-9]\+\)$/\1/p' | head -n 1)"
current_version="$(extract_current_version)"
needs_commit="true"
tag_exists="false"
recovering_release="false"

if [[ -n "$head_tag" ]]; then
  tag="$head_tag"
  next_version="${tag#v}"
  needs_commit="false"
  tag_exists="true"
  recovering_release="true"
elif [[ "$head_subject" =~ ^chore\(release\):[[:space:]](v[0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
  tag="${BASH_REMATCH[1]}"
  next_version="${tag#v}"
  validate_version "$next_version"
  if [[ "$current_version" != "$next_version" ]]; then
    echo "prepare-release: release commit version ${current_version} does not match ${next_version}" >&2
    exit 1
  fi
  needs_commit="false"
  recovering_release="true"
fi

if [[ "$recovering_release" != "true" ]]; then
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
fi

if git rev-parse --verify --quiet "refs/tags/${tag}" >/dev/null; then
  tag_exists="true"
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

if [[ "$recovering_release" == "true" ]]; then
  extract_changelog_section "$tag" "$notes_file"
  cp "$notes_file" "$section_file"
else
  cat >"$notes_file" <<EOF
## ${tag} - ${release_date}

${commit_list}
EOF

  cp "$notes_file" "$section_file"
fi

if [[ "$needs_commit" == "true" ]]; then
  python3 - "$version_file" "$next_version" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
version = sys.argv[2]
path.write_text(f'package app\n\nvar Version = "{version}"\n', encoding="utf-8")
PY

  write_changelog_entry "$section_file"
fi

printf '%s\n' "$next_version" >"${output_dir}/version.txt"
printf '%s\n' "$tag" >"${output_dir}/tag.txt"

if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  {
    printf 'version=%s\n' "$next_version"
    printf 'tag=%s\n' "$tag"
    printf 'notes_file=%s\n' "$notes_file"
    printf 'needs_commit=%s\n' "$needs_commit"
    printf 'tag_exists=%s\n' "$tag_exists"
  } >>"$GITHUB_OUTPUT"
fi
