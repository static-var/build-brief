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
- Returns failed tasks, failed tests, warnings, build scan URLs, configured custom regex matches, generated output paths, artifacts, report-task output, and final status
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
build-brief --config .build-brief.json connectedCheck
build-brief gains --history
build-brief doctor
build-brief --help
```

If you want to keep the original command shape explicit, prefer `build-brief gradle ...` for a PATH-resolved Gradle binary and `build-brief ./gradlew ...` for a project-local wrapper.

## Explicit CI mode

Use `--ci` when a job needs CI behavior; it is never inferred from the environment:

```bash
build-brief --ci test
```

`--ci` keeps the normal human summary and Gradle exit code. It rejects `--mode raw` with usage exit 2. In GitHub Actions only (`GITHUB_ACTIONS=true`), untrusted human-summary lines that begin with `::` are neutralized, including after CR line boundaries. A failed build adds one generic `::error` annotation; a partial failed summary adds at most one generic `::warning`. Successful builds add none. These annotations use the bounded synthetic tool-level location `build-brief:1`, not a source location. CI runs do not persist gains history, although token metrics are still calculated for the run.

Raw logs remain local files. GitHub annotations do not upload them: add a separate artifact-upload step if the workflow needs the raw log after the job.

Example successful test run:

```text
$ build-brief test
BUILD SUCCESSFUL in 2s
Tests: 2 passed, 0 failed
Warnings: 1
  - OpenJDK 64-Bit Server VM warning: Sharing is only supported for boot loader classes because bootstrap classpath has been appended
```

Report-style Gradle commands keep their report body in the default human output:

```text
$ build-brief gradle :tasks
> Task :tasks
Tasks runnable from root project 'sample'
Build tasks
-----------
assemble - Assembles the outputs of this project.

BUILD SUCCESSFUL in 400ms
```

Successful tasks can also surface generated output locations when tools print
lines like `AgentPreview report written to: ...`.

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

## Custom regex matches

Project-specific logs often contain useful links that build-brief cannot know
about ahead of time, such as Firebase Test Lab or emulator.wtf result URLs. Add
an optional `.build-brief.json` file in the project root to surface those
matches in the brief:

```json
{
  "matches": [
    {
      "name": "Firebase Test Lab",
      "pattern": "https://console\\.firebase\\.google\\.com/[^\\s]+"
    },
    {
      "name": "emulator.wtf",
      "pattern": "https://app\\.emulator\\.wtf/[^\\s]+"
    }
  ]
}
```

You can also point to a config file with `--config PATH` or
`BUILD_BRIEF_CONFIG`. Relative paths from either source resolve from the
effective project directory: `--project-dir`, then `BUILD_BRIEF_PROJECT_DIR`,
then the current working directory. `--project-dir` takes precedence over
`BUILD_BRIEF_PROJECT_DIR`. Absolute paths remain unchanged. Invalid regex
patterns fail fast before Gradle starts. Configs may contain more rules, but
build-brief retains the first 64 rules and ignores the rest; each retained rule
keeps up to 8 unique matches. `build-brief doctor --config PATH` warns when
rules are ignored.

## Build Brief Doctor

Use `build-brief doctor` to run read-only checks before wrapping a Gradle command. Doctor validates the project directory, optional config file and custom regexes, Gradle resolution, wrapper health, Build Brief environment overrides, and basic install health. It does not execute Gradle or modify files.

```bash
build-brief doctor
build-brief doctor --project-dir /path/to/project
build-brief doctor --config .build-brief.json
```

Doctor exits `0` when no checks fail, `1` when one or more checks fail, and `2` for doctor usage errors. Warnings do not fail the command.

## Check your gains

`build-brief` records rough token savings automatically whenever it wraps a Gradle command. Use the `gains` subcommand to inspect totals, recent runs, project-only scope, or machine-readable output:

```bash
build-brief gains
build-brief gains --history
build-brief gains --project
build-brief gains --format json
build-brief gains --reset
```

The savings numbers use the built-in chars-divided-by-4 heuristic. They are useful for rough feedback and trend tracking, not billing-grade accounting. Gains history is stored only in a local JSONL file under the OS config directory (`build-brief/tracking.jsonl`); no gains data is transmitted. Entries older than 90 days are pruned when the next tracked run is recorded, so inactive history may remain until then or `build-brief gains --reset`.

Example text output:

```text
build-brief Token Savings (Global Scope)
============================================================

Recorded period: 2026-03-01 to 2026-03-21 (21 days, 38 commands)

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

Local-only: Gains history stays on this machine. This report sends no gains data.
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
For Claude Code, `build-brief --global` now installs a managed local plugin plus
the usual `CLAUDE.md` guidance when Claude is detected.
For GitHub Copilot CLI, `build-brief --global` now installs a managed local plugin
with a `preToolUse` guardrail when Copilot is detected.
For Codex App & CLI, `build-brief --global` installs a managed local plugin,
marketplace entry, and hook-backed guardrail in the shared `~/.codex` config area
alongside the AGENTS integration when Codex is detected. It uses `hooks` plus
plugin-bundled hooks, pre-trusts the installed build-brief hook, and removes the
deprecated `codex_hooks` flag if present.

If your agent tool supports `AGENTS.md` or an instructions file, a simple default rule is:

```md
Use `build-brief` for routine Gradle commands.
Prefer `build-brief gradle ...` or `build-brief ./gradlew ...` over raw Gradle calls.
For chained shell commands, rewrite each Gradle segment, for example `build-brief gradle test && build-brief gradle check`.
Use the default output for report-style Gradle commands like `tasks`, `help`, `projects`, `dependencies`, and `dependencyInsight`; build-brief preserves their report body.
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
