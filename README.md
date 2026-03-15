# build-brief

[![Website](https://img.shields.io/badge/Website-bb.staticvar.dev-0f172a?style=flat-square)](https://bb.staticvar.dev)
[![Latest release](https://img.shields.io/github/v/release/static-var/build-brief?display_name=tag&style=flat-square)](https://github.com/static-var/build-brief/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/static-var/build-brief?style=flat-square)](https://github.com/static-var/build-brief/blob/main/go.mod)
[![License](https://img.shields.io/github/license/static-var/build-brief?style=flat-square)](./LICENSE)
[![Homebrew tap](https://img.shields.io/badge/Homebrew-static--var%2Ftap-fbbf24?style=flat-square)](https://github.com/static-var/homebrew-tap)

`build-brief` is a small Go CLI that sits in front of Gradle, keeps the full raw log on disk, and cuts terminal output down to the parts that usually matter.

It is inspired by [`rtk`](https://github.com/rtk-ai/rtk), but built separately because RTK is not compatible enough with Gradle workflows to reuse directly here.

For full docs, real before/after examples, agent setup, hook guidance, limitations, and install details, see **<https://bb.staticvar.dev>**.

## Why build-brief?

- Wraps either `gradle` or `./gradlew`
- Preserves the Gradle exit code
- Keeps the full raw log on disk
- Returns failed tasks, failed tests, warnings, artifacts, and final status
- Normalizes output-shaping flags so reduction stays stable
- Reuses or starts the Gradle daemon by default
- Works across Spring Boot, Ktor, Android, Kotlin Multiplatform, plain JVM, and multi-project builds

## Install

### Script install

```bash
curl -fsSL https://bb.staticvar.dev/install.sh | bash
wget -qO- https://bb.staticvar.dev/install.sh | bash
```

### Homebrew or Linuxbrew

```bash
brew tap static-var/tap
brew install static-var/tap/build-brief
```

### Build from source

```bash
go build -o build-brief ./cmd/build-brief
```

The installer currently targets macOS and Linux. Windows should use a release archive or build from source.

## Quick start

```bash
build-brief test
build-brief build
build-brief gradle test
build-brief ./gradlew test
build-brief --gradle-user-home /tmp/build-brief-gradle-home ./gradlew test
build-brief gains --history
build-brief --help
```

If you want to keep the original command shape explicit, prefer `build-brief gradle ...` for a PATH-resolved Gradle binary and `build-brief ./gradlew ...` for a project-local wrapper.

## Agent integration

`build-brief` can install managed instruction blocks for supported tools:

```bash
build-brief --install
build-brief --install-force
build-brief --global
```

There is also a checked-in Claude Code hook example at `examples/hooks/claude-code/`.

If your agent tool supports `AGENTS.md` or an instructions file, a simple default rule is:

```md
Use `build-brief` for routine Gradle commands.
Prefer `build-brief gradle ...` or `build-brief ./gradlew ...` over raw Gradle calls.
Fall back to raw Gradle only when the reduced summary is not enough.
```

## Current limitations

- Standard Gradle layouts work best today
- Custom artifact directories or unusual plugin output may still require the raw log
- Platform support exists for macOS, Linux, and Windows, but the project is not heavily tested across every OS, shell, CI environment, and Gradle/plugin combination yet

For the fuller behavior guide, hooks, examples, and caveats, head to **<https://bb.staticvar.dev>**.

## Project links

- Website: <https://bb.staticvar.dev>
- Releases: <https://github.com/static-var/build-brief/releases/latest>
- Homebrew tap: <https://github.com/static-var/homebrew-tap>
- Hook examples: [`examples/hooks/README.md`](./examples/hooks/README.md)

## License

MIT
