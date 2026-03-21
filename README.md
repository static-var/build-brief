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

Example successful test run:

```text
$ build-brief test
BUILD SUCCESSFUL in 2s
Tests: 2 passed, 0 failed
Warnings: 1
  - OpenJDK 64-Bit Server VM warning: Sharing is only supported for boot loader classes because bootstrap classpath has been appended
```

Example failed test run:

```text
$ build-brief test
BUILD FAILED in 900ms
Tests: 7 passed, 1 failed
Command: gradle --console=plain test
Failed tasks:
  - :test
Failed tests:
  - GreetingServiceTest > returns fallback message
Highlights:
  - GreetingServiceTest > returns fallback message: expected:<Hello> but was:<null>
Raw log: /tmp/build-brief/build-brief-abcd1234.latest.log
```

## Check your gains

`build-brief` records rough token savings automatically whenever it wraps a Gradle command. Use the `gains` subcommand to inspect totals, recent runs, project-only scope, or machine-readable output:

```bash
build-brief gains
build-brief gains --history
build-brief gains --project
build-brief gains --format json
build-brief gains --reset
```

The savings numbers use the built-in chars-divided-by-4 heuristic. They are useful for rough feedback and trend tracking, not billing-grade accounting.

Example text output:

```text
build-brief Token Savings (Global Scope)
============================================================

Total commands:  38
Raw tokens:      80.9K
Emitted tokens:  3.8K
Tokens saved:    77.2K (95.4%)
Efficiency:      ██████████████████████░ 95.4%

By Command
------------------------------------------------------------------------------
  #  Command                       Count     Saved    Avg%
------------------------------------------------------------------------------
  1  gradlew :androidApp:assembl…      6     38.6K   98.2%
  2  gradlew :androidApp:clean :…      6     32.7K   92.5%
  3  gradlew build                     9      2.6K   60.8%
  4  gradlew assembleDebug             2      1.4K   99.1%
  5  gradle clean jvmTest              6       848   47.5%

Recent Commands
----------------------------------------------------------
03-21 13:02 ▲ gradle clean test              67.2% (90)
03-21 13:02 ▲ gradle clean jvmTest           35.8% (82)
03-21 13:02 ▲ gradle clean test              49.2% (62)
```

Example JSON output:

```json
{
  "summary": {
    "total_commands": 38,
    "total_raw_tokens": 80925,
    "total_emitted_tokens": 3757,
    "total_saved_tokens": 77168,
    "avg_savings_pct": 95.3574297188755,
    "by_command": [
      {
        "command": "gradlew :androidApp:assembleDebug",
        "count": 6,
        "saved_tokens": 38555,
        "avg_savings_pct": 98.18226065576931
      },
      {
        "command": "gradlew :androidApp:clean :androidApp:assembleDebug",
        "count": 6,
        "saved_tokens": 32680,
        "avg_savings_pct": 92.50760139796417
      },
      {
        "command": "gradlew build",
        "count": 9,
        "saved_tokens": 2557,
        "avg_savings_pct": 60.75767841011745
      }
    ]
  }
}
```

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
