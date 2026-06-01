#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
build-brief installer

Usage:
  install.sh [--version x.y.z] [--bin-dir PATH] [--repo owner/repo]
  install.sh --help

Examples:
  curl -fsSL https://bb.staticvar.dev/install.sh | bash
  curl -fsSL https://bb.staticvar.dev/install.sh | bash -s -- --bin-dir /usr/local/bin
  wget -qO- https://bb.staticvar.dev/install.sh | bash
  curl -fsSL https://bb.staticvar.dev/install.sh | bash -s -- --version 0.1.0

Options:
  --version x.y.z   Install a specific released version (default: latest)
  --bin-dir PATH    Installation directory for the build-brief binary
  --repo owner/repo GitHub repository to download from (default: static-var/build-brief)
EOF
}

repo="static-var/build-brief"
version=""
bin_dir="${BUILD_BRIEF_INSTALL_DIR:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version="${2:-}"
      shift 2
      ;;
    --bin-dir)
      bin_dir="${2:-}"
      shift 2
      ;;
    --repo)
      repo="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "install.sh: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "install.sh: required command not found: $1" >&2
    exit 1
  fi
}

download_to_file() {
  local url="$1"
  local output="$2"

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$output"
    return
  fi

  if command -v wget >/dev/null 2>&1; then
    wget -qO "$output" "$url"
    return
  fi

  echo "install.sh: either curl or wget is required" >&2
  exit 1
}

trim_version() {
  local value="$1"
  value="${value#v}"
  printf '%s\n' "$value"
}

detect_os() {
  case "$(uname -s)" in
    Darwin)
      printf 'darwin\n'
      ;;
    Linux)
      printf 'linux\n'
      ;;
    *)
      echo "install.sh: unsupported operating system: $(uname -s)" >&2
      echo "install.sh: use the release archives for manual installation on this platform." >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)
      printf 'amd64\n'
      ;;
    arm64|aarch64)
      printf 'arm64\n'
      ;;
    *)
      echo "install.sh: unsupported architecture: $(uname -m)" >&2
      echo "install.sh: use the release archives for manual installation on this platform." >&2
      exit 1
      ;;
  esac
}

default_bin_dir() {
  if [[ -n "${bin_dir}" ]]; then
    printf '%s\n' "$bin_dir"
    return
  fi

  if [[ -d /usr/local/bin && -w /usr/local/bin ]]; then
    printf '/usr/local/bin\n'
    return
  fi

  printf '%s\n' "${HOME}/.local/bin"
}

resolve_url_effective() {
  local url="$1"

  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o /dev/null -w '%{url_effective}' "$url"
    return
  fi

  if command -v wget >/dev/null 2>&1; then
    wget -qO /dev/null --server-response "$url" 2>&1 | awk '
      tolower($1) == "location:" {
        location = $2
      }
      END {
        gsub(/\r/, "", location)
        print location
      }
    '
    return
  fi

  echo "install.sh: either curl or wget is required" >&2
  exit 1
}

resolve_latest_version() {
  local latest_url="https://github.com/${repo}/releases/latest"
  local effective_url
  effective_url="$(resolve_url_effective "$latest_url")"

  local tag
  tag="$(printf '%s\n' "$effective_url" | sed -n 's#.*/releases/tag/\([^/?#]*\).*#\1#p' | head -n 1)"
  if [[ -z "$tag" ]]; then
    echo "install.sh: could not determine latest release tag from ${latest_url}" >&2
    exit 1
  fi

  trim_version "$tag"
}

verify_checksum() {
  local asset_path="$1"
  local expected="$2"
  local asset_name="$3"

  if [[ -z "$expected" ]]; then
    echo "install.sh: expected checksum is empty for ${asset_name}" >&2
    exit 1
  fi

  local actual=""
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$asset_path" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$asset_path" | awk '{print $1}')"
  else
    echo "install.sh: skipping checksum verification because neither sha256sum nor shasum is available" >&2
    return
  fi

  if [[ "$actual" != "$expected" ]]; then
    echo "install.sh: checksum mismatch for ${asset_name}" >&2
    exit 1
  fi
}

checksum_from_sums_file() {
  local sums_path="$1"
  local asset_name="$2"
  awk -v name="$asset_name" '$2 == "./"name || $2 == name { print $1; exit }' "$sums_path"
}

need_cmd tar
need_cmd mktemp
need_cmd install

os="$(detect_os)"
arch="$(detect_arch)"
if [[ -n "$version" ]]; then
  version="$(trim_version "$version")"
else
  version="$(resolve_latest_version)"
fi
tag="v${version}"
bin_dir="$(default_bin_dir)"

asset_name="build-brief_${version}_${os}_${arch}.tar.gz"
asset_url="https://github.com/${repo}/releases/download/${tag}/${asset_name}"
sums_url="https://github.com/${repo}/releases/download/${tag}/SHA256SUMS"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

archive_path="${tmpdir}/${asset_name}"
sums_path="${tmpdir}/SHA256SUMS"
extract_dir="${tmpdir}/extract"

echo "==> Downloading build-brief ${version} for ${os}/${arch}"
download_to_file "$asset_url" "$archive_path"
download_to_file "$sums_url" "$sums_path"
verify_checksum "$archive_path" "$(checksum_from_sums_file "$sums_path" "$asset_name")" "$asset_name"

mkdir -p "$extract_dir"
tar -xzf "$archive_path" -C "$extract_dir"

binary_path="$(find "$extract_dir" -type f -name build-brief | head -n 1)"
if [[ -z "$binary_path" ]]; then
  echo "install.sh: could not find build-brief binary in downloaded archive" >&2
  exit 1
fi

mkdir -p "$bin_dir"
install -m 755 "$binary_path" "${bin_dir}/build-brief"

echo "==> Installed build-brief to ${bin_dir}/build-brief"
"${bin_dir}/build-brief" --version

case ":${PATH}:" in
  *":${bin_dir}:"*)
    ;;
  *)
    echo
    echo "Add ${bin_dir} to your PATH if it is not there already."
    ;;
esac
