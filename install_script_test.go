package buildbrief_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testRepo = "static-var/build-brief"
const testVersion = "0.0.11"
const testTag = "v0.0.11"

func TestInstallScriptLatestUsesReleaseRedirectAndDirectDownloads(t *testing.T) {
	fixture := newInstallFixture(t)

	result := runInstallScript(t, fixture, "--bin-dir", fixture.binDir, "--repo", testRepo)

	if result.err != nil {
		t.Fatalf("install.sh failed:\n%s", result.output)
	}
	if !strings.Contains(result.output, "Installed build-brief") {
		t.Fatalf("expected install output, got:\n%s", result.output)
	}

	calls := readCalls(t, fixture.callsPath)
	assertCallContains(t, calls, "https://github.com/"+testRepo+"/releases/latest")
	assertNoCallContains(t, calls, "api.github.com")
	assertCallContains(t, calls, fixture.archiveURL)
	assertCallContains(t, calls, "https://github.com/"+testRepo+"/releases/download/"+testTag+"/SHA256SUMS")
	assertInstalledBinary(t, fixture.binDir)
}

func TestInstallScriptPinnedVersionSkipsDiscoveryAndUsesDirectDownloads(t *testing.T) {
	fixture := newInstallFixture(t)

	result := runInstallScript(t, fixture, "--version", testVersion, "--bin-dir", fixture.binDir, "--repo", testRepo)

	if result.err != nil {
		t.Fatalf("install.sh failed:\n%s", result.output)
	}

	calls := readCalls(t, fixture.callsPath)
	assertNoCallContains(t, calls, "api.github.com")
	assertNoCallContains(t, calls, "https://github.com/"+testRepo+"/releases/latest")
	assertCallContains(t, calls, fixture.archiveURL)
	assertCallContains(t, calls, "https://github.com/"+testRepo+"/releases/download/"+testTag+"/SHA256SUMS")
	assertInstalledBinary(t, fixture.binDir)
}

func TestInstallScriptLooksUpChecksumFromSHA256SUMS(t *testing.T) {
	fixture := newInstallFixture(t)
	sumsContent := fmt.Sprintf("%s  ./%s\n", fixture.archiveSHA256, fixture.assetName)
	if err := os.WriteFile(fixture.sumsPath, []byte(sumsContent), 0o644); err != nil {
		t.Fatalf("write SHA256SUMS: %v", err)
	}

	result := runInstallScript(t, fixture, "--version", testVersion, "--bin-dir", fixture.binDir, "--repo", testRepo)

	if result.err != nil {
		t.Fatalf("install.sh failed:\n%s", result.output)
	}

	calls := readCalls(t, fixture.callsPath)
	assertCallContains(t, calls, "https://github.com/"+testRepo+"/releases/download/"+testTag+"/SHA256SUMS")
	assertInstalledBinary(t, fixture.binDir)
}

type installFixture struct {
	tempDir       string
	binDir        string
	fakeBinDir    string
	archivePath   string
	sumsPath      string
	callsPath     string
	assetName     string
	archiveURL    string
	archiveSHA256 string
}

type installResult struct {
	output string
	err    error
}

func newInstallFixture(t *testing.T) installFixture {
	t.Helper()

	osName, archName, ok := installerPlatform()
	if !ok {
		t.Skipf("install.sh does not support %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	tempDir := t.TempDir()
	assetName := fmt.Sprintf("build-brief_%s_%s_%s.tar.gz", testVersion, osName, archName)
	archivePath := filepath.Join(tempDir, assetName)
	archiveBytes := buildArchive(t)
	if err := os.WriteFile(archivePath, archiveBytes, 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	sum := sha256.Sum256(archiveBytes)
	archiveSHA256 := hex.EncodeToString(sum[:])

	sumsPath := filepath.Join(tempDir, "SHA256SUMS")
	if err := os.WriteFile(sumsPath, []byte(fmt.Sprintf("%s  %s\n", archiveSHA256, assetName)), 0o644); err != nil {
		t.Fatalf("write SHA256SUMS: %v", err)
	}

	fakeBinDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(fakeBinDir, 0o755); err != nil {
		t.Fatalf("create fake bin: %v", err)
	}
	callsPath := filepath.Join(tempDir, "curl-calls.log")
	writeFakeCurl(t, filepath.Join(fakeBinDir, "curl"))

	return installFixture{
		tempDir:       tempDir,
		binDir:        filepath.Join(tempDir, "install-bin"),
		fakeBinDir:    fakeBinDir,
		archivePath:   archivePath,
		sumsPath:      sumsPath,
		callsPath:     callsPath,
		assetName:     assetName,
		archiveURL:    "https://github.com/" + testRepo + "/releases/download/" + testTag + "/" + assetName,
		archiveSHA256: archiveSHA256,
	}
}

func runInstallScript(t *testing.T, fixture installFixture, args ...string) installResult {
	t.Helper()

	cmdArgs := append([]string{"install.sh"}, args...)
	cmd := exec.Command("bash", cmdArgs...)
	cmd.Env = append(os.Environ(),
		"PATH="+fixture.fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TEST_ARCHIVE="+fixture.archivePath,
		"TEST_SUMS="+fixture.sumsPath,
		"TEST_CALLS="+fixture.callsPath,
		"TEST_REPO="+testRepo,
		"TEST_ASSET_NAME="+fixture.assetName,
	)
	output, err := cmd.CombinedOutput()
	return installResult{output: string(output), err: err}
}

func buildArchive(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	binary := []byte("#!/usr/bin/env sh\necho build-brief version " + testVersion + "\n")

	if err := tw.WriteHeader(&tar.Header{
		Name: "build-brief",
		Mode: 0o755,
		Size: int64(len(binary)),
	}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func writeFakeCurl(t *testing.T, path string) {
	t.Helper()

	script := `#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >> "$TEST_CALLS"

url=""
output=""
write_out=""
args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
  case "${args[$i]}" in
    -o)
      i=$((i + 1))
      output="${args[$i]}"
      ;;
    -w|--write-out)
      i=$((i + 1))
      write_out="${args[$i]}"
      ;;
    http://*|https://*)
      url="${args[$i]}"
      ;;
  esac
done

latest_url="https://github.com/${TEST_REPO}/releases/latest"
tag_url="https://github.com/${TEST_REPO}/releases/tag/v0.0.11"
archive_url="https://github.com/${TEST_REPO}/releases/download/v0.0.11/${TEST_ASSET_NAME}"
sums_url="https://github.com/${TEST_REPO}/releases/download/v0.0.11/SHA256SUMS"

if [[ "$url" == https://api.github.com/* ]]; then
  echo "unexpected GitHub API request: $url" >&2
  exit 42
elif [[ "$url" == "$latest_url" ]]; then
  if [[ -n "$write_out" ]]; then
    printf '%s' "$tag_url"
  else
    printf 'HTTP/2 302\r\nlocation: %s\r\n\r\n' "$tag_url"
  fi
elif [[ "$url" == "$archive_url" ]]; then
  if [[ -z "$output" ]]; then
    echo "archive request missing -o" >&2
    exit 43
  fi
  cp "$TEST_ARCHIVE" "$output"
elif [[ "$url" == "$sums_url" ]]; then
  if [[ -z "$output" ]]; then
    echo "SHA256SUMS request missing -o" >&2
    exit 44
  fi
  cp "$TEST_SUMS" "$output"
else
  echo "unexpected URL: $url" >&2
  exit 45
fi
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
}

func installerPlatform() (string, string, bool) {
	var osName string
	switch runtime.GOOS {
	case "darwin":
		osName = "darwin"
	case "linux":
		osName = "linux"
	default:
		return "", "", false
	}

	var archName string
	switch runtime.GOARCH {
	case "amd64":
		archName = "amd64"
	case "arm64":
		archName = "arm64"
	default:
		return "", "", false
	}

	return osName, archName, true
}

func readCalls(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read curl calls: %v", err)
	}
	return string(content)
}

func assertCallContains(t *testing.T, calls, want string) {
	t.Helper()

	if !strings.Contains(calls, want) {
		t.Fatalf("expected curl calls to contain %q, got:\n%s", want, calls)
	}
}

func assertNoCallContains(t *testing.T, calls, notWant string) {
	t.Helper()

	if strings.Contains(calls, notWant) {
		t.Fatalf("expected curl calls not to contain %q, got:\n%s", notWant, calls)
	}
}

func assertInstalledBinary(t *testing.T, binDir string) {
	t.Helper()

	path := filepath.Join(binDir, "build-brief")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected installed binary at %s: %v", path, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected installed binary to be executable, mode=%s", info.Mode())
	}
}
