#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/generate-homebrew-formula.sh --version x.y.z --repo owner/repo --artifact-dir DIR --output FILE
EOF
}

version=""
repo=""
artifact_dir=""
output=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version="${2:-}"
      shift 2
      ;;
    --repo)
      repo="${2:-}"
      shift 2
      ;;
    --artifact-dir)
      artifact_dir="${2:-}"
      shift 2
      ;;
    --output)
      output="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "generate-homebrew-formula: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "$version" || -z "$repo" || -z "$artifact_dir" || -z "$output" ]]; then
  usage >&2
  exit 1
fi

python3 - "$version" "$repo" "$artifact_dir" "$output" <<'PY'
from pathlib import Path
import hashlib
import sys

version, repo, artifact_dir, output_path = sys.argv[1:5]
artifact_dir = Path(artifact_dir)
output_path = Path(output_path)

artifacts = {
    "darwin_arm64": artifact_dir / f"build-brief_{version}_darwin_arm64.tar.gz",
    "darwin_amd64": artifact_dir / f"build-brief_{version}_darwin_amd64.tar.gz",
    "linux_arm64": artifact_dir / f"build-brief_{version}_linux_arm64.tar.gz",
    "linux_amd64": artifact_dir / f"build-brief_{version}_linux_amd64.tar.gz",
}

missing = [str(path) for path in artifacts.values() if not path.exists()]
if missing:
    raise SystemExit(f"missing artifacts for formula generation: {missing}")

def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()

checksums = {name: sha256(path) for name, path in artifacts.items()}

formula = f"""class BuildBrief < Formula
  desc "Reduce noisy Gradle output into concise build summaries"
  homepage "https://bb.staticvar.dev"
  version "{version}"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/{repo}/releases/download/v{version}/build-brief_{version}_darwin_arm64.tar.gz"
      sha256 "{checksums["darwin_arm64"]}"
    elsif Hardware::CPU.intel?
      url "https://github.com/{repo}/releases/download/v{version}/build-brief_{version}_darwin_amd64.tar.gz"
      sha256 "{checksums["darwin_amd64"]}"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/{repo}/releases/download/v{version}/build-brief_{version}_linux_arm64.tar.gz"
      sha256 "{checksums["linux_arm64"]}"
    elsif Hardware::CPU.intel?
      url "https://github.com/{repo}/releases/download/v{version}/build-brief_{version}_linux_amd64.tar.gz"
      sha256 "{checksums["linux_amd64"]}"
    end
  end

  def install
    binary = Dir["build-brief_*/build-brief"].find {{ |path| File.file?(path) }}
    raise "build-brief binary not found in archive" unless binary

    bin.install binary => "build-brief"
  end

  test do
    assert_match version.to_s, shell_output("#{{bin}}/build-brief --version")
  end
end
"""

output_path.parent.mkdir(parents=True, exist_ok=True)
output_path.write_text(formula, encoding="utf-8")
PY
