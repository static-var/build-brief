#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/publish-homebrew-tap.sh --tap-repo owner/homebrew-tap --formula-file FILE --version x.y.z [--branch BRANCH]
Requires HOMEBREW_TAP_TOKEN in the environment.
EOF
}

tap_repo=""
formula_file=""
version=""
branch="main"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tap-repo)
      tap_repo="${2:-}"
      shift 2
      ;;
    --formula-file)
      formula_file="${2:-}"
      shift 2
      ;;
    --version)
      version="${2:-}"
      shift 2
      ;;
    --branch)
      branch="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "publish-homebrew-tap: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "$tap_repo" || -z "$formula_file" || -z "$version" ]]; then
  usage >&2
  exit 1
fi

if [[ -z "${HOMEBREW_TAP_TOKEN:-}" ]]; then
  echo "publish-homebrew-tap: HOMEBREW_TAP_TOKEN is not set; skipping tap publish" >&2
  exit 0
fi

if [[ ! -f "$formula_file" ]]; then
  echo "publish-homebrew-tap: formula file not found: $formula_file" >&2
  exit 1
fi

formula_file="$(cd "$(dirname "$formula_file")" && pwd)/$(basename "$formula_file")"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

git clone --quiet "https://x-access-token:${HOMEBREW_TAP_TOKEN}@github.com/${tap_repo}.git" "$tmpdir/tap"

cd "$tmpdir/tap"
git checkout "$branch" 2>/dev/null || git checkout -b "$branch"
mkdir -p Formula
cp "$formula_file" Formula/build-brief.rb

git add Formula/build-brief.rb

if git diff --cached --quiet; then
  echo "publish-homebrew-tap: Formula/build-brief.rb is unchanged"
  exit 0
fi

git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git commit -m "build-brief v${version}"

if git ls-remote --exit-code --heads origin "$branch" >/dev/null 2>&1; then
  git pull --rebase origin "$branch"
fi

git push origin "HEAD:${branch}"
