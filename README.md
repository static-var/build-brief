# build-brief

`build-brief` is a wrapper-first CLI for Gradle builds that keeps the raw log, reduces noisy output, and emits either a concise human summary or a stable JSON payload for tools and agents.

## What it does

- prefers `./gradlew` when available and falls back to system `gradle`
- injects `--console=plain` unless the caller already set a console mode
- preserves the Gradle exit code exactly
- writes output to a reusable per-project `latest` raw log file for full-detail debugging without unbounded memory growth
- summarizes failed tasks, failed tests, warnings, and final build status
- supports `human`, `json`, and `raw` output modes

## Usage

```bash
build-brief test
build-brief --mode json build
build-brief --version
build-brief --project-dir /path/to/project test
build-brief -- --stacktrace test
```

If you want to pass Gradle flags that look like `build-brief` flags, use `--` to separate them.

## Output modes

- `human` — concise summary for terminal use
- `json` — structured output for CLIs, scripts, and agents
- `raw` — print the captured Gradle output with no reduction

## Raw log behavior

- `build-brief` streams Gradle output directly to disk instead of retaining the whole log in memory
- by default it reuses a per-project `latest` log file under the system temp directory
- this keeps disk usage bounded while still preserving the most recent full log for debugging

## Environment variables

- `BUILD_BRIEF_MODE`
- `BUILD_BRIEF_PROJECT_DIR`
- `BUILD_BRIEF_GRADLE_PATH`
- `BUILD_BRIEF_LOG_DIR`

## Integration helpers

- `examples/instructions/agent-instructions.md`
- `examples/shell/build-brief.zsh`
- `examples/hooks/README.md`

## Current scope

This repository currently implements the wrapper, resolver, reducer, output renderers, reusable raw-log pipeline, strict wrapper-flag parsing, and representative fixture coverage for Spring Boot, Ktor, Android, and KMP-style logs.
