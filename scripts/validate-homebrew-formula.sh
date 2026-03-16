#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/validate-homebrew-formula.sh --version x.y.z --artifact-dir DIR --formula FILE
EOF
}

version=""
artifact_dir=""
formula=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version="${2:-}"
      shift 2
      ;;
    --artifact-dir)
      artifact_dir="${2:-}"
      shift 2
      ;;
    --formula)
      formula="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "validate-homebrew-formula: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "$version" || -z "$artifact_dir" || -z "$formula" ]]; then
  usage >&2
  exit 1
fi

archive="${artifact_dir}/build-brief_${version}_darwin_arm64.tar.gz"
if [[ ! -f "$archive" ]]; then
  echo "validate-homebrew-formula: archive not found: $archive" >&2
  exit 1
fi

if [[ ! -f "$formula" ]]; then
  echo "validate-homebrew-formula: formula not found: $formula" >&2
  exit 1
fi

if ! grep -Fq '"build-brief"' "$formula" || ! grep -Fq 'build-brief_*/build-brief' "$formula"; then
  echo "validate-homebrew-formula: formula install logic is missing the expected root and nested binary candidates" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

tar -xzf "$archive" -C "$tmpdir"
extracted_root="${tmpdir}/build-brief_${version}_darwin_arm64"

if [[ ! -d "$extracted_root" ]]; then
  echo "validate-homebrew-formula: extracted root not found: $extracted_root" >&2
  exit 1
fi

ruby - "$tmpdir" "$extracted_root" <<'RUBY'
def resolve_binary(dir)
  Dir.chdir(dir) do
    ["build-brief", *Dir["build-brief_*/build-brief"].sort].find { |path| File.file?(path) }
  end
end

parent_dir, root_dir = ARGV

parent_binary = resolve_binary(parent_dir)
raise "expected nested archive candidate from #{parent_dir}, got #{parent_binary.inspect}" unless parent_binary == File.join(File.basename(root_dir), "build-brief")

root_binary = resolve_binary(root_dir)
raise "expected root archive candidate from #{root_dir}, got #{root_binary.inspect}" unless root_binary == "build-brief"
RUBY
