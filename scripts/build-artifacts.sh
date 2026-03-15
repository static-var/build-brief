#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/build-artifacts.sh --version x.y.z [--output-dir DIR]
EOF
}

version=""
output_dir="dist"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version="${2:-}"
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
      echo "build-artifacts: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "$version" ]]; then
  echo "build-artifacts: --version is required" >&2
  exit 1
fi

mkdir -p "$output_dir"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

targets=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
  "windows amd64"
  "windows arm64"
)

for target in "${targets[@]}"; do
  read -r goos goarch <<<"$target"
  package_root="${work_dir}/build-brief_${version}_${goos}_${goarch}"
  mkdir -p "$package_root"

  binary_name="build-brief"
  if [[ "$goos" == "windows" ]]; then
    binary_name="${binary_name}.exe"
  fi

  env CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags="-s -w -X build-brief/internal/app.Version=${version}" \
    -o "${package_root}/${binary_name}" ./cmd/build-brief

  cp README.md LICENSE "$package_root/"

  archive_base="build-brief_${version}_${goos}_${goarch}"
  if [[ "$goos" == "windows" ]]; then
    python3 - "$package_root" "${output_dir}/${archive_base}.zip" <<'PY'
from pathlib import Path
import sys
import zipfile

source_dir = Path(sys.argv[1])
archive_path = Path(sys.argv[2])

with zipfile.ZipFile(archive_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
    for path in source_dir.rglob("*"):
        if path.is_file():
            zf.write(path, arcname=path.relative_to(source_dir.parent))
PY
  else
    tar -C "$work_dir" -czf "${output_dir}/${archive_base}.tar.gz" "${archive_base}"
  fi
done

(
  cd "$output_dir"
  sha256sum ./* > SHA256SUMS
)
