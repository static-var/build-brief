# Contributor instructions

## Setup and verification

- Requires the Go version in `go.mod` (currently Go 1.26.1). Run `go mod download`; build with `go build ./cmd/build-brief`.
- Before handing off code, run `go test ./...`, `go vet ./...`, and `go test -race ./...` when the platform supports it. Also use `gofmt -w` on changed Go files, `go mod tidy -diff`, `go mod verify`, and `git diff --check`.
- CI runs tests on Linux, macOS, and Windows; Linux also runs the race detector. Its quality job enforces formatting, vet, module checks, shell syntax, and diff hygiene. Keep changes portable.

## Architecture

- `cmd/build-brief`: CLI entry point.
- `internal/app`: argument parsing and orchestration; `internal/gradle`: command resolution/classification; `internal/runner`: process and raw-log capture.
- `internal/reducer`, `internal/output`, `internal/artifacts`: summarize Gradle output and render results.
- `internal/config`, `internal/doctor`, `internal/rewrite`, `internal/install`: configuration and auxiliary commands.
- `internal/tracking`: local gains history. Local-only: gains history stays on this machine; no gains data is transmitted.
- `site/` is the static website; `scripts/` and `.github/workflows/` define release/CI operations; `smoke/` holds Gradle smoke fixtures.

## Safety

- Preserve Gradle exit codes and raw-log behavior; do not change runtime behavior for documentation-only work.
- Treat `scripts/prepare-release.sh`, artifact/formula scripts, and the Release workflow as release-only operations. Do not publish, merge, or release without explicit approval.
- Keep this file concise; use `README.md`, tests, and current code as the detailed source of truth.
